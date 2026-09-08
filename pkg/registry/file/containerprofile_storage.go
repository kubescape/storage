package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
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
	// input is the persisted object, read under Lock(key) at write time. Every
	// writer of a base key holds Lock(key) across both the payload rename and
	// the row's commit, so a completer that finished before this lock was
	// taken is fully visible here and one that has not finished is excluded.
	// A Completed/Full persisted object is never overwritten with merged data:
	// the pass's transaction rolls back and the next tick takes the frozen
	// gate in updateProfile, whose predicate is exactly this one.
	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		if cur, ok := input.(*softwarecomposition.ContainerProfile); ok && softwarecomposition.IsCompletedFull(cur.Annotations) {
			metrics.IncConsolidationFrozenRefusals()
			return nil, nil, ErrProfileFrozen
		}
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
	//
	// singleWriterEnabled (spike/single-writer-priority-queue): route this
	// consolidation write through the single writer's LOW priority lane
	// instead of GuaranteedUpdateWithConn's caller-supplied-connection,
	// per-key-lock path -- UNLESS the caller already holds an active connection
	// / transaction in ctx (e.g. from WithConnection / BeginTransaction), in which
	// case attempting to acquire another connection for writes deadlocks against
	// the active transaction in SQLite.
	if singleWriterEnabled && ctx.Value(connKey) == nil {
		if err := c.storageImpl.guaranteedUpdateSingleWriter(cpCtx, key, &softwarecomposition.ContainerProfile{},
			true, nil, tryUpdate, nil, "", priorityLow); err != nil {
			return fmt.Errorf("failed to update container profile: %w", err)
		}
		return nil
	}

	conn := ctx.Value(connKey).(*sqlite.Conn)
	err := c.storageImpl.GuaranteedUpdateWithConn(cpCtx, conn, key, &softwarecomposition.ContainerProfile{},
		true, nil, tryUpdate, nil, "")
	if err != nil {
		return fmt.Errorf("failed to update container profile: %w", err)
	}

	return nil
}

// healFailure names the step of HealDivergence that failed; the reason is a
// metrics.HealFailed* label value.
type healFailure struct {
	reason string
	err    error
}

func (e *healFailure) Error() string { return e.reason + ": " + e.err.Error() }
func (e *healFailure) Unwrap() error { return e.err }

func healFailureReason(err error) string {
	var hf *healFailure
	if errors.As(err, &hf) {
		return hf.reason
	}
	return "unknown"
}

// HealDivergence re-persists a base ContainerProfile whose payload says
// Completed/Full while its committed metadata row does not -- the shape a
// process crash or a failed COMMIT leaves between saveObject's payload rename
// and the row's commit. The payload WAS the completed profile; only its
// durable announcement was lost, so the minimal write that restores agreement
// is the payload's own content, as-is: RV+1, Spec, status and lifecycle
// annotations preserved, checksum annotation and managedFields rewritten as on
// every save, no merge, no new data, and the one Modified the crash lost,
// dispatched after COMMIT.
//
// Lock order is Lock(key) then SQLite's write lock (REST's order). The
// BEGIN IMMEDIATE takes the write lock FIRST, so any in-flight writer's COMMIT
// or ROLLBACK has landed before the row is re-read: a completer that just
// committed wins and nothing is written. That wait is bounded by the
// connection's busy timeout (DefaultBusyTimeout, 60s), with Lock(key) held.
// A row that is absent rather than divergent is left alone: the caller never
// asks for a heal on one, and this function does not resurrect a deleted key.
//
// saveObject is called directly, not through SaveContainerProfile:
// GuaranteedUpdateWithConn compares tryUpdate's result with the object it
// read and writes nothing when they are equal, so an as-is re-save through it
// is a no-op by construction (the legacy-format migration re-save has the
// same need and does the same). This is therefore the one deliberate writer
// of a Completed/Full base that the completed-immutability guards do not
// consult; it writes the base's own content and is counted.
//
// Must run in autocommit, before the pass's transaction (it opens its own).
func (c *ContainerProfileStorageImpl) HealDivergence(ctx context.Context, key string) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	s := c.storageImpl
	lockCtx, cancel := context.WithTimeout(ctx, lockTimeout)
	defer cancel()
	if err := s.locks.Lock(lockCtx, key); err != nil {
		return &healFailure{reason: metrics.HealFailedLockTimeout, err: newContentionTimeoutError("heal", key, err)}
	}
	defer s.locks.Unlock(key)

	var cur softwarecomposition.ContainerProfile
	var metaEvent runtime.Object
	err := func() (err error) {
		endFn, terr := sqlitex.ImmediateTransaction(conn)
		if terr != nil {
			return &healFailure{reason: metrics.HealFailedBegin, err: terr}
		}
		defer func() {
			endFn(&err)
			// endFn replaces a nil err with the COMMIT's own error (a ROLLBACK
			// failure panics), so any error that is not already a healFailure
			// is a failed COMMIT: the payload was renamed, the row rolled back
			// -- the shape this heal repairs, one version further ahead.
			var hf *healFailure
			if err != nil && !errors.As(err, &hf) {
				err = &healFailure{reason: metrics.HealFailedCommit, err: err}
			}
		}()
		raw, rerr := ReadMetadata(conn, key)
		if errors.Is(rerr, ErrMetadataNotFound) {
			// No row at all is not divergence but absence (a crash between
			// the row's delete and the payload's remove): resurrecting the
			// key from its payload is not this heal's business.
			return nil
		}
		if rerr != nil {
			return &healFailure{reason: metrics.HealFailedRead, err: rerr}
		}
		var row softwarecomposition.ContainerProfile
		if uerr := json.Unmarshal(raw, &row); uerr != nil {
			return &healFailure{reason: metrics.HealFailedRead, err: uerr}
		}
		if softwarecomposition.IsCompletedFull(row.Annotations) {
			// A concurrent completer won: nothing to heal.
			return nil
		}
		if gerr := s.get(ctx, conn, key, storage.GetOptions{}, &cur, hasWriteLock); gerr != nil {
			return &healFailure{reason: metrics.HealFailedRead, err: gerr}
		}
		if !softwarecomposition.IsCompletedFull(cur.Annotations) {
			// The payload changed under us (a migration re-save, a REST reset): not our case.
			return nil
		}
		metaEvent, err = s.saveObject(conn, key, &cur, &softwarecomposition.ContainerProfile{}, "")
		if err != nil {
			return &healFailure{reason: metrics.HealFailedSave, err: err}
		}
		return nil
	}()
	if err != nil || metaEvent == nil {
		return err
	}
	metrics.IncConsolidationDivergence(metrics.DivergencePayloadAhead)
	logger.L().Warning("HealDivergence - payload was Completed/Full but the metadata row was not; re-persisted the payload and dispatched the lost completion event",
		loggerhelpers.String("key", key), loggerhelpers.String("resourceVersion", cur.ResourceVersion))
	s.watchDispatcher.Modified(key, metaEvent, &cur)
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
