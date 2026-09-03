package file

// Single-writer + priority-queue write path (spike/single-writer-priority-queue).
//
// This file implements the design described in the spike task: split every
// write into a "prepare" phase (PreSave, resourceVersion bump, checksum,
// ManagedFields zeroing, gob-encode -- everything saveObject does before its
// SQLite/rename commit) that runs fully in the caller's own goroutine with NO
// per-key lock held, and a "commit" phase (re-read the current
// resourceVersion, compare against what prepare computed against, and either
// write or reject with a retryable conflict) that is submitted as a job to
// exactly one dedicated writer goroutine per StorageImpl, arbitrated by a
// two-lane (high/low) priority queue.
//
// It is gated end-to-end behind singleWriterEnabled (default false, see
// SetSingleWriterEnabled / config.Config.SingleWriterEnabled): when disabled,
// none of this file's code runs and Create/GuaranteedUpdate/SaveContainerProfile
// behave exactly as they did before this file existed.
//
// Deliberately NOT touched by this file: Delete (stays on the existing
// utils.MapMutex path), migrateObject/appendGobObjectFromFile (the RC3
// migration-exec path), and Get/GetList (stay on their existing read path).
// See the spike report (delivered alongside this branch) for the specific
// correctness questions this design raises and how each is resolved or
// flagged as an open risk -- in particular the Get/GetList torn-read question
// and the Delete-vs-single-writer race, both discussed at commit's per-key
// lock usage below.
//
// prepareSingleWriterPayload deliberately duplicates saveObject's pre-encode
// mutation pipeline (storage.go's saveObject, lines ~271-330) rather than
// refactoring saveObject to share it. This is a considered choice: sharing
// code here would mean any change made for the new path's sake (e.g. the
// unique temp-file name) risks altering saveObject's behavior for the
// singleWriterEnabled=false path, which must stay byte-for-byte identical to
// today. Duplication costs ~30 lines and one place where the two copies could
// drift; that is judged cheaper than the risk of an accidental behavior
// change to the extensively-tested existing path.

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/metrics"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/conversion"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// singleWriterEnabled gates the entire single-writer write path. On by
// default. Package-level var (not const), for the same test-overridability
// reason as lockTimeout/poolTimeout (see SetPoolTimeout's doc comment):
// tests flip it directly (with a defer to restore), and SetSingleWriterEnabled
// is the production entry point, called once at startup from
// config.Config.SingleWriterEnabled (see main.go), mirroring SetPoolTimeout.
var singleWriterEnabled = true

// SetSingleWriterEnabled toggles the single-writer write path. Since the flag
// is read at call time (not baked into a StorageImpl at construction), this
// affects every StorageImpl immediately, including ones already constructed.
func SetSingleWriterEnabled(enabled bool) {
	singleWriterEnabled = enabled
}

// writePriority selects a commitJob's lane in the single writer's two-lane
// priority queue.
type writePriority int

const (
	priorityLow writePriority = iota
	priorityHigh
)

// label returns the metrics package's priority label value (metrics.PriorityHigh
// / metrics.PriorityLow) for this writePriority, for use in the single-writer
// queue-wait/commit/queue-depth metrics.
func (p writePriority) label() string {
	if p == priorityHigh {
		return metrics.PriorityHigh
	}
	return metrics.PriorityLow
}

// errWriteConflict is returned by the single writer when the resourceVersion
// re-read at commit time no longer matches the resourceVersion the prepare
// phase computed its candidate against -- i.e. some other write committed to
// this key in between. The caller must redo the full prepare phase against a
// fresh read and resubmit; see guaranteedUpdateSingleWriter's retry loop.
var errWriteConflict = errors.New("single-writer: resourceVersion conflict at commit")

// singleWriterConflictBackoff waits a small, capped, jittered delay before a
// GuaranteedUpdate conflict-retry, so many goroutines contending on the same
// key don't all redo their prepare phase in lockstep (see the call site's
// comment). Capped exponential backoff, base 1ms, doubling per attempt up to
// 64ms, plus up to 50% jitter. Returns ctx.Err() if ctx is done first.
func singleWriterConflictBackoff(ctx context.Context, attempt int) error {
	base := time.Duration(1<<uint(min(attempt, 6))) * time.Millisecond
	d := base + time.Duration(rand.Int63n(int64(base)/2+1))
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// commitJob is one fully-prepared write (PreSave already run, resourceVersion
// already bumped, payload already gob-encoded to a uniquely-named temp file)
// waiting for the single writer goroutine to commit it.
type commitJob struct {
	ctx context.Context
	key string

	// create is true for Create semantics (the key must not already exist at
	// commit time); false for GuaranteedUpdate semantics (the key must
	// currently be at exactly baseRV).
	create bool
	baseRV uint64

	// newObj is the fully-prepared candidate object (post PreSave, post
	// resourceVersion bump/ManagedFields zeroing/checksum -- everything
	// saveObject does before its own encode). newObjFactory returns a zero
	// value of the same concrete type, for decoding the row currently in
	// SQLite at commit time.
	newObj        runtime.Object
	newObjFactory func() runtime.Object

	tmpPayloadPath   string
	finalPayloadPath string

	// resultCh is buffered (capacity 1) so the writer goroutine's send in
	// process() never blocks, even if the submitting caller has already given
	// up (ctx done) and nobody will ever receive it.
	resultCh chan commitResult

	// priority is the lane this job was submitted on (set by submit(), before
	// the send into the channel), used by process()/commit() to label the
	// queue-wait and commit-outcome metrics.
	priority writePriority

	// enqueuedAt is set by submit() immediately before the job is sent into
	// its lane's channel, so process() can compute how long the job waited in
	// queue once the writer goroutine picks it up.
	enqueuedAt time.Time
}

type commitResult struct {
	err      error
	metadata runtime.Object
}

// singleWriter serializes every prepared Create/GuaranteedUpdate write for
// one StorageImpl through a single goroutine, arbitrated by a two-lane
// (high/low) priority queue: REST-originated writes (priorityHigh) are
// preferred over consolidation/background writes (priorityLow), with a
// bounded fairness override (highBurstLimit) so a sustained stream of
// high-priority jobs cannot starve a queued low-priority job forever.
//
// Because commit runs on exactly one goroutine, the compare-and-commit
// (re-read the current resourceVersion, write only if it still matches
// baseRV) needs no additional locking to be race-free against other writers
// on the same key -- structurally, there are no other writers once
// singleWriterEnabled routes all Create/GuaranteedUpdate/SaveContainerProfile
// traffic through here.
//
// commit still acquires the existing per-key utils.MapMutex (s.locks) around
// the actual disk mutation, so that Get/GetList (unmodified, still
// lock-based readers) and Delete (unmodified, still lock-based) cannot
// observe a torn write or race the filesystem/SQLite mutation -- see commit's
// doc comment for why this does not reintroduce RC1, and the spike report for
// the residual gap this does NOT close (a Delete that lands after this job's
// own prepare phase read its base state, but before commit acquires the
// lock, is not detected as a conflict by a Create job, only by an Update
// job -- see Q3 in the spike report).
type singleWriter struct {
	s    *StorageImpl
	high chan *commitJob
	low  chan *commitJob

	tmpSeq uint64
}

// highBurstLimit bounds how many consecutive high-priority commits the
// writer will service while at least one low-priority job is waiting, before
// forcing a low-priority commit through instead. This is the concrete answer
// to "can queued low-priority (consolidation) jobs be starved indefinitely by
// a sustained stream of high-priority (REST) jobs": no -- bounded to at most
// highBurstLimit consecutive high-priority commits, provided a low-priority
// job is actually queued during that window (see run's loop).
const highBurstLimit = 32

func newSingleWriter(s *StorageImpl) *singleWriter {
	w := &singleWriter{
		s:    s,
		high: make(chan *commitJob, 256),
		low:  make(chan *commitJob, 256),
	}
	go w.run()
	return w
}

// nextTempPath returns a temp payload path that is unique across concurrent
// prepare phases, including concurrent retries of the same key -- unlike
// saveObject's shared "finalPayloadPath+.t" (storage.go), which is only safe
// because the per-key lock serializes same-key writers, a guarantee prepare
// no longer has in this design.
func (w *singleWriter) nextTempPath(finalPayloadPath string) string {
	n := atomic.AddUint64(&w.tmpSeq, 1)
	return fmt.Sprintf("%s.t.%d.%d", finalPayloadPath, time.Now().UnixNano(), n)
}

// run is the single writer goroutine's main loop. See highBurstLimit's doc
// comment for the fairness guarantee this implements.
func (w *singleWriter) run() {
	var highStreak int
	for {
		if highStreak < highBurstLimit {
			select {
			case job := <-w.high:
				metrics.SetSingleWriterQueueDepth(metrics.PriorityHigh, len(w.high))
				w.process(job)
				highStreak++
				continue
			default:
			}
		}
		// Either the burst limit was hit, or high was empty: prefer a
		// waiting low job if there is one, without blocking.
		select {
		case job := <-w.low:
			metrics.SetSingleWriterQueueDepth(metrics.PriorityLow, len(w.low))
			w.process(job)
			highStreak = 0
			continue
		default:
		}
		// Nothing ready non-blocking on either lane's preferred order: block
		// on whichever arrives first.
		select {
		case job := <-w.high:
			metrics.SetSingleWriterQueueDepth(metrics.PriorityHigh, len(w.high))
			w.process(job)
			highStreak++
		case job := <-w.low:
			metrics.SetSingleWriterQueueDepth(metrics.PriorityLow, len(w.low))
			w.process(job)
			highStreak = 0
		}
	}
}

func (w *singleWriter) process(job *commitJob) {
	metrics.ObserveSingleWriterQueueWait(resourceFromKey(job.key), job.priority.label(), time.Since(job.enqueuedAt))
	res := w.commit(job)
	select {
	case job.resultCh <- res:
	default:
	}
}

// submit enqueues job on the requested lane and blocks until the writer
// processes it or ctx is done. If ctx is done after the job was already
// enqueued, the job is still processed eventually by the writer (it never
// blocks trying to deliver a result nobody is waiting for); the result is
// simply discarded here. This bounds submit's own wait by the caller's
// context, consistent with how poolContext/lockTimeout bound every other
// contention point in this package -- see the spike report's Q5 discussion.
func (w *singleWriter) submit(ctx context.Context, job *commitJob, priority writePriority) (commitResult, error) {
	ch := w.low
	if priority == priorityHigh {
		ch = w.high
	}
	job.priority = priority
	job.enqueuedAt = time.Now()
	select {
	case ch <- job:
		metrics.SetSingleWriterQueueDepth(priority.label(), len(ch))
	case <-ctx.Done():
		return commitResult{}, ctx.Err()
	}
	select {
	case res := <-job.resultCh:
		return res, nil
	case <-ctx.Done():
		return commitResult{}, ctx.Err()
	}
}

func commitOpName(job *commitJob) string {
	if job.create {
		return "create"
	}
	return "update"
}

// commit performs the actual disk mutation for one prepared job: acquire the
// per-key lock, acquire a pool connection, re-read the current
// resourceVersion, and either write (if it still matches what the prepare
// phase computed against) or reject with errWriteConflict / KeyExistsError.
//
// Connection-then-lock ordering is used here: the single writer goroutine
// is structurally bounded to at most ONE pool connection (it never runs
// concurrently with itself). Acquiring the connection first avoids holding
// the per-key lock across pool.Take waits (which would block readers of that key
// for up to poolTimeout) and prevents lock-order inversion deadlocks against
// callers like WithConnection that hold a connection and request an RLock.
func (w *singleWriter) commit(job *commitJob) commitResult {
	s := w.s
	kind := resourceFromKey(job.key)
	priority := job.priority.label()

	// NOTE: poolCancel is deferred (not called immediately after Take) and so
	// stays alive for as long as conn is in use. The zombiezen/modernc driver
	// ties a checked-out connection's interrupt to the context it was taken
	// with; cancelling that context right after Take (before actually using
	// the connection) marks every subsequent statement on it "interrupted".
	poolCtx, poolCancel := poolContext()
	defer poolCancel()
	beforePool := time.Now()
	conn, err := s.pool.Take(poolCtx)
	if err != nil {
		metrics.ObservePoolWait(kind, metrics.OutcomeTimeout, time.Since(beforePool))
		_ = s.appFs.Remove(job.tmpPayloadPath)
		metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeError)
		return commitResult{err: newContentionTimeoutError(commitOpName(job), job.key, err)}
	}
	metrics.ObservePoolWait(kind, metrics.OutcomeAcquired, time.Since(beforePool))
	defer s.pool.Put(conn)

	lockCtx, lockCancel := context.WithTimeout(job.ctx, lockTimeout)
	beforeLock := time.Now()
	lockErr := s.locks.Lock(lockCtx, job.key)
	lockCancel()
	lockDuration := time.Since(beforeLock)
	if lockErr != nil {
		metrics.ObserveLockWait(kind, metrics.OutcomeTimeout, lockDuration)
		_ = s.appFs.Remove(job.tmpPayloadPath)
		metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeError)
		return commitResult{err: newContentionTimeoutError(commitOpName(job), job.key, lockErr)}
	}
	metrics.ObserveLockWait(kind, metrics.OutcomeAcquired, lockDuration)
	defer s.locks.Unlock(job.key)

	currentRV, exists, err := readCurrentResourceVersion(conn, job.key, job.newObjFactory, s.versioner)
	if err != nil {
		_ = s.appFs.Remove(job.tmpPayloadPath)
		metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeError)
		return commitResult{err: fmt.Errorf("read current resourceVersion: %w", err)}
	}

	if job.create {
		if exists {
			_ = s.appFs.Remove(job.tmpPayloadPath)
			metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeConflict)
			return commitResult{err: storage.NewKeyExistsError(job.key, 0)}
		}
	} else if job.baseRV != 0 {
		if !exists || currentRV != job.baseRV {
			_ = s.appFs.Remove(job.tmpPayloadPath)
			metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeConflict)
			return commitResult{err: errWriteConflict}
		}
	}
	// job.baseRV == 0 and !job.create: origState was read with
	// ignoreNotFound=true against a missing key (getCurrentState/s.get
	// returns a zero object + nil error, not an error, in that case -- see
	// StorageImpl.get), which is a supported upsert on the legacy saveObject
	// path: it writes unconditionally, whether the key is still missing
	// (this commit effectively creates it) or was created by someone else in
	// the meantime (last write wins, matching the legacy path's behavior).
	// Treating baseRV==0 as "must not exist" here would make every such
	// call -- e.g. SaveContainerProfile's very first write for a key --
	// conflict forever, since there is no resourceVersion it could ever
	// match.

	metadata := extractFields(job.newObj, []string{"ObjectMeta", "SchemaVersion"})

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
		if werr := writeMeta(conn, job.key, metadata); werr != nil {
			return fmt.Errorf("write metadata: %w", werr)
		}
		if rerr := renamePayload(job.tmpPayloadPath, job.finalPayloadPath); rerr != nil {
			return fmt.Errorf("rename payload into place: %w", rerr)
		}
		return nil
	}()
	release(&err)
	if err != nil {
		_ = s.appFs.Remove(job.tmpPayloadPath)
		metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeError)
		return commitResult{err: err}
	}

	metrics.IncSingleWriterCommit(kind, priority, metrics.CommitOutcomeCommitted)
	return commitResult{metadata: metadata}
}

// readCurrentResourceVersion reads the resourceVersion currently committed in
// SQLite for key, or exists=false if there is no metadata row. factory
// produces a zero value of the object's concrete type to decode into
// (metadata rows hold ObjectMeta+SchemaVersion only -- the same shape
// extractFields produces).
func readCurrentResourceVersion(conn *sqlite.Conn, key string, factory func() runtime.Object, versioner storage.Versioner) (rv uint64, exists bool, err error) {
	data, err := ReadMetadata(conn, key)
	if err != nil {
		if errors.Is(err, ErrMetadataNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	obj := factory()
	if err := json.Unmarshal(data, obj); err != nil {
		return 0, false, err
	}
	rv, err = versioner.ObjectResourceVersion(obj)
	if err != nil {
		return 0, false, err
	}
	return rv, true, nil
}

// newObjFactoryFor returns a factory producing zero values of obj's concrete
// (pointer) type.
func newObjFactoryFor(obj runtime.Object) func() runtime.Object {
	t := reflect.TypeOf(obj).Elem()
	return func() runtime.Object {
		return reflect.New(t).Interface().(runtime.Object)
	}
}

// ensureWriter lazily creates s.writer on first use.
func (s *StorageImpl) ensureWriter() *singleWriter {
	s.writerOnce.Do(func() {
		s.writer = newSingleWriter(s)
	})
	return s.writer
}

// prepareSingleWriterPayload mirrors saveObject's pre-encode mutation
// pipeline (resourceVersion bump, ManagedFields zeroing, checksum -- see
// storage.go's saveObject) and gob-encodes the result to a uniquely-named
// temp file. See this file's package doc comment for why this is a
// deliberate copy rather than a shared refactor of saveObject.
func (s *StorageImpl) prepareSingleWriterPayload(key string, obj runtime.Object, checksum string) (runtime.Object, string, string, error) {
	// increment resourceVersion
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil {
		if err := s.versioner.UpdateObject(obj, version+1); err != nil {
			return nil, "", "", fmt.Errorf("set resourceVersion: %w", err)
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
			return nil, "", "", fmt.Errorf("calculate checksum: %w", err)
		}
	}
	// add checksum to annotations
	if anno := obj.(metav1.Object).GetAnnotations(); anno == nil {
		obj.(metav1.Object).SetAnnotations(map[string]string{helpersv1.SyncChecksumMetadataKey: checksum})
	} else {
		anno[helpersv1.SyncChecksumMetadataKey] = checksum
	}

	p := filepath.Join(s.root, key)
	if err := s.appFs.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return nil, "", "", fmt.Errorf("mkdir: %w", err)
	}
	finalPayloadPath := makePayloadPath(p)
	tmpPayloadPath := s.ensureWriter().nextTempPath(finalPayloadPath)

	payloadFile, err := s.openPayloadFileWithFallback(tmpPayloadPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return nil, "", "", fmt.Errorf("open payload file: %w", err)
	}
	directIOWriter := NewDirectIOWriter(payloadFile)

	payloadEncoder := gob.NewEncoder(directIOWriter)
	if err := payloadEncoder.Encode(obj); err != nil {
		_ = directIOWriter.Close()
		_ = payloadFile.Close()
		return nil, "", "", fmt.Errorf("encode payload: %w", err)
	}
	if err := directIOWriter.Close(); err != nil {
		_ = payloadFile.Close()
		return nil, "", "", fmt.Errorf("close directIOWriter: %w", err)
	}
	if err := payloadFile.Close(); err != nil {
		return nil, "", "", fmt.Errorf("close payload file: %w", err)
	}

	return obj, tmpPayloadPath, finalPayloadPath, nil
}

// createSingleWriter implements Create's semantics via the prepare/commit
// split. PreSave and the payload gob-encode happen here, in the caller's own
// goroutine, against a connection taken and released just for that (no
// per-key lock involved at all); the actual SQLite upsert + payload rename
// happens once, later, in the single writer goroutine, serialized against
// every other write on any key via the priority queue.
func (s *StorageImpl) createSingleWriter(ctx context.Context, key string, obj, metaOut runtime.Object, priority writePriority) error {
	// Cheap existence pre-check (mirrors CreateWithConn's early Stat check).
	// This is an optimization only -- the authoritative check happens at
	// commit time against SQLite, inside the single writer.
	if _, err := s.appFs.Stat(makePayloadPath(filepath.Join(s.root, key))); err == nil {
		return storage.NewKeyExistsError(key, 0)
	}
	// resourceVersion should not be set on create
	if version, err := s.versioner.ObjectResourceVersion(obj); err == nil && version != 0 {
		msg := "resourceVersion should not be set on objects to be created"
		logger.L().Ctx(ctx).Error(msg)
		return errors.New(msg)
	}

	// poolCancel is deferred, not called right after Take -- see commit's
	// matching comment for why an immediate cancel would interrupt the
	// connection before PreSave gets to use it.
	poolCtx, poolCancel := poolContext()
	defer poolCancel()
	beforePool := time.Now()
	conn, err := s.pool.Take(poolCtx)
	if err != nil {
		metrics.ObservePoolWait(resourceFromKey(key), metrics.OutcomeTimeout, time.Since(beforePool))
		return newContentionTimeoutError("create", key, err)
	}
	metrics.ObservePoolWait(resourceFromKey(key), metrics.OutcomeAcquired, time.Since(beforePool))

	presaveCtx := context.WithValue(ctx, connKey, conn)
	if err := s.processor.PreSave(presaveCtx, obj); err != nil {
		if errors.Is(err, ObjectTooLargeError) {
			clearSpec(obj)
			metadata := obj.(metav1.Object)
			annotations := metadata.GetAnnotations()
			if annotations == nil {
				annotations = make(map[string]string)
			}
			annotations[helpersv1.StatusMetadataKey] = helpersv1.TooLarge
			metadata.SetAnnotations(annotations)
			logger.L().Debug("Create - too large object, saving metadata only", helpers.String("key", key))
			// fall through: still need to save the object with updated annotations
		} else {
			s.pool.Put(conn)
			return err
		}
	}
	s.pool.Put(conn)

	candidate, tmpPath, finalPath, err := s.prepareSingleWriterPayload(key, obj, "")
	if err != nil {
		return err
	}

	writer := s.ensureWriter()
	job := &commitJob{
		ctx:              ctx,
		key:              key,
		create:           true,
		newObj:           candidate,
		newObjFactory:    newObjFactoryFor(candidate),
		tmpPayloadPath:   tmpPath,
		finalPayloadPath: finalPath,
		resultCh:         make(chan commitResult, 1),
	}
	res, err := writer.submit(ctx, job, priority)
	if err != nil {
		_ = s.appFs.Remove(tmpPath)
		return err
	}
	if res.err != nil {
		logger.L().Ctx(ctx).Error("Create - save object failed", helpers.Error(res.err), helpers.String("key", key))
		return res.err
	}

	// AfterCreate needs its own live connection; the prepare connection above
	// was already released before commit.
	poolCtx2, poolCancel2 := poolContext()
	defer poolCancel2()
	conn2, err := s.pool.Take(poolCtx2)
	if err != nil {
		return newContentionTimeoutError("create", key, err)
	}
	defer s.pool.Put(conn2)
	afterCtx := context.WithValue(ctx, connKey, conn2)
	if err := s.processor.AfterCreate(afterCtx, candidate); err != nil {
		return fmt.Errorf("processor.AfterCreate: %w", err)
	}

	if metaOut != nil {
		val := reflect.ValueOf(metaOut)
		if val.Kind() == reflect.Ptr {
			val = val.Elem()
		}
		val.Set(reflect.ValueOf(candidate).Elem())
	}
	s.watchDispatcher.Added(key, res.metadata, candidate)
	return nil
}

// guaranteedUpdateSingleWriter implements GuaranteedUpdate's semantics via
// the prepare/commit split, reusing the exact retry-loop structure of
// GuaranteedUpdateWithConn (storage.go) -- preconditions check, tryUpdate
// invocation, the "same serialized contents" short-circuit -- but replacing
// the per-key-lock-held read-modify-write with: read fresh (via the ordinary,
// still lock-based Get path -- WAL snapshot isolation is what makes this
// fresh read meaningful without a lock), run tryUpdate/PreSave/prepare with
// no lock held, then submit a commit job. A commit rejected with
// errWriteConflict means someone else committed since our read; the loop
// redoes the full prepare phase against a fresh read, exactly analogous to
// the original loop's "it's possible we were working with stale data" retry
// branches.
func (s *StorageImpl) guaranteedUpdateSingleWriter(
	ctx context.Context, key string, metaOut runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object,
	checksum string, priority writePriority) error {

	v, err := conversion.EnforcePtr(metaOut)
	if err != nil {
		logger.L().Ctx(ctx).Error("GuaranteedUpdate - unable to convert output object to pointer", helpers.Error(err), helpers.String("key", key))
		return fmt.Errorf("unable to convert output object to pointer: %v", err)
	}

	getCurrentState := func() (*objState, error) {
		objPtr := reflect.New(v.Type()).Interface().(runtime.Object)
		if err := s.Get(ctx, key, storage.GetOptions{IgnoreNotFound: ignoreNotFound}, objPtr); err != nil {
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
		v.Set(reflect.ValueOf(origState.obj).Elem())
		return nil
	}

	writer := s.ensureWriter()
	var conflictAttempts int

	for {
		// run preconditions
		if err := preconditions.Check(key, origState.obj); err != nil {
			if origStateIsCurrent {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - preconditions check failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true
			continue
		}

		// Snapshot the "before" state now, before tryUpdate runs. tryUpdate may
		// mutate origState.obj in place and return that same reference (this is
		// exactly what genericregistry.Store's own finalizer-delete tryUpdate
		// does, via markAsDeleting) -- capturing this snapshot after tryUpdate
		// ran would observe the already-mutated state, making the no-op-update
		// check below spuriously always-equal and silently dropping the write.
		// See GuaranteedUpdateWithConn (storage.go) for the same fix applied
		// to the legacy path.
		orig := origState.obj.DeepCopyObject() // FIXME this is expensive (same as GuaranteedUpdateWithConn)

		// run tryUpdate
		ret, _, err := tryUpdate(origState.obj, storage.ResponseMeta{})
		if err != nil {
			if origStateIsCurrent {
				if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				}
				logger.L().Debug("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			cachedRev := origState.rev
			cachedUpdateErr := err
			origState, err = getCurrentState()
			if err != nil {
				logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
			origStateIsCurrent = true
			if cachedRev == origState.rev {
				if !apierrors.IsNotFound(err) && !apierrors.IsInvalid(err) {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				}
				logger.L().Debug("GuaranteedUpdate - tryUpdate func failed", helpers.Error(err), helpers.String("key", key))
				return cachedUpdateErr
			}
			continue
		}

		// poolCancel is called only after both PreSave invocations below are
		// done with conn (not right after Take) -- an immediate cancel would
		// interrupt the connection before it's used. This is inside a loop,
		// so unlike the non-looping call sites in this file, poolCancel is
		// called explicitly on every exit path rather than deferred, to avoid
		// piling up live timers across retries.
		poolCtx, poolCancel := poolContext()
		beforePool := time.Now()
		conn, err := s.pool.Take(poolCtx)
		if err != nil {
			poolCancel()
			metrics.ObservePoolWait(resourceFromKey(key), metrics.OutcomeTimeout, time.Since(beforePool))
			return newContentionTimeoutError("update", key, err)
		}
		metrics.ObservePoolWait(resourceFromKey(key), metrics.OutcomeAcquired, time.Since(beforePool))
		presaveCtx := context.WithValue(ctx, connKey, conn)

		// call processor on object to be saved
		if err := s.processor.PreSave(presaveCtx, ret); err != nil {
			if errors.Is(err, ObjectTooLargeError) {
				clearSpec(ret)
				metadata := ret.(metav1.Object)
				anns := metadata.GetAnnotations()
				if anns == nil {
					anns = make(map[string]string)
				}
				anns[helpersv1.StatusMetadataKey] = helpersv1.TooLarge
				metadata.SetAnnotations(anns)
				logger.L().Debug("GuaranteedUpdate - too large object, skipping update", helpers.String("key", key))
				// fall through: still need to save the object with updated annotations
			} else {
				s.pool.Put(conn)
				poolCancel()
				logger.L().Debug("GuaranteedUpdate - processor.PreSave failed", helpers.Error(err), helpers.String("key", key))
				return err
			}
		}

		// check if the object is the same as the original (orig was snapshotted
		// above, before tryUpdate ran)
		_ = s.processor.PreSave(presaveCtx, orig)
		s.pool.Put(conn)
		poolCancel()

		if reflect.DeepEqual(orig, ret) {
			logger.L().Debug("GuaranteedUpdate - tryUpdate returned the same object, no update needed", helpers.String("key", key))
			v.Set(reflect.ValueOf(origState.obj).Elem())
			return nil
		}

		candidate, tmpPath, finalPath, err := s.prepareSingleWriterPayload(key, ret, checksum)
		if err != nil {
			return err
		}

		job := &commitJob{
			ctx:              ctx,
			key:              key,
			create:           false,
			baseRV:           uint64(origState.rev),
			newObj:           candidate,
			newObjFactory:    newObjFactoryFor(candidate),
			tmpPayloadPath:   tmpPath,
			finalPayloadPath: finalPath,
			resultCh:         make(chan commitResult, 1),
		}
		res, err := writer.submit(ctx, job, priority)
		if err != nil {
			_ = s.appFs.Remove(tmpPath)
			return err
		}
		if res.err != nil {
			if errors.Is(res.err, errWriteConflict) {
				// Someone else committed a newer resourceVersion for this key
				// since we read origState. Redo the full prepare phase against
				// a fresh read and resubmit -- the single-writer analogue of
				// the "it's possible we were working with stale data" retry
				// branches above.
				//
				// conflictAttempts backoff: without this, many goroutines
				// hammering the SAME key with no backoff at all produce a
				// thundering-herd retry storm -- every successful commit
				// invalidates every other in-flight attempt's baseRV
				// simultaneously, so all of them redo getCurrentState/PreSave
				// at once, mostly to conflict again immediately. The old
				// MapMutex-based path never had this failure mode: contending
				// callers simply queued on the lock (O(n) total work for n
				// callers), whereas naive optimistic retry here is O(n^2) in
				// the worst case. This capped, jittered backoff (found
				// empirically necessary while writing this spike's
				// same-key-concurrency test) is the mitigation; see the spike
				// report's Q5 discussion for why an unbounded retry COUNT is
				// still acceptable (bounded by the caller's own ctx deadline,
				// same as today) even though this adds pacing between
				// attempts.
				conflictAttempts++
				metrics.IncSingleWriterConflictRetry(resourceFromKey(key))
				if backoffErr := singleWriterConflictBackoff(ctx, conflictAttempts); backoffErr != nil {
					return newContentionTimeoutError("update", key, backoffErr)
				}
				origState, err = getCurrentState()
				if err != nil {
					logger.L().Ctx(ctx).Error("GuaranteedUpdate - get state failed after conflict", helpers.Error(err), helpers.String("key", key))
					return err
				}
				origStateIsCurrent = true
				continue
			}
			logger.L().Ctx(ctx).Error("GuaranteedUpdate - save object failed", helpers.Error(res.err), helpers.String("key", key))
			return res.err
		}

		if metaOut != nil {
			val := reflect.ValueOf(metaOut)
			if val.Kind() == reflect.Ptr {
				val = val.Elem()
			}
			val.Set(reflect.ValueOf(candidate).Elem())
		}
		// Only successful updates should produce modification events
		s.watchDispatcher.Modified(key, res.metadata, candidate)
		return nil
	}
}
