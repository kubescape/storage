package file

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

const (
	DefaultStorageRoot       = "/data"
	GobExt                   = ".g"
	MetadataExt              = ".m"
	SchemaVersion            = int64(1)
	StorageV1Beta1ApiVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
	connKey                  = "conn"
	operationNotSupportedMsg = "operation not supported"
)

var (
	ObjectCompletedError = errors.New("object is completed")
	ObjectTooLargeError  = errors.New("object is too large")
)

// lockTimeout is the hardcoded backstop for lock acquisition. It sits well under the
// ~60s outer apiserver request deadline so a contended request fails fast with a
// Retry-After signal instead of hanging to the full request timeout.
//
// NOTE: this is a package-level var, not a const, so unit tests can shrink it to a few
// milliseconds and exercise the real timeout path without a real 5s wait. It is never
// mutated at runtime; only tests override it (and restore it via defer).
var lockTimeout = 5 * time.Second

// poolTimeout is the hardcoded backstop for acquiring a *sqlite.Conn from the pool. It used
// to be a full minute (long enough that a pool-exhaustion stall would blow past the k8s
// apiserver's own outer non-long-running-request timeout, typically ~34s, before this
// package's own error ever had a chance to fire) and is now bounded the same way lockTimeout
// bounds lock acquisition: well under that outer deadline, with the same fail-fast
// ServerTimeout+Retry-After signal on expiry instead of an opaque "take connection" error.
//
// NOTE: package-level var, not const, for the same test-overridability reason as lockTimeout.
var poolTimeout = 5 * time.Second

// newContentionTimeoutError returns a ServerTimeout (HTTP 500, Reason=ServerTimeout) carrying
// a positive RetryAfterSeconds so the apiserver emits a Retry-After header that client-go's
// retry logic honors. It is used for both lock acquisition (s.locks.Lock/RLock) and
// connection-pool acquisition (s.pool.Take) timeouts: both are internal contention points
// that should fail fast with the same backoff-friendly signal rather than hang to the outer
// request deadline. op is "create"/"get"/"update"/"delete"/"list".
//
// The underlying err (e.g. context.DeadlineExceeded from the child context) is folded into
// the observability trail rather than discarded: log it at Debug with the op/key so a
// contended-vs-cancelled acquisition can be told apart post-hoc. NewServerTimeout does not
// take a wrapped cause, so err is logged here (not embedded in the StatusError).
//
// If err is context.Canceled (the caller's own ctx was cancelled, e.g. a client disconnect,
// rather than our lockTimeout/poolTimeout child context expiring), a Retry-After signal is
// misleading -- there is no client left to retry -- so this returns a plain InternalError
// without one instead of ServerTimeout.
func newContentionTimeoutError(op, key string, err error) *apierrors.StatusError {
	if errors.Is(err, context.Canceled) {
		logger.L().Debug("acquisition cancelled",
			helpers.String("op", op), helpers.String("key", key), helpers.Error(err))
		return apierrors.NewInternalError(fmt.Errorf("%s %s: acquisition cancelled: %w", op, key, err))
	}
	logger.L().Debug("acquisition timed out",
		helpers.String("op", op), helpers.String("key", key), helpers.Error(err))
	return apierrors.NewServerTimeout(
		schema.GroupResource{Group: softwarecomposition.GroupName, Resource: resourceFromKey(key)},
		op, 1)
}

// connAttemptTimeout bounds a single pool.Take attempt inside
// acquireLockedConn's lock-then-connection retry loop. It must stay well
// under lockTimeout so a lock can be released and retried several times
// within the overall poolTimeout budget, rather than pinning the lock while
// blocking indefinitely on pool.Take -- see .omc/plans/storage-locking-rewrite.md,
// Phase 1 step 1 (RC1: connection-before-lock ordering).
//
// NOTE: package-level var, not const, for the same test-overridability reason
// as lockTimeout/poolTimeout.
var connAttemptTimeout = 250 * time.Millisecond

// acquireLockedConn implements RC1's corrected lock-then-connection ordering
// for the single-key, connection-less REST entry points (Get, Delete): it
// acquires the per-key lock via acquire first, then attempts a pool
// connection within a short, bounded sub-window (connAttemptTimeout). If no
// connection is available in that window, it releases the lock (via
// release) and retries from lock acquisition -- it never blocks
// indefinitely on a connection while holding the lock, which is what let a
// stalled lock wait pin an idle pool connection before this fix (previously,
// these wrappers took a connection first and only acquired the lock inside
// the *WithConn core, so the connection sat idle for the whole lock wait).
//
// ctx is the caller's real request context, used for two independent
// purposes with different lifetimes:
//   - It parents the "waiting for lock" trace span (so it nests under the
//     caller's own request span rather than showing up as an orphaned root)
//     and, via the internal budgetCtx below, lets a caller's own
//     cancellation (client disconnect) abort a stalled lock/connection wait
//     early instead of always running the full poolTimeout budget.
//   - It is the interrupt source rebound onto the returned connection (see
//     below), so the connection stays valid for as long as the caller
//     actually holds it -- which can outlast the bounded acquisition budget
//     itself (e.g. a slow decode after a long lock wait).
//
// The overall lock+connection retry budget is internally bounded to
// poolTimeout via budgetCtx (context.WithTimeout(ctx, poolTimeout)), not tied
// to ctx's own (possibly absent, possibly much longer) deadline. This is
// required, not optional: TestStorageImpl_PoolContentionReturnsServerTimeout
// calls with context.Background() (no deadline); context.WithTimeout always
// adds its own deadline regardless of the parent's, so the shrunk poolTimeout
// still applies and that test still times out rather than hanging forever.
// Each lock-acquisition attempt is separately bounded by lockTimeout (nested
// under budgetCtx, so it can only ever be shorter, never longer, than the
// remaining overall budget) -- this keeps
// TestStorageImpl_LockContentionReturnsServerTimeout's shrunk lockTimeout
// still failing fast on a genuinely contended key lock.
//
// A retrying goroutine re-enters lock acquisition through acquire's normal
// path on every attempt; it relies on pkg/utils/mutex.go's MapMutex.Lock
// fast-path fix (checking pendingWriters, mirroring RLock's own check) so
// that a rapid stream of retries here cannot barge ahead of another,
// already-queued writer indefinitely (e.g. the consolidation path, which
// holds a connection while blocked on the same key's write lock).
//
// On success, the caller owns both the lock (must call release(key)) and
// the connection (must call s.pool.Put(conn)).
func (s *StorageImpl) acquireLockedConn(ctx context.Context, op, key string, acquire func(context.Context, string) error, release func(string)) (*sqlite.Conn, error) {
	budgetCtx, budgetCancel := context.WithTimeout(ctx, poolTimeout)
	defer budgetCancel()
	for {
		_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
		beforeLock := time.Now()
		lockCtx, lockCancel := context.WithTimeout(budgetCtx, lockTimeout)
		lockErr := acquire(lockCtx, key)
		lockCancel()
		spanLock.End()
		if lockErr != nil {
			return nil, newContentionTimeoutError(op, key, lockErr)
		}
		lockDuration := time.Since(beforeLock)
		if lockDuration > time.Second {
			logger.L().Debug(op, helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
		}

		connCtx, connCancel := context.WithTimeout(budgetCtx, connAttemptTimeout)
		conn, connErr := s.pool.Take(connCtx)
		if connErr == nil {
			// Pool.Take binds the returned conn's interrupt source to
			// connCtx (see sqlitex.Pool.Take/sqlite.Conn.SetInterrupt) --
			// cancelling connCtx immediately below would otherwise mark
			// conn permanently interrupted for its entire subsequent use.
			// Rebind to the caller's real, full-lifetime ctx (alive for as
			// long as the caller holds the connection -- unlike budgetCtx,
			// which expires at poolTimeout regardless of whether the caller
			// is still legitimately using the connection) before releasing
			// connCtx.
			conn.SetInterrupt(ctx.Done())
			connCancel()
			return conn, nil
		}
		connCancel()

		release(key)
		if budgetCtx.Err() != nil {
			return nil, newContentionTimeoutError(op, key, connErr)
		}
		// Overall budget not yet exhausted: loop back to lock acquisition.
		// connAttemptTimeout's own wait (pool.Take blocks up to its context
		// deadline when the pool is genuinely exhausted, per
		// sqlitemigration.Pool.Take) provides natural pacing between
		// retries, so no additional backoff is added here.
	}
}

// resourceFromKey extracts an approximate plural resource name from a storage key for use
// in the error's informational Details/message; correctness rides on RetryAfterSeconds, so
// an approximate resource string is acceptable.
func resourceFromKey(key string) string {
	_, _, kind, _, _, _ := K8sPathToKeys(key)
	kind = strings.ToLower(kind)
	if kind == "" {
		return "containerprofiles"
	}
	if !strings.HasSuffix(kind, "s") {
		kind += "s"
	}
	return kind
}

type objState struct {
	obj  runtime.Object
	meta *storage.ResponseMeta
	rev  int64
	data []byte
}

// StorageImpl offers a common interface for object marshaling/unmarshaling operations and
// hides all the storage-related operations behind it.
type StorageImpl struct {
	appFs     afero.Fs
	pool      *sqlitemigration.Pool
	locks     utils.MapMutex[string]
	processor Processor
	// writeMetadataFn is the metadata persistence step of saveObject; a field so
	// tests can inject failures to pin the atomic write order (issue #44).
	writeMetadataFn func(conn *sqlite.Conn, path string, metadata runtime.Object) error
	// renamePayloadFn is the payload visibility step of saveObject; a field so
	// tests can inject rename failures and pin the rollback contract.
	renamePayloadFn func(oldpath, newpath string) error
	root            string
	scheme          *runtime.Scheme
	versioner       storage.Versioner
	watchDispatcher *WatchDispatcher
}

func (s *StorageImpl) EnableResourceSizeEstimation(keysFunc storage.KeysFunc) error {
	return nil
}

// openPayloadFileWithFallback opens a payload file with O_DIRECT when supported
// and transparently retries without it when the filesystem returns an
// "unsupported" error (e.g. EINVAL on tmpfs/overlayfs).
func (s *StorageImpl) openPayloadFileWithFallback(path string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := s.appFs.OpenFile(path, openFlagDirect|flag, perm)
	if err != nil && isDirectIOUnsupported(err) {
		f, err = s.appFs.OpenFile(path, flag, perm)
	}
	return f, err
}

func (s *StorageImpl) Stats(_ context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (s *StorageImpl) SetKeysFunc(_ storage.KeysFunc) {
	//TODO implement me
	panic("implement me")
}

func (s *StorageImpl) CompactRevision() int64 {
	//TODO implement me
	panic("implement me")
}

// StorageQuerier wraps the storage.Interface and adds some extra methods which are used by the storage implementation.
type StorageQuerier interface {
	storage.Interface
	CalculateChecksum(in runtime.Object) (string, error)
	GetByNamespace(ctx context.Context, apiVersion, kind, namespace string, listObj runtime.Object) error
	GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error
}

var _ storage.Interface = (*StorageImpl)(nil)

var _ StorageQuerier = (*StorageImpl)(nil)

func NewStorageImpl(appFs afero.Fs, root string, pool *sqlitemigration.Pool, watchDispatcher *WatchDispatcher, scheme *runtime.Scheme) StorageQuerier {
	return NewStorageImplWithCollector(appFs, root, pool, watchDispatcher, scheme, DefaultProcessor{})
}

func NewStorageImplWithCollector(appFs afero.Fs, root string, conn *sqlitemigration.Pool, watchDispatcher *WatchDispatcher, scheme *runtime.Scheme, processor Processor) StorageQuerier {
	if watchDispatcher == nil {
		watchDispatcher = NewWatchDispatcher()
	}
	storageImpl := &StorageImpl{
		appFs:           appFs,
		pool:            conn,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		writeMetadataFn: writeMetadata,
		root:            root,
		scheme:          scheme,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: watchDispatcher,
	}
	processor.SetStorage(NewContainerProfileStorageImpl(storageImpl, conn))
	return storageImpl
}

func (s *StorageImpl) GetCurrentResourceVersion(_ context.Context) (uint64, error) {
	return 0, nil
}

func (s *StorageImpl) ReadinessCheck() error {
	return nil
}

// Versioner Returns Versioner associated with this interface.
func (s *StorageImpl) Versioner() storage.Versioner {
	return s.versioner
}

func extractFields(obj runtime.Object, fields []string) runtime.Object {
	val := reflect.ValueOf(obj).Elem()
	ret := reflect.New(val.Type()).Interface().(runtime.Object)
	for _, name := range fields {
		field := val.FieldByName(name)
		if field.IsValid() {
			reflect.ValueOf(ret).Elem().FieldByName(name).Set(field)
		}
	}
	return ret
}

// makePayloadPath returns a path for the payload file
func makePayloadPath(path string) string {
	return path + GobExt
}

// IsPayloadFile returns true if a given file at `path` is an object payload file, else false
func IsPayloadFile(path string) bool {
	return strings.HasSuffix(path, GobExt)
}

func poolContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), poolTimeout)
}

func (s *StorageImpl) keyFromPath(path string) string {
	extension := filepath.Ext(path)
	return strings.TrimPrefix(strings.TrimSuffix(path, extension), s.root)
}

// saveObject persists obj (payload + SQLite metadata row) and, on success,
// fills metaOut with the full post-mutation obj (resourceVersion bumped,
// managedFields cleared, checksum annotation set) -- metaOut is the REST
// layer's "out" parameter (e.g. the object returned from Create/Update), so
// it must carry the same Spec/etc. that was actually persisted, not just the
// metadata subset written to SQLite.
//
// It also returns a metadata-only copy (ObjectMeta/SchemaVersion, no Spec)
// for callers to use as the lightweight watch-event object: watchers created
// without ResourceVersionFullSpec receive this reduced object instead of the
// full one, to avoid bloating watch traffic with large Spec payloads.
func (s *StorageImpl) saveObject(conn *sqlite.Conn, key string, obj runtime.Object, metaOut runtime.Object, checksum string) (runtime.Object, error) {
	// increment resourceVersion
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil {
		if err := s.versioner.UpdateObject(obj, version+1); err != nil {
			return nil, fmt.Errorf("set resourceVersion: %w", err)
		}
	}
	// remove managed fields
	managedFields := reflect.ValueOf(obj).Elem().FieldByName("ObjectMeta").FieldByName("ManagedFields")
	if managedFields.IsValid() {
		managedFields.Set(reflect.Zero(managedFields.Type()))
	}
	// calculate checksum
	if checksum == "" {
		var err error
		checksum, err = s.CalculateChecksum(obj)
		if err != nil {
			return nil, fmt.Errorf("calculate checksum: %w", err)
		}
	}
	// add checksum to annotations
	if anno := obj.(metav1.Object).GetAnnotations(); anno == nil {
		obj.(metav1.Object).SetAnnotations(map[string]string{helpersv1.SyncChecksumMetadataKey: checksum})
	} else {
		anno[helpersv1.SyncChecksumMetadataKey] = checksum
	}
	// prepare path
	p := filepath.Join(s.root, key)
	if err := s.appFs.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}
	// Atomic write order (issue #44): stage the payload in a temp file, commit
	// the metadata, and only then rename the payload into place. A failing
	// metadata step must leave the served payload untouched — the old code
	// truncated the real payload first, so a lock/interrupt during the metadata
	// insert left GET (new payload) and LIST/RV (old metadata) permanently
	// divergent.
	finalPayloadPath := makePayloadPath(p)
	tmpPayloadPath := finalPayloadPath + ".t"
	payloadFile, err := s.openPayloadFileWithFallback(tmpPayloadPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("open payload file: %w", err)
	}
	directIOWriter := NewDirectIOWriter(payloadFile)

	// write payload
	payloadEncoder := gob.NewEncoder(directIOWriter)
	if err := payloadEncoder.Encode(obj); err != nil {
		_ = directIOWriter.Close()
		_ = payloadFile.Close()
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	if err := directIOWriter.Close(); err != nil {
		_ = payloadFile.Close()
		return nil, fmt.Errorf("close directIOWriter: %w", err)
	}
	if err := payloadFile.Close(); err != nil {
		return nil, fmt.Errorf("close payload file: %w", err)
	}

	// extract metadata (for the SQLite row and as the lightweight watch-event object)
	metadata := extractFields(obj, []string{"ObjectMeta", "SchemaVersion"})
	// Metadata and payload visibility commit together: the metadata write runs
	// in a savepoint that is rolled back if the payload rename fails, so
	// neither side can outlive the other. The staged file is removed on any
	// failure.
	writeMeta := s.writeMetadataFn
	if writeMeta == nil {
		writeMeta = writeMetadata
	}
	renamePayload := s.renamePayloadFn
	if renamePayload == nil {
		renamePayload = s.appFs.Rename
	}
	release := sqlitex.Save(conn)
	err = func() error {
		if werr := writeMeta(conn, key, metadata); werr != nil {
			return fmt.Errorf("write metadata: %w", werr)
		}
		if rerr := renamePayload(tmpPayloadPath, finalPayloadPath); rerr != nil {
			return fmt.Errorf("rename payload into place: %w", rerr)
		}
		return nil
	}()
	release(&err)
	if err != nil {
		_ = s.appFs.Remove(tmpPayloadPath)
		return nil, err
	}
	// eventually fill metaOut with the full persisted object (Spec included) --
	// see the doc comment above for why this must NOT be the metadata-only copy.
	//
	// This is a shallow struct copy: metaOut and obj end up sharing any
	// reference-typed data reachable from Spec (slices, maps, pointers), not
	// just ObjectMeta as before. That's harmless as long as nothing mutates
	// either in place afterward expecting the other to stay untouched -- every
	// current caller either doesn't touch obj again after this point, or only
	// reads it (see saveObject's callers and WatchDispatcher.notify, which
	// fans the same obj/metaOut references out to multiple watchers without
	// copying). If a future caller ever needs to mutate one post-save, it
	// must deep-copy first.
	if metaOut != nil {
		val := reflect.ValueOf(metaOut)
		if val.Kind() == reflect.Ptr {
			// Dereference the pointer
			val = val.Elem()
		}
		val.Set(reflect.ValueOf(obj).Elem())
	}
	return metadata, nil
}

// Create adds a new object at a key unless it already exists. 'ttl' is time-to-live
// in seconds (and is ignored). If no error is returned and out is not nil, out will be
// set to the read value from database.
func (s *StorageImpl) Create(ctx context.Context, key string, obj, metaOut runtime.Object, _ uint64) error {
	poolCtx, cancel := poolContext()
	defer cancel()
	conn, err := s.pool.Take(poolCtx)
	if err != nil {
		return newContentionTimeoutError("create", key, err)
	}
	defer s.pool.Put(conn)
	return s.CreateWithConn(ctx, conn, key, obj, metaOut, 0)
}

func (s *StorageImpl) CreateWithConn(ctx context.Context, conn *sqlite.Conn, key string, obj, metaOut runtime.Object, _ uint64) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Create")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
	defer lockCancel()
	err := s.locks.Lock(lockCtx, key)
	spanLock.End()
	if err != nil {
		return newContentionTimeoutError("create", key, err)
	}
	defer s.locks.Unlock(key)
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("Create", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}
	// check if object already exists
	if _, err := s.appFs.Stat(makePayloadPath(filepath.Join(s.root, key))); err == nil {
		return storage.NewKeyExistsError(key, 0)
	}
	// resourceVersion should not be set on create
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		msg := "resourceVersion should not be set on objects to be created"
		logger.L().Ctx(ctx).Error(msg)
		return errors.New(msg)
	}
	// add conn to context
	ctx = context.WithValue(ctx, connKey, conn)
	// call processor on object to be saved
	if err := s.processor.PreSave(ctx, obj); err != nil {
		if errors.Is(err, ObjectTooLargeError) {
			// clear spec to not bloat the storage
			clearSpec(obj)
			// update annotations with the new state
			metadata := obj.(metav1.Object)
			annotations := metadata.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string)
			}
			annotations[helpersv1.StatusMetadataKey] = helpersv1.TooLarge
			metadata.SetAnnotations(annotations)
			logger.L().Debug("Create - too large object, saving metadata only", helpers.String("key", key))
			// we don't return here as we still need to save the object with updated annotations
		} else {
			return err
		}
	}
	// save object
	metaEvent, err := s.saveObject(conn, key, obj, metaOut, "")
	if err != nil {
		logger.L().Ctx(ctx).Error("Create - save object failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	// call processor on saved object
	if err := s.processor.AfterCreate(ctx, obj); err != nil {
		return fmt.Errorf("processor.AfterCreate: %w", err)
	}
	// publish event to watchers
	s.watchDispatcher.Added(key, metaEvent, obj)
	return nil
}

// Delete removes the specified key and returns the value that existed at that spot.
// If key didn't exist, it will return NotFound storage error.
// If 'cachedExistingObject' is non-nil, it can be used as a suggestion about the
// current version of the object to avoid read operation from storage to get it.
// However, the implementations have to retry in case suggestion is stale.
// Delete implements RC1's lock-then-connection ordering directly (see
// acquireLockedConn): it does not call DeleteWithConn, which keeps its own,
// unchanged connection-then-lock internal acquisition for its direct
// callers (none currently, but its signature/behavior is preserved as
// documented Phase 1 scope).
func (s *StorageImpl) Delete(ctx context.Context, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	conn, err := s.acquireLockedConn(ctx, "delete", key, s.locks.Lock, s.locks.Unlock)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	defer s.locks.Unlock(key)

	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Delete")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return s.delete(ctx, conn, key, metaOut, nil, nil, nil, storage.DeleteOptions{})
}

func (s *StorageImpl) DeleteWithConn(ctx context.Context, conn *sqlite.Conn, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Delete")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
	defer lockCancel()
	err := s.locks.Lock(lockCtx, key)
	spanLock.End()
	if err != nil {
		return newContentionTimeoutError("delete", key, err)
	}
	defer s.locks.Unlock(key)
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("Delete", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}
	return s.delete(ctx, conn, key, metaOut, nil, nil, nil, storage.DeleteOptions{})
}

func (s *StorageImpl) delete(ctx context.Context, conn *sqlite.Conn, key string, metaOut runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	p := filepath.Join(s.root, key)
	// delete metadata in SQLite
	err := DeleteMetadata(conn, key, metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("Delete - delete metadata failed", helpers.Error(err), helpers.String("key", key))
	}
	// delete payload file
	if err := s.appFs.Remove(makePayloadPath(p)); err != nil {
		logger.L().Ctx(ctx).Error("Delete - remove json file failed", helpers.Error(err), helpers.String("key", key))
	}
	// delete time series entries if this is a containerprofile
	_, _, kind, _, _, _ := K8sPathToKeys(key)
	if IsContainerProfileKind(kind) {
		if err := DeleteTimeSeriesContainerEntries(conn, key); err != nil {
			logger.L().Ctx(ctx).Error("Delete - delete time series entries failed", helpers.Error(err), helpers.String("key", key))
			return fmt.Errorf("delete time series entries: %w", err)
		}
	}
	// publish event to watchers
	s.watchDispatcher.Deleted(key, metaOut)
	return nil
}

// Watch begins watching the specified key. Events are decoded into API objects,
// and any items selected by 'p' are sent down to returned watch.Interface.
// resourceVersion may be used to specify what version to begin watching,
// which should be the current resourceVersion, and no longer rv+1
// (e.g. reconnecting without missing any updates).
// If resource version is "0", this interface will get current object at given key
// and send it in an "ADDED" event, before watch starts.
func (s *StorageImpl) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	_, span := otel.Tracer("").Start(ctx, "StorageImpl.Watch")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	// Namespace-scoped watches used to be rejected here (returning an idle,
	// event-free watch instead) to work around namespaces getting stuck in
	// Terminating -- but WatchDispatcher.Register/notify (watch.go) match
	// purely on path-prefix strings and have never treated a namespaced key
	// any differently from a cluster-scoped one, so a namespaced watch is
	// dispatched through the exact same code already proven correct for
	// cluster-scoped resources. See docs/features/namespace-watch-enabled.md.
	// TODO(ttimonen) Should we do ctx.WithoutCancel; or does the parent ctx lifetime match with expectations?
	nw := newWatcher(ctx, opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec)
	s.watchDispatcher.Register(key, nw)
	return nw, nil
}

// Get unmarshals object found at key into objPtr. On a not found error, will either
// return a zero object of the requested type, or an error, depending on 'opts.ignoreNotFound'.
// Treats empty responses and nil response nodes exactly like a not found error.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
// Get implements RC1's lock-then-connection ordering directly (see
// acquireLockedConn): it does not call GetWithConn, which keeps its own,
// unchanged connection-then-lock internal acquisition for its direct
// callers (the consolidation path in containerprofile_storage.go, which
// already holds a connection before calling it -- see that fix's
// documented exception).
func (s *StorageImpl) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	conn, err := s.acquireLockedConn(ctx, "get", key, s.locks.RLock, s.locks.RUnlock)
	if err != nil {
		return err
	}
	defer s.pool.Put(conn)
	defer s.locks.RUnlock(key)

	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	return s.get(ctx, conn, key, opts, objPtr, hasReadLock)
}

func (s *StorageImpl) GetWithConn(ctx context.Context, conn *sqlite.Conn, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
	defer lockCancel()
	err := s.locks.RLock(lockCtx, key)
	spanLock.End()
	if err != nil {
		return newContentionTimeoutError("get", key, err)
	}
	defer s.locks.RUnlock(key)
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("Get", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}
	return s.get(ctx, conn, key, opts, objPtr, hasReadLock)
}

// getLockState describes what lock the caller holds when invoking get(), so the
// migration path can manage locks correctly without crashing or deadlocking.
type getLockState int

const (
	noLock       getLockState = iota // caller holds no lock; migration acquires a temporary write lock
	hasReadLock                      // caller holds a read lock; migration upgrades to write lock then restores read lock
	hasWriteLock                     // caller already holds the write lock; migration runs without lock changes
)

// get is a helper function for Get to allow calls without locks from other methods that already have them
func (s *StorageImpl) get(ctx context.Context, conn *sqlite.Conn, key string, opts storage.GetOptions, objPtr runtime.Object, lockState getLockState) error {
	p := filepath.Join(s.root, key)
	if opts.ResourceVersion == softwarecomposition.ResourceVersionMetadata {
		// get metadata from SQLite
		metadata, err := ReadMetadata(conn, key)
		if err != nil {
			if errors.Is(err, ErrMetadataNotFound) {
				if opts.IgnoreNotFound {
					return runtime.SetZeroValue(objPtr)
				} else {
					return storage.NewKeyNotFoundError(key, 0)
				}
			} else {
				return fmt.Errorf("read metadata: %w", err)
			}
		}
		return json.Unmarshal(metadata, objPtr)
	}

	// noLock callers perform unsynchronized file I/O by default.  Acquire a
	// temporary read lock so that a concurrent saveObject cannot truncate or
	// overwrite the file while we are decoding it.  The lock is released via
	// the deferred cleanup below; the migration path clears ownedRLock before
	// calling RUnlock itself so the defer becomes a no-op.
	ownedRLock := false
	if lockState == noLock {
		lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
		defer lockCancel()
		if err := s.locks.RLock(lockCtx, key); err != nil {
			return newContentionTimeoutError("get", key, err)
		}
		ownedRLock = true
		defer func() {
			if ownedRLock {
				s.locks.RUnlock(key)
			}
		}()
	}

	payloadFile, err := s.openPayloadFileWithFallback(makePayloadPath(p), os.O_RDONLY, 0)
	if err != nil {
		if errors.Is(err, afero.ErrFileNotFound) {
			// file not found, delete corresponding metadata
			_ = DeleteMetadata(conn, key, nil)
			if opts.IgnoreNotFound {
				return runtime.SetZeroValue(objPtr)
			} else {
				return storage.NewKeyNotFoundError(key, 0)
			}
		}
		logger.L().Ctx(ctx).Error("Get - read file failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	defer func() {
		_ = payloadFile.Close()
	}()

	// Try normal decode first
	decoder := gob.NewDecoder(NewDirectIOReader(payloadFile))
	err = decoder.Decode(objPtr)
	if err == nil {
		return nil
	}

	// If it fails with type error, or any other gob error, try external migration tool
	if strings.Contains(err.Error(), "gob: wrong type") || strings.Contains(err.Error(), "extra fields") {
		logger.L().Ctx(ctx).Info("Get - detected gob type mismatch, attempting external migration", helpers.Error(err), helpers.String("key", key))

		// Acquire a write lock before running the migration tool to prevent
		// concurrent migration attempts.  All lock transitions are explicit
		// (no deferred lock calls) so that errors can be checked and the lock
		// state is always well-defined on every return path.
		var migErr error
		switch lockState {
		case hasReadLock:
			// Drop the caller's read lock and upgrade to write lock.
			// After migration, restore the read lock so the caller's deferred
			// RUnlock (in GetWithConn) has a matching acquire.
			s.locks.RUnlock(key)
			if lockErr := s.locks.Lock(ctx, key); lockErr != nil {
				logger.L().Ctx(ctx).Error("Get - failed to acquire write lock for migration", helpers.Error(lockErr), helpers.String("key", key))
				// Best-effort: restore the read lock so the caller's deferred
				// RUnlock is not left unmatched.
				if rlockErr := s.locks.RLock(ctx, key); rlockErr != nil {
					logger.L().Ctx(ctx).Error("Get - failed to restore read lock after write lock failure", helpers.Error(rlockErr), helpers.String("key", key))
				}
				return fmt.Errorf("failed to acquire write lock for migration: %w", lockErr)
			}
			migErr = s.migrateObjectUnlocked(ctx, conn, p, key, opts, objPtr)
			s.locks.Unlock(key)
			if rlockErr := s.locks.RLock(ctx, key); rlockErr != nil {
				logger.L().Ctx(ctx).Error("Get - failed to restore read lock after migration", helpers.Error(rlockErr), helpers.String("key", key))
				return fmt.Errorf("failed to restore read lock after migration: %w", rlockErr)
			}

		case noLock:
			// We hold a temporary read lock (ownedRLock=true); release it and
			// upgrade to write lock.  Do not re-acquire afterwards — there is
			// no outer caller expecting a read lock to be held.
			ownedRLock = false
			s.locks.RUnlock(key)
			if lockErr := s.locks.Lock(ctx, key); lockErr != nil {
				logger.L().Ctx(ctx).Error("Get - failed to acquire write lock for migration", helpers.Error(lockErr), helpers.String("key", key))
				return fmt.Errorf("failed to acquire write lock for migration: %w", lockErr)
			}
			migErr = s.migrateObjectUnlocked(ctx, conn, p, key, opts, objPtr)
			s.locks.Unlock(key)

		case hasWriteLock:
			// Already holding the write lock — just migrate.
			migErr = s.migrateObject(ctx, conn, p, key, opts, objPtr)
		}
		return migErr
	}

	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		// irrecoverable error, delete corresponding data
		_ = DeleteMetadata(conn, key, nil)
		_ = s.appFs.Remove(makePayloadPath(p))
		logger.L().Ctx(ctx).Error("Get - gob error, treating as corrupted and removing files", helpers.Error(err), helpers.String("key", key))
		if opts.IgnoreNotFound {
			return runtime.SetZeroValue(objPtr)
		} else {
			return storage.NewKeyNotFoundError(key, 0)
		}
	}
	logger.L().Ctx(ctx).Error("Get - gob unmarshal failed", helpers.Error(err), helpers.String("key", key))
	return err
}

// migrateObject runs the external migration tool and unmarshals the output into objPtr.
// It is used by get() to migrate objects that need external migration.
// The caller must hold the write lock for key before calling this.
func (s *StorageImpl) migrateObject(ctx context.Context, conn *sqlite.Conn, path, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	// Re-check if the file still needs migration — another goroutine may have
	// already migrated it while we were waiting for the write lock.
	payloadFileRetry, err := s.openPayloadFileWithFallback(makePayloadPath(path), os.O_RDONLY, 0)
	if err == nil {
		decoderRetry := gob.NewDecoder(NewDirectIOReader(payloadFileRetry))
		errRetry := decoderRetry.Decode(objPtr)
		_ = payloadFileRetry.Close()
		if errRetry == nil {
			logger.L().Ctx(ctx).Info("Get - migration already completed by another thread", helpers.String("key", key))
			return nil
		}
	}

	typeName := "ContainerProfile"
	if _, ok := objPtr.(*softwarecomposition.SeccompProfile); ok {
		typeName = "SeccompProfile"
	}

	migrationCtx, migrationCancel := context.WithTimeout(ctx, 30*time.Second)
	defer migrationCancel()

	cmd := exec.CommandContext(migrationCtx, "/usr/bin/migration", "-file", makePayloadPath(path), "-type", typeName)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		if errors.Is(migrationCtx.Err(), context.DeadlineExceeded) {
			logger.L().Ctx(ctx).Error("Get - migration tool timed out", helpers.String("key", key))
			return fmt.Errorf("migration tool timed out: %w", migrationCtx.Err())
		}
		logger.L().Ctx(ctx).Error("Get - migration tool failed", helpers.Error(runErr), helpers.String("stderr", stderr.String()), helpers.String("key", key))
		// If migration tool fails, treat as corrupted and delete
		_ = DeleteMetadata(conn, key, nil)
		_ = s.appFs.Remove(makePayloadPath(path))
		if opts.IgnoreNotFound {
			return runtime.SetZeroValue(objPtr)
		} else {
			return storage.NewKeyNotFoundError(key, 0)
		}
	}

	// Migration tool outputted JSON, unmarshal it into objPtr
	if unmarshalErr := json.Unmarshal(out.Bytes(), objPtr); unmarshalErr != nil {
		logger.L().Ctx(ctx).Error("Get - unmarshal migrated JSON failed", helpers.Error(unmarshalErr), helpers.String("key", key))
		return unmarshalErr
	}

	logger.L().Ctx(ctx).Info("Get - external migration successful", helpers.String("key", key))

	if _, saveErr := s.saveObject(conn, key, objPtr, nil, ""); saveErr != nil {
		logger.L().Ctx(ctx).Error("Get - failed to rewrite migrated object", helpers.Error(saveErr), helpers.String("key", key))
	} else {
		logger.L().Ctx(ctx).Info("Get - successfully migrated object to modern format", helpers.String("key", key))
	}

	return nil
}

// tryDecodePayload attempts to gob-decode the payload file at path into
// objPtr. It returns (true, nil) on a successful decode, (false, nil) if the
// file opened but is still undecodable, or (false, err) if the file could
// not even be opened -- err wraps afero.ErrFileNotFound when the file is
// simply gone (checkable via errors.Is), distinguishing "still needs
// migration" from "a concurrent Delete already removed it".
func (s *StorageImpl) tryDecodePayload(path string, objPtr runtime.Object) (bool, error) {
	f, err := s.openPayloadFileWithFallback(makePayloadPath(path), os.O_RDONLY, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	decoder := gob.NewDecoder(NewDirectIOReader(f))
	if decodeErr := decoder.Decode(objPtr); decodeErr != nil {
		return false, nil
	}
	return true, nil
}

// migrationBinaryPath is the external migration tool invoked by
// execMigrationTool (the hasReadLock/noLock, unlocked-exec path only --
// migrateObject's own, unchanged exec call for the hasWriteLock path keeps
// its hardcoded path). Package-level var, not const, so tests can point it
// at a fixture script instead of the real /usr/bin/migration binary.
var migrationBinaryPath = "/usr/bin/migration"

// execMigrationTool runs the external migration binary against path and
// returns its JSON output on success. A timeout is distinguished from any
// other tool failure via errors.Is(err, context.DeadlineExceeded) on the
// returned error, matching migrateObject's original error-handling shape.
func execMigrationTool(ctx context.Context, path, typeName string) ([]byte, error) {
	migrationCtx, migrationCancel := context.WithTimeout(ctx, 30*time.Second)
	defer migrationCancel()

	cmd := exec.CommandContext(migrationCtx, migrationBinaryPath, "-file", makePayloadPath(path), "-type", typeName)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		if errors.Is(migrationCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("migration tool timed out: %w", migrationCtx.Err())
		}
		return nil, fmt.Errorf("migration tool failed (stderr=%q): %w", stderr.String(), runErr)
	}
	return out.Bytes(), nil
}

// migrateObjectUnlocked implements RC3's fix for get()'s hasReadLock/noLock
// caller states (see .omc/plans/storage-locking-rewrite.md, Phase 1 step 3):
// it releases the write lock around the migration tool's up-to-30s exec,
// then re-acquires it and performs a three-way re-verify before committing,
// rather than holding the write lock for the whole exec as migrateObject
// does. This must NOT be used for the hasWriteLock caller state (reached
// from inside GuaranteedUpdate's read-modify-write transaction): dropping a
// lock that call didn't itself acquire would break that transaction's
// exclusivity, since StorageImpl has no resourceVersion conflict check to
// catch a concurrent write landing in the gap -- migrateObject stays
// unchanged and fully locked for that path.
//
// The three-way re-verify (N11) distinguishes: (a) the payload now decodes
// successfully -- a concurrent writer already handled it; objPtr holds that
// current, valid object, and neither the save nor delete branch fires; (b)
// the payload open fails specifically because the file no longer exists --
// a concurrent Delete won, so this aborts without resurrecting it (neither
// branch fires); (c) the file is present and still genuinely undecodable --
// commit the precomputed exec outcome (save on success, delete on
// migration-tool failure, plain error on timeout), exactly as migrateObject
// does today.
//
// The caller must hold the write lock for key before calling this. It is
// guaranteed to still hold the write lock when this returns, by any path --
// the internal unlock/exec/relock is transparent to the caller. Re-acquiring
// the lock after the exec uses a context detached from ctx's cancellation
// (context.WithoutCancel) so a caller-context cancellation during the
// (bounded, 30s) exec cannot leave this function returning without the lock
// its caller's own unlock logic expects -- this mirrors an already-accepted
// residual elsewhere in this file (get()'s own migration-upgrade lock
// acquisitions use the caller's context with no lockTimeout child either).
func (s *StorageImpl) migrateObjectUnlocked(ctx context.Context, conn *sqlite.Conn, path, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	// Fast path, still locked: another goroutine may have already migrated
	// the object while we were waiting for the write lock.
	if decoded, decodeErr := s.tryDecodePayload(path, objPtr); decodeErr == nil && decoded {
		logger.L().Ctx(ctx).Info("Get - migration already completed by another thread", helpers.String("key", key))
		return nil
	}

	typeName := "ContainerProfile"
	if _, ok := objPtr.(*softwarecomposition.SeccompProfile); ok {
		typeName = "SeccompProfile"
	}

	s.locks.Unlock(key)
	migratedJSON, execErr := execMigrationTool(ctx, path, typeName)
	// Re-acquire no matter what: this function is guaranteed to still hold
	// the write lock on return (see the doc comment above), and callers
	// unlock unconditionally right after it returns without checking for an
	// error -- MapMutex.Unlock doesn't verify ownership, so returning here
	// without the lock would have the caller release a lock it never
	// reacquired, corrupting lock state for this key (or, if another
	// goroutine has since legitimately acquired it, releasing *their* lock).
	// relockCtx (context.WithoutCancel) already implies "keep trying rather
	// than give up": today Lock genuinely cannot fail for it (its only error
	// paths require either a nil ctx, which this never is, or the ctx's own
	// cancellation, which a detached context never observes), but loop
	// instead of relying on that, so the guarantee holds even if Lock's
	// error semantics change later.
	relockCtx := context.WithoutCancel(ctx)
	for {
		if lockErr := s.locks.Lock(relockCtx, key); lockErr == nil {
			break
		} else {
			logger.L().Ctx(ctx).Error("Get - failed to re-acquire write lock after migration exec, retrying", helpers.Error(lockErr), helpers.String("key", key))
		}
	}

	reDecoded, reErr := s.tryDecodePayload(path, objPtr)
	switch {
	case reErr == nil && reDecoded:
		// (a) a concurrent writer already fixed it; objPtr now holds the
		// current, valid object.
		return nil
	case errors.Is(reErr, afero.ErrFileNotFound):
		// (b) a concurrent Delete won while we were unlocked; abort without
		// resurrecting it.
		if opts.IgnoreNotFound {
			return runtime.SetZeroValue(objPtr)
		}
		return storage.NewKeyNotFoundError(key, 0)
	default:
		// (c) still genuinely undecodable; commit the precomputed outcome.
		if execErr != nil {
			if errors.Is(execErr, context.DeadlineExceeded) {
				logger.L().Ctx(ctx).Error("Get - migration tool timed out", helpers.String("key", key))
				return execErr
			}
			logger.L().Ctx(ctx).Error("Get - migration tool failed", helpers.Error(execErr), helpers.String("key", key))
			_ = DeleteMetadata(conn, key, nil)
			_ = s.appFs.Remove(makePayloadPath(path))
			if opts.IgnoreNotFound {
				return runtime.SetZeroValue(objPtr)
			}
			return storage.NewKeyNotFoundError(key, 0)
		}
		if unmarshalErr := json.Unmarshal(migratedJSON, objPtr); unmarshalErr != nil {
			logger.L().Ctx(ctx).Error("Get - unmarshal migrated JSON failed", helpers.Error(unmarshalErr), helpers.String("key", key))
			return unmarshalErr
		}
		logger.L().Ctx(ctx).Info("Get - external migration successful", helpers.String("key", key))
		if _, saveErr := s.saveObject(conn, key, objPtr, nil, ""); saveErr != nil {
			logger.L().Ctx(ctx).Error("Get - failed to rewrite migrated object", helpers.Error(saveErr), helpers.String("key", key))
		} else {
			logger.L().Ctx(ctx).Info("Get - successfully migrated object to modern format", helpers.String("key", key))
		}
		return nil
	}
}

// GetList unmarshalls objects found at key into a *List api object (an object
// that satisfies runtime.IsList definition).
// If 'opts.Recursive' is false, 'key' is used as an exact match. If `opts.Recursive`
// is true, 'key' is used as a prefix.
// The returned contents may be delayed, but it is guaranteed that they will
// match 'opts.ResourceVersion' according 'opts.ResourceVersionMatch'.
// GetList only returns metadata for the objects, not the objects themselves.
//
// GetList acquires and releases a pool connection separately for each internal
// SQLite-level page of results, rather than holding one connection for the whole
// call: the ResourceVersionFullSpec branch takes a per-key read lock per object,
// and a connection held idle while stalled on one of those locks would otherwise
// stay pinned through every subsequent page too. Callers that already hold their
// own connection and want the whole list in one call should use GetListWithConn
// instead, which keeps that single connection for its entire duration.
func (s *StorageImpl) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	ctx, predicate, v, elem, limit, cursor, isFullSpec, err := s.prepareGetList(ctx, key, opts, listObj)
	if err != nil {
		return err
	}

	pageLast := ""
	for int64(v.Len()) < limit {
		remaining := limit - int64(v.Len())

		poolCtx, cancel := poolContext()
		conn, err := s.pool.Take(poolCtx)
		if err != nil {
			cancel()
			return newContentionTimeoutError("list", key, err)
		}
		fetched, err := s.fetchListPage(ctx, conn, key, cursor, remaining, isFullSpec, predicate, v, elem)
		s.pool.Put(conn)
		cancel()
		if err != nil {
			return err
		}
		pageLast = fetched.pageLast

		if int64(fetched.count) < remaining {
			pageLast = ""
			break
		}
		cursor = pageLast
	}

	return setListContinue(listObj, pageLast)
}

// GetListWithConn is the same as GetList, but reuses a single, caller-supplied
// connection for the entire (possibly multi-page) call, instead of acquiring and
// releasing a connection separately per internal page. Use this only when the
// caller already holds its own connection for other reasons; it pins that
// connection across any per-key lock waits that occur along the way.
func (s *StorageImpl) GetListWithConn(ctx context.Context, conn *sqlite.Conn, key string, opts storage.ListOptions, listObj runtime.Object) error {
	ctx, predicate, v, elem, limit, cursor, isFullSpec, err := s.prepareGetList(ctx, key, opts, listObj)
	if err != nil {
		return err
	}

	pageLast := ""
	for int64(v.Len()) < limit {
		remaining := limit - int64(v.Len())

		fetched, err := s.fetchListPage(ctx, conn, key, cursor, remaining, isFullSpec, predicate, v, elem)
		if err != nil {
			return err
		}
		pageLast = fetched.pageLast

		if int64(fetched.count) < remaining {
			pageLast = ""
			break
		}
		cursor = pageLast
	}

	return setListContinue(listObj, pageLast)
}

// prepareGetList performs the (connection-independent) setup shared by GetList
// and GetListWithConn: predicate normalization and pulling the destination slice,
// element type, page limit, starting cursor and full-spec flag out of listObj/opts.
// It returns the normalized predicate for the caller to reuse across pages -- opts
// is passed by value, so mutating opts.Predicate here does not propagate back to
// the caller's own copy.
func (s *StorageImpl) prepareGetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) (_ context.Context, predicate storage.SelectionPredicate, v reflect.Value, elem reflect.Type, limit int64, cursor string, isFullSpec bool, err error) {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	predicate, err = normalizeSelectionPredicate(opts.Predicate)
	if err != nil {
		logger.L().Ctx(ctx).Error("GetList - normalize selection predicate failed", helpers.Error(err))
		return ctx, predicate, v, elem, 0, "", false, err
	}
	opts.Predicate = predicate

	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("GetList - get items ptr failed", helpers.Error(err), helpers.String("key", key))
		return ctx, predicate, v, elem, 0, "", false, err
	}
	v, err = conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("GetList - need ptr to slice", helpers.Error(err), helpers.String("key", key))
		return ctx, predicate, v, elem, 0, "", false, fmt.Errorf("need ptr to slice: %v", err)
	}
	// set default limit
	limit = opts.Predicate.Limit
	if limit == 0 {
		limit = 500
	}
	// populate list object
	elem = v.Type().Elem()
	v.Set(reflect.MakeSlice(v.Type(), 0, 0))
	isFullSpec = opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec

	return ctx, predicate, v, elem, limit, opts.Predicate.Continue, isFullSpec, nil
}

// listPageResult is the outcome of fetching one internal SQLite-level page.
type listPageResult struct {
	pageLast string
	count    int // raw entries fetched from SQLite, before predicate filtering
}

// fetchListPage fetches one internal page (up to `remaining` entries from SQLite
// starting at cursor) and appends predicate-matching objects to v. The returned
// count is how many raw entries were fetched (as opposed to how many matched and
// were appended) -- the caller uses it to decide whether more entries might still
// exist upstream.
func (s *StorageImpl) fetchListPage(ctx context.Context, conn *sqlite.Conn, key string, cursor string, remaining int64, isFullSpec bool, predicate storage.SelectionPredicate, v reflect.Value, elem reflect.Type) (listPageResult, error) {
	var entries []string
	var pageLast string
	var err error
	if isFullSpec {
		// get names from SQLite
		entries, pageLast, err = listMetadataKeys(conn, key, cursor, remaining)
	} else {
		// get metadata from SQLite
		entries, pageLast, err = listMetadata(conn, key, cursor, remaining)
	}
	if err != nil {
		return listPageResult{}, fmt.Errorf("list objects for %q: %w", key, err)
	}

	v.Grow(len(entries))

	for _, entry := range entries {
		obj := reflect.New(elem).Interface().(runtime.Object)
		if isFullSpec {
			if err := s.get(ctx, conn, entry, storage.GetOptions{}, obj, noLock); err != nil {
				logger.L().Ctx(ctx).Error("GetList - get object failed", helpers.Error(err), helpers.String("key", entry))
				continue
			}
		} else {
			if err := json.Unmarshal([]byte(entry), obj); err != nil {
				logger.L().Ctx(ctx).Error("GetList - unmarshal metadata failed", helpers.Error(err), helpers.String("key", key))
				continue
			}
		}

		matched, err := predicate.Matches(obj)
		if err != nil {
			return listPageResult{}, fmt.Errorf("match selection predicate: %w", err)
		}
		if matched {
			v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
		}
	}

	return listPageResult{pageLast: pageLast, count: len(entries)}, nil
}

// setListContinue sets listObj's continue token, mirroring what GetList/
// GetListWithConn used to do inline at the end of their pagination loop.
func setListContinue(listObj runtime.Object, pageLast string) error {
	if pageLast == "" {
		return nil
	}
	listAccessor, err := meta.ListAccessor(listObj)
	if err != nil {
		return fmt.Errorf("list accessor: %w", err)
	}
	listAccessor.SetContinue(pageLast)
	return nil
}

// getListWithSpec is the same as GetList, but it returns the full objects instead of just the metadata.
func (s *StorageImpl) getListWithSpec(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.getListWithSpec")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	listPtr, err := meta.GetItemsPtr(listObj)
	if err != nil {
		logger.L().Ctx(ctx).Error("getListWithSpec - get items ptr failed", helpers.Error(err), helpers.String("key", key))
		return err
	}
	v, err := conversion.EnforcePtr(listPtr)
	if err != nil || v.Kind() != reflect.Slice {
		logger.L().Ctx(ctx).Error("getListWithSpec - need ptr to slice", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("need ptr to slice: %v", err)
	}

	p := filepath.Join(s.root, key)
	var payloadFiles []string

	payloadPath := makePayloadPath(p)
	if exists, _ := afero.Exists(s.appFs, payloadPath); exists {
		// key refers to one object
		payloadFiles = append(payloadFiles, payloadPath)
	} else {
		_ = afero.Walk(s.appFs, p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && IsPayloadFile(path) {
				payloadFiles = append(payloadFiles, path)
			}
			return nil
		})
	}
	for _, payloadFile := range payloadFiles {
		if err := s.appendGobObjectFromFile(ctx, payloadFile, v); err != nil {
			logger.L().Ctx(ctx).Error("getListWithSpec - appending Gob object from file failed", helpers.Error(err), helpers.String("path", payloadFile))
		}
	}
	return nil
}

func (s *StorageImpl) getStateFromObject(ctx context.Context, obj runtime.Object) (*objState, error) {
	state := &objState{
		obj:  obj,
		meta: &storage.ResponseMeta{},
	}

	rv, err := s.versioner.ObjectResourceVersion(obj)
	if err != nil {
		logger.L().Ctx(ctx).Error("getStateFromObject - get object resource version failed", helpers.Error(err), helpers.Interface("object", obj))
		return nil, fmt.Errorf("couldn't get resource version: %v", err)
	}
	state.rev = int64(rv)
	state.meta.ResourceVersion = uint64(state.rev)

	state.data, err = json.Marshal(obj)
	if err != nil {
		logger.L().Ctx(ctx).Error("getStateFromObject - marshal object failed", helpers.Error(err), helpers.Interface("object", obj))
		return nil, err
	}
	if err := s.versioner.UpdateObject(state.obj, rv); err != nil {
		logger.L().Ctx(ctx).Error("getStateFromObject - update object version failed", helpers.Error(err), helpers.Interface("object", obj))
	}
	return state, nil
}

// GuaranteedUpdate keeps calling 'tryUpdate()' to update key 'key' (of type 'destination')
// retrying the update until success if there is index conflict.
// Note that object passed to tryUpdate may change across invocations of tryUpdate() if
// other writers are simultaneously updating it, so tryUpdate() needs to take into account
// the current contents of the object when deciding how the update object should look.
// If the key doesn't exist, it will return NotFound storage error if ignoreNotFound=false
// else `destination` will be set to the zero value of its type.
// If the eventual successful invocation of `tryUpdate` returns an output with the same serialized
// contents as the input, it won't perform any update, but instead set `destination` to an object with those
// contents.
// If 'cachedExistingObject' is non-nil, it can be used as a suggestion about the
// current version of the object to avoid read operation from storage to get it.
// However, the implementations have to retry in case suggestion is stale.
//
// Example:
//
// s := /* implementation of Interface */
// err := s.GuaranteedUpdate(
//
//	 "myKey", &MyType{}, true, preconditions,
//	 func(input runtime.Object, res ResponseMeta) (runtime.Object, *uint64, error) {
//	   // Before each invocation of the user defined function, "input" is reset to
//	   // current contents for "myKey" in database.
//	   curr := input.(*MyType)  // Guaranteed to succeed.
//
//	   // Make the modification
//	   curr.Counter++
//
//	   // Return the modified object - return an error to stop iterating. Return
//	   // a uint64 to alter the TTL on the object, or nil to keep it the same value.
//	   return cur, nil, nil
//	}, cachedExistingObject
//
// )
func (s *StorageImpl) GuaranteedUpdate(
	ctx context.Context, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	poolCtx, cancel := poolContext()
	defer cancel()
	conn, err := s.pool.Take(poolCtx)
	if err != nil {
		return newContentionTimeoutError("update", key, err)
	}
	defer s.pool.Put(conn)
	return s.GuaranteedUpdateWithConn(ctx, conn, key, metaOut, ignoreNotFound, preconditions, tryUpdate, cachedExistingObject, "")
}

func (s *StorageImpl) GuaranteedUpdateWithConn(
	ctx context.Context, conn *sqlite.Conn, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object, checksum string) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GuaranteedUpdate")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()
	// add conn to context now (rather than only once tryUpdate has succeeded,
	// as before): the "before" snapshot's PreSave call (below, taken ahead of
	// tryUpdate) needs the same conn-bearing context that the "after" (ret)
	// PreSave call gets, or a processor that reads the connection out of ctx
	// (e.g. ContainerProfileProcessor.PreSave) panics on the orig snapshot.
	ctx = context.WithValue(ctx, connKey, conn)
	_, spanLock := otel.Tracer("").Start(ctx, "waiting for lock")
	beforeLock := time.Now()
	lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
	defer lockCancel()
	err := s.locks.Lock(lockCtx, key)
	spanLock.End()
	if err != nil {
		logger.L().Debug("GuaranteedUpdate - lock failed", helpers.Error(err), helpers.String("key", key))
		return newContentionTimeoutError("update", key, err)
	}
	defer s.locks.Unlock(key)
	lockDuration := time.Since(beforeLock)
	if lockDuration > time.Second {
		logger.L().Debug("GuaranteedUpdate/", helpers.String("key", key), helpers.String("lockDuration", lockDuration.String()))
	}

	// key preparation is skipped
	// otel span tracking is skipped

	v, err := conversion.EnforcePtr(metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("GuaranteedUpdate - unable to convert output object to pointer", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}

	getCurrentState := func() (*objState, error) {
		objPtr := reflect.New(v.Type()).Interface().(runtime.Object)
		err := s.get(ctx, conn, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, objPtr, hasWriteLock)
		if err != nil {
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - get failed", helpers.Error(err), helpers.String("key", key))
			return nil, err
		}
		return s.getStateFromObject(ctx, objPtr)
	}

	var origState *objState
	var origStateIsCurrent bool
	if cachedExistingObject != nil {
		origState, err = s.getStateFromObject(ctx, cachedExistingObject)
	} else {
		origState, err = getCurrentState()
		origStateIsCurrent = true
	}
	if err != nil {
		logger.L().Ctx(ctx).Error("GuaranteedUpdate - get original state failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	// check object size
	annotations := origState.obj.(metav1.Object).GetAnnotations()
	if annotations != nil && annotations[helpersv1.StatusMetadataKey] == helpersv1.TooLarge {
		logger.L().Debug("GuaranteedUpdate - already too large object, skipping update", helpers.String("key", key))
		// no change, return the original object
		v.Set(reflect.ValueOf(origState.obj).Elem())
		return nil
	}

	for {
		// run preconditions
		if err := preconditions.Check(key, origState.obj); err != nil {
			// If our data is already up-to-date, return the error
			if origStateIsCurrent {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - preconditions check failed", helpers.Error(err), helpers.String("key", key))
				return err
			}

			// It's possible we were working with stale data
			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true
			// Retry
			continue
		}

		// Snapshot the "before" state now, before tryUpdate runs. tryUpdate may
		// mutate origState.obj in place and return that same reference (this is
		// exactly what genericregistry.Store's own finalizer-delete tryUpdate
		// does, via markAsDeleting) -- capturing this snapshot after tryUpdate
		// ran would observe the already-mutated state, making the no-op-update
		// check below spuriously always-equal and silently dropping the write.
		orig := origState.obj.DeepCopyObject() // FIXME this is expensive
		_ = s.processor.PreSave(ctx, orig)

		// run tryUpdate
		ret, _, err := tryUpdate(origState.obj, storage.ResponseMeta{})
		if err != nil {
			// If our data is already up-to-date, return the error
			if origStateIsCurrent {
				if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				}
				logger.L().Debug("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return err
			}

			// It's possible we were working with stale data
			// Remember the revision of the potentially stale data and the resulting update error
			cachedRev := origState.rev
			cachedUpdateErr := err

			// Actually fetch
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true

			// it turns out our cached data was not stale, return the error
			if cachedRev == origState.rev {
				if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				}
				logger.L().Debug("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return cachedUpdateErr
			}

			// Retry
			continue
		}

		// call processor on object to be saved
		if err := s.processor.PreSave(ctx, ret); err != nil {
			if errors.Is(err, ObjectTooLargeError) {
				// clear spec to not bloat the storage
				clearSpec(ret)
				// update annotations with the new state
				metadata := ret.(metav1.Object)
				annotations := metadata.GetAnnotations()
				if annotations == nil {
					annotations = make(map[string]string)
				}
				annotations[helpersv1.StatusMetadataKey] = helpersv1.TooLarge
				metadata.SetAnnotations(annotations)
				logger.L().Debug("GuaranteedUpdate - too large object, skipping update", helpers.String("key", key))
				// we don't return here as we still need to save the object with updated annotations
			} else {
				logger.L().Debug("GuaranteedUpdate - processor.PreSave failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
		}

		// check if the object is the same as the original
		if reflect.DeepEqual(orig, ret) {
			logger.L().Debug("GuaranteedUpdate - tryUpdate returned the same object, no update needed", helpers.String("key", key))
			// no change, return the original object
			v.Set(reflect.ValueOf(origState.obj).Elem())
			return nil
		}

		// save to disk and fill into metaOut
		metaEvent, err := s.saveObject(conn, key, ret, metaOut, checksum)
		if err != nil {
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - save object failed", helpers.Error(err), helpers.String("key", key))
			return err
		}
		// Only successful updates should produce modification events
		s.watchDispatcher.Modified(key, metaEvent, ret)
		return nil
	}
}

// Count returns number of different entries under the key (generally being path prefix).
func (s *StorageImpl) Count(key string) (int64, error) {
	logger.L().Debug("Custom storage count", helpers.String("key", key))
	poolCtx, cancel := poolContext()
	defer cancel()
	conn, err := s.pool.Take(poolCtx)
	if err != nil {
		return 0, newContentionTimeoutError("count", key, err)
	}
	defer s.pool.Put(conn)
	return countMetadata(conn, key)
}

// RequestWatchProgress fulfills the storage.Interface
//
// Its function is only relevant to etcd.
func (s *StorageImpl) RequestWatchProgress(context.Context) error {
	return nil
}

// GetByNamespace returns all objects in a given namespace, given their api version and kind.
func (s *StorageImpl) GetByNamespace(ctx context.Context, apiVersion, kind, namespace string, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetByNamespace")
	span.SetAttributes(attribute.String("apiVersion", apiVersion), attribute.String("kind", kind), attribute.String("namespace", namespace))
	defer span.End()

	p := filepath.Join(apiVersion, kind, namespace)

	return s.getListWithSpec(ctx, p, storage.ListOptions{}, listObj)
}

// GetByCluster returns all objects in a given cluster, given their api version and kind.
func (s *StorageImpl) GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "StorageImpl.GetClusterScopedResource")
	defer span.End()

	p := filepath.Join(apiVersion, kind)

	return s.getListWithSpec(ctx, p, storage.ListOptions{}, listObj)
}

// appendGobObjectFromFile unmarshalls a Gob file into a runtime.Object and appends it to the underlying list object.
func (s *StorageImpl) appendGobObjectFromFile(ctx context.Context, path string, v reflect.Value) error {
	key := s.keyFromPath(path)
	lockCtx, lockCancel := context.WithTimeout(ctx, lockTimeout)
	defer lockCancel()
	err := s.locks.RLock(lockCtx, key)
	if err != nil {
		return newContentionTimeoutError("list", key, err)
	}
	defer s.locks.RUnlock(key)
	payloadFile, err := s.openPayloadFileWithFallback(path, os.O_RDONLY, 0)
	if err != nil {
		// skip if file is not readable, maybe it was deleted
		return nil
	}
	defer func() {
		_ = payloadFile.Close()
	}()

	obj := reflect.New(v.Type().Elem()).Interface().(runtime.Object)

	// Try normal decode first
	decoder := gob.NewDecoder(NewDirectIOReader(payloadFile))
	err = decoder.Decode(obj)
	if err != nil {
		// If it fails with type error, try legacy decoding via external tool
		if strings.Contains(err.Error(), "gob: wrong type") || strings.Contains(err.Error(), "extra fields") {
			logger.L().Ctx(ctx).Info("appendGobObjectFromFile - detected gob type mismatch, attempting external migration", helpers.Error(err), helpers.String("path", path))

			// Rewrite the object in the modern format to complete the migration
			// We upgrade to a write lock BEFORE running the migration tool to prevent concurrent migration attempts
			s.locks.RUnlock(key)
			// re-acquire read lock if anything fails or when we are done
			defer s.locks.RLock(ctx, key)

			if lockErr := s.locks.Lock(ctx, key); lockErr != nil {
				logger.L().Ctx(ctx).Error("appendGobObjectFromFile - failed to acquire write lock for migration", helpers.Error(lockErr), helpers.String("path", path))
				return fmt.Errorf("failed to acquire write lock for migration: %w", lockErr)
			}
			defer s.locks.Unlock(key)

			// Re-check if the file still needs migration now that we have the write lock
			// Another thread might have finished the migration while we were waiting for the lock
			payloadFileRetry, err := s.openPayloadFileWithFallback(path, os.O_RDONLY, 0)
			if err == nil {
				decoderRetry := gob.NewDecoder(NewDirectIOReader(payloadFileRetry))
				errRetry := decoderRetry.Decode(obj)
				_ = payloadFileRetry.Close()
				if errRetry == nil {
					logger.L().Ctx(ctx).Info("appendGobObjectFromFile - migration already completed by another thread", helpers.String("path", path))
					v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))
					return nil
				}
			}

			typeName := "ContainerProfile"
			if _, ok := obj.(*softwarecomposition.SeccompProfile); ok {
				typeName = "SeccompProfile"
			}

			// Run migration tool: /usr/bin/migration -file <path> -type <typeName>
			migrationCtx, migrationCancel := context.WithTimeout(ctx, 30*time.Second)
			defer migrationCancel()

			cmd := exec.CommandContext(migrationCtx, "/usr/bin/migration", "-file", path, "-type", typeName)
			var out bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			if runErr := cmd.Run(); runErr != nil {
				if errors.Is(migrationCtx.Err(), context.DeadlineExceeded) {
					logger.L().Ctx(ctx).Error("appendGobObjectFromFile - migration tool timed out", helpers.String("path", path))
					return nil // treat as skip/corrupted in list operations
				}
				logger.L().Ctx(ctx).Error("appendGobObjectFromFile - migration tool failed", helpers.Error(runErr), helpers.String("stderr", stderr.String()), helpers.String("path", path))
				// If migration tool fails, treat as corrupted and skip
				return nil
			}

			// Migration tool outputted JSON, unmarshal it into obj
			if unmarshalErr := json.Unmarshal(out.Bytes(), obj); unmarshalErr != nil {
				logger.L().Ctx(ctx).Error("appendGobObjectFromFile - unmarshal migrated JSON failed", helpers.Error(unmarshalErr), helpers.String("path", path))
				return unmarshalErr
			}

			logger.L().Ctx(ctx).Info("appendGobObjectFromFile - external migration successful", helpers.String("path", path))

			// Write modern format back to disk to finish migration
			poolCtx, cancel := poolContext()
			defer cancel()
			conn, err := s.pool.Take(poolCtx)
			if err != nil {
				logger.L().Ctx(ctx).Error("appendGobObjectFromFile - failed to take connection for migration save", helpers.Error(err), helpers.String("path", path))
			} else {
				defer s.pool.Put(conn)
				if _, saveErr := s.saveObject(conn, key, obj, nil, ""); saveErr != nil {
					logger.L().Ctx(ctx).Error("appendGobObjectFromFile - failed to rewrite migrated object", helpers.Error(saveErr), helpers.String("path", path))
				} else {
					logger.L().Ctx(ctx).Info("appendGobObjectFromFile - successfully migrated object to modern format", helpers.String("path", path))
				}
			}
		} else {
			return err
		}
	}

	v.Set(reflect.Append(v, reflect.ValueOf(obj).Elem()))

	return nil
}

func (s *StorageImpl) CalculateChecksum(in runtime.Object) (string, error) {
	// convert to v1beta1 object
	obj, err := s.scheme.ConvertToVersion(in, v1beta1.SchemeGroupVersion)
	if err != nil {
		return "", fmt.Errorf("convert to v1beta1: %w", err)
	}
	utils.RemoveManagedFields(obj.(metav1.Object))
	// add type meta information to the object
	sl := strings.Split(reflect.ValueOf(obj).Elem().Type().String(), ".")
	for len(sl) < 2 {
		sl = append(sl, "")
	}
	obj.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{
		Group:   softwarecomposition.GroupName,
		Version: sl[0],
		Kind:    sl[1],
	})
	b, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal object: %w", err)
	}
	hash, err := utils.CanonicalHash(b)
	if err != nil {
		return "", fmt.Errorf("calculate checksum: %w", err)
	}
	return hash, nil
}

func getNamespaceFromKey(key string) string {
	keySplit := strings.Split(key, "/")
	if len(keySplit) != 4 {
		return ""
	}

	return keySplit[3]
}

// replaceKeyForKind encapsulates the logic of replacing the kind in the key with the given kind.
func replaceKeyForKind(key string, kind string) string {
	keySplit := strings.Split(key, "/")
	keySplit[2] = strings.ToLower(kind)

	return strings.Join(keySplit, "/")
}

type immutableStorage struct{}

// Create is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Create(_ context.Context, key string, _, _ runtime.Object, _ uint64) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Delete is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Delete(_ context.Context, key string, _ runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object, _ storage.DeleteOptions) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Watch is not supported for immutable objects. Objects are generated on the fly and not stored.
// It returns an idle, event-free watch that closes on client disconnect or Stop:
// a pre-closed channel would send reflectors into a "very short watch" tight retry loop.
func (immutableStorage) Watch(ctx context.Context, _ string, _ storage.ListOptions) (watch.Interface, error) {
	return newIdleWatch(ctx), nil
}

// GuaranteedUpdate is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) GuaranteedUpdate(_ context.Context, key string, _ runtime.Object, _ bool, _ *storage.Preconditions, _ storage.UpdateFunc, _ runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Count is not supported for immutable objects. Objects are generated on the fly and not stored.
func (immutableStorage) Count(key string) (int64, error) {
	return 0, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

func (immutableStorage) ReadinessCheck() error {
	return nil
}

// RequestWatchProgress fulfills the storage.Interface
//
// It’s function is only relevant to etcd.
func (immutableStorage) RequestWatchProgress(context.Context) error { return nil }

// Versioner Returns fixed versioner associated with this interface.
func (immutableStorage) Versioner() storage.Versioner {
	return storage.APIObjectVersioner{}
}

func clearSpec(obj runtime.Object) {
	v := reflect.ValueOf(obj)
	for v.Kind() == reflect.Ptr {
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		f := v.FieldByName("Spec")
		if f.IsValid() && f.CanSet() {
			f.Set(reflect.Zero(f.Type()))
		}
	}
}
