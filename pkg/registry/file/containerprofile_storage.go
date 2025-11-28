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

// SQLiteTransaction wraps a SQLite connection to implement the Transaction interface.
// This allows the connection to be passed through the abstract ContainerProfileStorage
// interface while maintaining type safety for SQLite-specific operations.
type SQLiteTransaction struct {
	conn      *sqlite.Conn
	pool      *sqlitemigration.Pool
	committed bool
}

// transaction implements the Transaction interface marker method
func (t *SQLiteTransaction) transaction() {}

// Conn returns the underlying SQLite connection.
// This is useful for SQLite-specific operations that need direct access.
func (t *SQLiteTransaction) Conn() *sqlite.Conn {
	return t.conn
}

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

// getSQLiteConn extracts the SQLite connection from an abstract Transaction.
// Returns an error if the transaction type doesn't match.
func getSQLiteConn(tx Transaction) (*sqlite.Conn, error) {
	if tx == nil {
		return nil, ErrInvalidTransactionType
	}
	sqliteTx, ok := tx.(*SQLiteTransaction)
	if !ok {
		return nil, ErrInvalidTransactionType
	}
	return sqliteTx.conn, nil
}

// BeginTransaction acquires a connection from the pool and returns it wrapped as a Transaction.
func (c *ContainerProfileStorageImpl) BeginTransaction(ctx context.Context) (Transaction, error) {
	conn, err := c.pool.Take(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to take connection from pool: %w", err)
	}
	return &SQLiteTransaction{conn: conn, pool: c.pool}, nil
}

// CommitTransaction commits the transaction and returns the connection to the pool.
func (c *ContainerProfileStorageImpl) CommitTransaction(tx Transaction) error {
	sqliteTx, ok := tx.(*SQLiteTransaction)
	if !ok || sqliteTx.conn == nil {
		return ErrInvalidTransactionType
	}
	if sqliteTx.committed {
		return nil // already committed
	}
	sqliteTx.committed = true
	c.pool.Put(sqliteTx.conn)
	return nil
}

// CloseTransaction closes the transaction and returns the connection to the pool.
// This is safe to call multiple times or after commit.
func (c *ContainerProfileStorageImpl) CloseTransaction(tx Transaction) {
	sqliteTx, ok := tx.(*SQLiteTransaction)
	if !ok || sqliteTx.conn == nil || sqliteTx.committed {
		return
	}
	sqliteTx.committed = true
	c.pool.Put(sqliteTx.conn)
}

// BeginNestedTransaction starts a SQLite transaction (savepoint) and returns a function
// to commit or rollback based on the error state.
func (c *ContainerProfileStorageImpl) BeginNestedTransaction(tx Transaction) (func(*error), error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return nil, err
	}
	return sqlitex.Transaction(conn), nil
}

func (c *ContainerProfileStorageImpl) DeleteContainerProfile(ctx context.Context, tx Transaction, key string) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}
	return c.storageImpl.delete(ctx, conn, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
}

func (c *ContainerProfileStorageImpl) GetContainerProfile(ctx context.Context, tx Transaction, key string) (softwarecomposition.ContainerProfile, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return softwarecomposition.ContainerProfile{}, err
	}
	profile := softwarecomposition.ContainerProfile{}
	err = c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) GetContainerProfileMetadata(ctx context.Context, tx Transaction, key string) (softwarecomposition.ContainerProfile, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return softwarecomposition.ContainerProfile{}, err
	}
	profile := softwarecomposition.ContainerProfile{}
	err = c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) GetSbom(ctx context.Context, tx Transaction, key string) (softwarecomposition.SBOMSyft, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return softwarecomposition.SBOMSyft{}, err
	}
	sbom := softwarecomposition.SBOMSyft{}
	err = c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &sbom)
	return sbom, err
}

func (c *ContainerProfileStorageImpl) GetStorageImpl() *StorageImpl {
	return c.storageImpl
}

func (c *ContainerProfileStorageImpl) GetTsContainerProfile(ctx context.Context, tx Transaction, key string) (softwarecomposition.ContainerProfile, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return softwarecomposition.ContainerProfile{}, err
	}
	tsProfile := softwarecomposition.ContainerProfile{}
	err = c.storageImpl.get(ctx, conn, key, storage.GetOptions{}, &tsProfile) // get instead of GetWithConn to bypass locking
	return tsProfile, err
}

func (c *ContainerProfileStorageImpl) SaveContainerProfile(ctx context.Context, tx Transaction, key string, profile *softwarecomposition.ContainerProfile) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		return profile, nil, nil
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	err = c.storageImpl.GuaranteedUpdateWithConn(cpCtx, conn, key, &softwarecomposition.ContainerProfile{},
		true, nil, tryUpdate, &softwarecomposition.ContainerProfile{}, "")
	if err != nil {
		return fmt.Errorf("failed to update container profile: %w", err)
	}

	return nil
}

func (c *ContainerProfileStorageImpl) UpdateApplicationProfile(
	ctx context.Context,
	tx Transaction,
	key, prefix, root, namespace, slug, wlid string,
	instanceID interface{ GetStringNoContainer() string },
	profile *softwarecomposition.ContainerProfile,
	creationTimestamp metav1.Time,
	getAggregatedData GetAggregatedDataFunc,
) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}

	apKey := keysToPath(prefix, root, "applicationprofiles", namespace, slug)
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

		status, completion, checksum := getAggregatedData(ctx, tx, key, ap.Parts)
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

	err = c.storageImpl.GuaranteedUpdateWithConn(apCtx, conn, apKey, &softwarecomposition.ApplicationProfile{},
		true, nil, tryUpdate, nil, apChecksum)
	if err != nil {
		return fmt.Errorf("failed to update application profile: %w", err)
	}

	return nil
}

func (c *ContainerProfileStorageImpl) UpdateNetworkNeighborhood(
	ctx context.Context,
	tx Transaction,
	key, prefix, root, namespace, slug, wlid string,
	instanceID interface{ GetStringNoContainer() string },
	profile *softwarecomposition.ContainerProfile,
	creationTimestamp metav1.Time,
	getAggregatedData GetAggregatedDataFunc,
) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}

	nnKey := keysToPath(prefix, root, "networkneighborhoods", namespace, slug)
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

		status, completion, checksum := getAggregatedData(ctx, tx, key, nn.Parts)
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

	err = c.storageImpl.GuaranteedUpdateWithConn(nnCtx, conn, nnKey, &softwarecomposition.NetworkNeighborhood{},
		true, nil, tryUpdate, nil, nnChecksum)
	if err != nil {
		return fmt.Errorf("failed to update network neighborhood: %w", err)
	}

	return nil
}

// Time Series Operations

func (c *ContainerProfileStorageImpl) ListTimeSeriesExpired(ctx context.Context, tx Transaction, threshold time.Duration) ([]string, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return nil, err
	}
	return ListTimeSeriesExpired(conn, threshold)
}

func (c *ContainerProfileStorageImpl) ListTimeSeriesWithData(ctx context.Context, tx Transaction) ([]string, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return nil, err
	}
	return ListTimeSeriesWithData(conn)
}

func (c *ContainerProfileStorageImpl) ListTimeSeriesContainers(ctx context.Context, tx Transaction, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return nil, err
	}
	return ListTimeSeriesContainers(conn, key)
}

func (c *ContainerProfileStorageImpl) DeleteTimeSeriesContainerEntries(ctx context.Context, tx Transaction, key string) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}
	return DeleteTimeSeriesContainerEntries(conn, key)
}

func (c *ContainerProfileStorageImpl) ReplaceTimeSeriesContainerEntries(ctx context.Context, tx Transaction, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}
	return ReplaceTimeSeriesContainerEntries(conn, key, seriesID, deleteTimeSeries, newTimeSeries)
}

func (c *ContainerProfileStorageImpl) WriteTimeSeriesEntry(ctx context.Context, tx Transaction, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	conn, err := getSQLiteConn(tx)
	if err != nil {
		return err
	}
	return WriteTimeSeriesEntry(conn, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData)
}
