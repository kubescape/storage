package file

import (
	"context"
	"fmt"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Storage kinds for container profile artifacts. ContainerProfileKind is the
// canonical observed CP produced by time-series consolidation.
const (
	ContainerProfileKind       = "containerprofile"
	ContainerProfileKindPlural = "containerprofiles"
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

// GetContainerProfileMetadataNoLock reads container profile metadata without
// acquiring the per-key lock. GetContainerProfileMetadata takes a read lock via
// GetWithConn; when PreSave runs from inside GuaranteedUpdate, the write lock for
// the same key is already held, so a read lock there would self-deadlock. The
// metadata branch of get() reads straight from SQLite and takes no lock of its
// own, so hasWriteLock (caller already holds the lock) is passed to skip any
// lock management.
func (c *ContainerProfileStorageImpl) GetContainerProfileMetadataNoLock(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	profile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.get(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &profile, hasWriteLock)
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
	err := c.storageImpl.get(ctx, conn, key, storage.GetOptions{}, &tsProfile, noLock) // get instead of GetWithConn to bypass locking
	return tsProfile, err
}

func (c *ContainerProfileStorageImpl) SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		return profile, nil, nil
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	// cachedExistingObject is deliberately nil. Passing a non-nil value (even an
	// empty object) tells GuaranteedUpdate to treat it as the current on-disk
	// state and skip the read-from-disk, so its "same serialized contents"
	// short-circuit compares the freshly consolidated profile against an empty
	// object — never equal — and rewrites the observed CP (bumping its
	// ResourceVersion) on every consolidation tick that carries new time-series
	// data, even when the consolidated content is byte-identical to what is
	// already persisted. That spurious RV bump then propagates to the merged CP
	// (whose merged-source-observed-rv annotation tracks observed.ResourceVersion),
	// refreshing it once per node-agent report. profile already carries the
	// persisted CP's identity (ResourceVersion, UID, creationTimestamp,
	// SyncChecksum) from loadOrInitializeProfile, so reading the real current
	// state lets an unchanged consolidation compare equal and skip the write
	// (kubescape/storage#315 review).
	err := c.storageImpl.GuaranteedUpdateWithConn(cpCtx, conn, key, &softwarecomposition.ContainerProfile{},
		true, nil, tryUpdate, nil, "")
	if err != nil {
		return fmt.Errorf("failed to update container profile: %w", err)
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

func IsContainerProfileKind(kind string) bool {
	return kind == ContainerProfileKind || kind == ContainerProfileKindPlural
}

func NormalizeContainerProfileKind(kind string) string {
	if kind == ContainerProfileKindPlural {
		return ContainerProfileKind
	}
	return kind
}
