package file

import (
	"context"
	"fmt"
	"time"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ContainerProfileStorageImpl implements ContainerProfileStorage using SQLite as the backend.
type ContainerProfileStorageImpl struct {
	storageImpl *StorageImpl
	pool        *sqlitemigration.Pool
}

// NewContainerProfileStorageImpl creates a new SQLite-backed ContainerProfileStorage.
func NewContainerProfileStorageImpl(storageImpl *StorageImpl, pool *sqlitemigration.Pool) *ContainerProfileStorageImpl {
	return &ContainerProfileStorageImpl{
		storageImpl: storageImpl,
		pool:        pool,
	}
}

var _ ContainerProfileStorage = (*ContainerProfileStorageImpl)(nil)

// WithConnection acquires a connection from the pool and returns a new context
// with the connection embedded, plus a cleanup function to return the connection to the pool.
func (c *ContainerProfileStorageImpl) WithConnection(ctx context.Context) (context.Context, func(), error) {
	conn, err := c.pool.Take(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to take connection from pool: %w", err)
	}
	var cleaned bool
	cleanup := func() {
		if !cleaned {
			cleaned = true
			c.pool.Put(conn)
		}
	}
	return context.WithValue(ctx, connKey, conn), cleanup, nil
}

// BeginTransaction starts a SQLite transaction (savepoint) and returns a function
// to commit or rollback based on the error state.
func (c *ContainerProfileStorageImpl) BeginTransaction(ctx context.Context) (func(*error), error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return sqlitex.Transaction(conn), nil
}

func (c *ContainerProfileStorageImpl) DeleteContainerProfile(ctx context.Context, key string) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return c.storageImpl.delete(ctx, conn, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
}

func (c *ContainerProfileStorageImpl) GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	profile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) GetContainerProfileMetadata(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	profile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) GetSbom(ctx context.Context, key string) (softwarecomposition.SBOMSyft, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	sbom := softwarecomposition.SBOMSyft{}
	err := c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &sbom)
	return sbom, err
}

func (c *ContainerProfileStorageImpl) GetStorageImpl() *StorageImpl {
	return c.storageImpl
}

func (c *ContainerProfileStorageImpl) GetTsContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	tsProfile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.get(ctx, conn, key, storage.GetOptions{}, &tsProfile) // get instead of GetWithConn to bypass locking
	return tsProfile, err
}

func (c *ContainerProfileStorageImpl) SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		return profile, nil, nil
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	err := c.storageImpl.GuaranteedUpdateWithConn(cpCtx, conn, key, &softwarecomposition.ContainerProfile{},
		true, nil, tryUpdate, &softwarecomposition.ContainerProfile{}, "")
	if err != nil {
		return fmt.Errorf("failed to update container profile: %w", err)
	}

	return nil
}

func (c *ContainerProfileStorageImpl) UpdateApplicationProfile(ctx context.Context, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time, getAggregatedData GetAggregatedDataFunc) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	apKey := KeysToPath(prefix, root, "applicationprofiles", namespace, slug)
	var apChecksum string

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		output := input.DeepCopyObject()
		ap, ok := output.(*softwarecomposition.ApplicationProfile)
		if !ok {
			return nil, nil, fmt.Errorf("given object is not an ApplicationProfile")
		}

		ap.Name = slug
		ap.Namespace = namespace
		if ap.CreationTimestamp.IsZero() {
			ap.CreationTimestamp = creationTimestamp
		}
		ap.SchemaVersion = SchemaVersion
		if ap.Parts == nil {
			ap.Parts = map[string]string{}
		}
		ap.Parts[key] = "" // checksum will be updated by getAggregatedData

		status, completion, checksum := getAggregatedData(ctx, key, ap.Parts)
		apChecksum = checksum

		ap.Annotations = map[string]string{
			helpers.CompletionMetadataKey: completion,
			helpers.InstanceIDMetadataKey: instanceID.GetStringNoContainer(),
			helpers.StatusMetadataKey:     status,
			helpers.WlidMetadataKey:       wlid,
		}
		ap.Labels = map[string]string{}
		utils.MergeMaps(ap.Labels, profile.Labels)
		delete(ap.Labels, helpers.ContainerNameMetadataKey)

		return output, nil, nil
	}

	apCtx, apCancel := context.WithTimeout(ctx, 5*time.Second)
	defer apCancel()

	err := c.storageImpl.GuaranteedUpdateWithConn(apCtx, conn, apKey, &softwarecomposition.ApplicationProfile{},
		true, nil, tryUpdate, nil, apChecksum)
	if err != nil {
		return fmt.Errorf("failed to update application profile: %w", err)
	}

	return nil
}

func (c *ContainerProfileStorageImpl) UpdateNetworkNeighborhood(ctx context.Context, key, prefix, root, namespace, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time, getAggregatedData GetAggregatedDataFunc) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	nnKey := KeysToPath(prefix, root, "networkneighborhoods", namespace, slug)
	var nnChecksum string

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		output := input.DeepCopyObject()
		nn, ok := output.(*softwarecomposition.NetworkNeighborhood)
		if !ok {
			return nil, nil, fmt.Errorf("given object is not an NetworkNeighborhood")
		}

		nn.Name = slug
		nn.Namespace = namespace
		if nn.CreationTimestamp.IsZero() {
			nn.CreationTimestamp = creationTimestamp
		}
		nn.SchemaVersion = SchemaVersion
		if nn.Parts == nil {
			nn.Parts = map[string]string{}
		}
		nn.Parts[key] = "" // checksum will be updated by getAggregatedData

		status, completion, checksum := getAggregatedData(ctx, key, nn.Parts)
		nnChecksum = checksum

		nn.Annotations = map[string]string{
			helpers.CompletionMetadataKey: completion,
			helpers.InstanceIDMetadataKey: instanceID.GetStringNoContainer(),
			helpers.StatusMetadataKey:     status,
			helpers.WlidMetadataKey:       wlid,
		}
		nn.Labels = map[string]string{}
		utils.MergeMaps(nn.Labels, profile.Labels)
		delete(nn.Labels, helpers.ContainerNameMetadataKey)

		return output, nil, nil
	}

	nnCtx, nnCancel := context.WithTimeout(ctx, 5*time.Second)
	defer nnCancel()

	err := c.storageImpl.GuaranteedUpdateWithConn(nnCtx, conn, nnKey, &softwarecomposition.NetworkNeighborhood{},
		true, nil, tryUpdate, nil, nnChecksum)
	if err != nil {
		return fmt.Errorf("failed to update network neighborhood: %w", err)
	}

	return nil
}

// Time Series Operations

func (c *ContainerProfileStorageImpl) ListTimeSeriesExpired(ctx context.Context, threshold time.Duration) ([]string, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ListTimeSeriesExpired(conn, threshold)
}

func (c *ContainerProfileStorageImpl) ListTimeSeriesWithData(ctx context.Context) ([]string, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ListTimeSeriesWithData(conn)
}

func (c *ContainerProfileStorageImpl) ListTimeSeriesContainers(ctx context.Context, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ListTimeSeriesContainers(conn, key)
}

func (c *ContainerProfileStorageImpl) DeleteTimeSeriesContainerEntries(ctx context.Context, key string) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return DeleteTimeSeriesContainerEntries(conn, key)
}

func (c *ContainerProfileStorageImpl) ReplaceTimeSeriesContainerEntries(ctx context.Context, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ReplaceTimeSeriesContainerEntries(conn, key, seriesID, deleteTimeSeries, newTimeSeries)
}

func (c *ContainerProfileStorageImpl) WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return WriteTimeSeriesEntry(conn, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData)
}
