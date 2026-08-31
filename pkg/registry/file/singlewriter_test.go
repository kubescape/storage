package file

// Tests for the single-writer + priority-queue write path (see
// singlewriter.go). These exercise the concrete correctness properties the
// spike report claims:
//
//   - basic round trip through the new path (Create, Get, GuaranteedUpdate)
//   - no lost updates under concurrent GuaranteedUpdate on the SAME key
//   - concurrent GuaranteedUpdate on DIFFERENT keys prepare in parallel
//     (only the commit phase is serialized)
//   - priority ordering: a high-priority job queued behind several
//     low-priority jobs still commits before the remaining low-priority ones
//   - the commit-time per-key lock (added to close the torn-read gap
//     discussed in the spike report's Q2) actually prevents a concurrent Get
//     from observing a torn metadata/payload state
//   - a concurrent Delete racing a GuaranteedUpdate's commit is detected as a
//     conflict rather than silently resurrecting the deleted object (partial
//     mitigation of the spike report's Q3 gap for the update case)
//
// All tests flip the package-level singleWriterEnabled var directly (mirrors
// the existing lockTimeout/poolTimeout test pattern in storage_test.go) and
// restore it via defer; none use t.Parallel(), for the same reason those
// existing tests don't.

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/client-go/kubernetes/scheme"
)

// enableSingleWriter flips the package-level flag on and registers a cleanup
// to restore it, mirroring the existing lockTimeout/poolTimeout test pattern.
func enableSingleWriter(t *testing.T) {
	t.Helper()
	old := singleWriterEnabled
	singleWriterEnabled = true
	t.Cleanup(func() { singleWriterEnabled = old })
}

func newSingleWriterTestStorage(t *testing.T) (*StorageImpl, func()) {
	t.Helper()
	sch := scheme.Scheme
	install.Install(sch)
	pool := NewTestPool(t.TempDir())
	fs := afero.NewMemMapFs()
	si := NewStorageImpl(fs, "/", pool, nil, sch).(*StorageImpl)
	return si, func() { _ = pool.Close() }
}

// newSingleWriterTestStorageWithPoolSize is like newSingleWriterTestStorage
// but with an explicit SQLite pool size, for tests that deliberately drive
// many concurrent prepare phases (each of which briefly needs its own
// connection for PreSave) and want to isolate SAME-key write-serialization
// behavior from ordinary connection-pool sizing/timeout effects (already
// covered by PR #371's Phase 0 work), which is a separate concern from what
// these tests are checking.
func newSingleWriterTestStorageWithPoolSize(t *testing.T, poolSize int) (*StorageImpl, func()) {
	t.Helper()
	sch := scheme.Scheme
	install.Install(sch)
	dir := t.TempDir()
	pool := NewPool(dir+"/test.sq3", poolSize, 0)
	fs := afero.NewMemMapFs()
	si := NewStorageImpl(fs, "/", pool, nil, sch).(*StorageImpl)
	return si, func() { _ = pool.Close() }
}

func testProfileKey(name string) string {
	return "/spdx.softwarecomposition.kubescape.io/containerprofile/ns1/" + name
}

// TestSingleWriter_CreateThenGet is the basic round-trip: Create via the new
// path, then Get (the existing, unmodified read path) returns the correct
// object.
func TestSingleWriter_CreateThenGet(t *testing.T) {
	enableSingleWriter(t)
	si, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	key := testProfileKey("create-then-get")
	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "create-then-get", Namespace: "ns1", Labels: map[string]string{"v": "1"}},
	}
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Create(ctx, key, obj, out, 0))
	require.Equal(t, "1", out.ResourceVersion)

	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got))
	require.Equal(t, "1", got.Labels["v"])
	require.Equal(t, "1", got.ResourceVersion)

	// A second Create on the same key must fail (still enforced, now via the
	// commit-time compare-and-commit rather than the old Stat pre-check).
	dup := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "create-then-get", Namespace: "ns1"},
	}
	err := si.Create(ctx, key, dup, &softwarecomposition.ContainerProfile{}, 0)
	require.Error(t, err)
	require.True(t, storage.IsExist(err), "expected AlreadyExists, got %v", err)
}

// TestSingleWriter_GuaranteedUpdate_RoundTrip confirms GuaranteedUpdate via
// the new path applies the update function and bumps resourceVersion.
func TestSingleWriter_GuaranteedUpdate_RoundTrip(t *testing.T) {
	enableSingleWriter(t)
	si, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()

	ctx := context.Background()
	key := testProfileKey("update-roundtrip")
	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "update-roundtrip", Namespace: "ns1", Labels: map[string]string{"n": "0"}},
	}
	require.NoError(t, si.Create(ctx, key, obj, &softwarecomposition.ContainerProfile{}, 0))

	out := &softwarecomposition.ContainerProfile{}
	err := si.GuaranteedUpdate(ctx, key, out, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			cur.Labels["n"] = "1"
			return cur, nil, nil
		}, nil)
	require.NoError(t, err)
	require.Equal(t, "1", out.Labels["n"])
	require.Equal(t, "2", out.ResourceVersion, "resourceVersion must bump from 1 to 2")

	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got))
	require.Equal(t, "1", got.Labels["n"])
	require.Equal(t, "2", got.ResourceVersion)
}

// TestSingleWriter_ConcurrentUpdatesSameKey_NoLostUpdates hammers ONE key
// with many concurrent GuaranteedUpdate calls, each incrementing a counter.
// The single-writer's compare-and-commit must reject every commit whose
// baseRV has gone stale and force a re-prepare, so no successful update is
// ever silently lost: the final counter must equal exactly the number of
// calls (every one of which is expected to eventually succeed, since
// GuaranteedUpdate retries internally), and the final resourceVersion must
// reflect exactly that many writes (1 create + N updates).
func TestSingleWriter_ConcurrentUpdatesSameKey_NoLostUpdates(t *testing.T) {
	enableSingleWriter(t)
	// A larger pool than the default (10) isolates same-key
	// write-serialization correctness from ordinary connection-pool sizing
	// effects: 40 goroutines each need a connection briefly during prepare
	// (for PreSave) even though DefaultProcessor's PreSave is a no-op, and
	// with only the default pool size that alone can push some acquisitions
	// close to poolTimeout under this test's deliberately extreme, unstaggered
	// concurrency -- a real, separately-notable effect (see the spike
	// report's note on pool sizing interacting with prepare) but not what
	// this test is checking.
	si, cleanup := newSingleWriterTestStorageWithPoolSize(t, 64)
	defer cleanup()

	ctx := context.Background()
	key := testProfileKey("concurrent-same-key")
	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "concurrent-same-key", Namespace: "ns1", Labels: map[string]string{"counter": "0"}},
	}
	require.NoError(t, si.Create(ctx, key, obj, &softwarecomposition.ContainerProfile{}, 0))

	const n = 40
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out := &softwarecomposition.ContainerProfile{}
			errs[i] = si.GuaranteedUpdate(ctx, key, out, false, nil,
				func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
					cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
					n, _ := strconv.Atoi(cur.Labels["counter"])
					cur.Labels["counter"] = strconv.Itoa(n + 1)
					return cur, nil, nil
				}, nil)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "update %d failed", i)
	}

	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got))
	require.Equal(t, strconv.Itoa(n), got.Labels["counter"], "no update should be lost or double-applied")
	require.Equal(t, strconv.Itoa(n+1), got.ResourceVersion, "resourceVersion must reflect exactly 1 create + n updates")
}

// countingProcessor's PreSave tracks how many invocations are concurrently
// in-flight (peak) and optionally sleeps, so tests can observe whether
// prepare phases for different keys actually run in parallel.
type countingProcessor struct {
	DefaultProcessor
	sleep    time.Duration
	inflight int32
	peak     int32
}

func (p *countingProcessor) PreSave(ctx context.Context, obj runtime.Object) error {
	cur := atomic.AddInt32(&p.inflight, 1)
	for {
		peak := atomic.LoadInt32(&p.peak)
		if cur <= peak || atomic.CompareAndSwapInt32(&p.peak, peak, cur) {
			break
		}
	}
	if p.sleep > 0 {
		time.Sleep(p.sleep)
	}
	atomic.AddInt32(&p.inflight, -1)
	return nil
}

// TestSingleWriter_ConcurrentUpdatesDifferentKeys_PrepareRunsInParallel
// asserts that GuaranteedUpdate on N DIFFERENT keys, launched concurrently,
// overlap their prepare phases (proven by observing more than one PreSave
// in flight at once) rather than serializing on a shared lock the way the
// old MapMutex-based path would if it held the lock across PreSave. Only the
// (comparatively instantaneous) commit phase is serialized by the single
// writer goroutine.
func TestSingleWriter_ConcurrentUpdatesDifferentKeys_PrepareRunsInParallel(t *testing.T) {
	enableSingleWriter(t)
	sch := scheme.Scheme
	install.Install(sch)
	pool := NewTestPool(t.TempDir())
	defer func() { _ = pool.Close() }()
	fs := afero.NewMemMapFs()

	proc := &countingProcessor{sleep: 100 * time.Millisecond}
	si := NewStorageImplWithCollector(fs, "/", pool, nil, sch, proc).(*StorageImpl)

	ctx := context.Background()
	const n = 8
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = testProfileKey(fmt.Sprintf("parallel-prepare-%d", i))
		obj := &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("parallel-prepare-%d", i), Namespace: "ns1"},
		}
		require.NoError(t, si.Create(ctx, keys[i], obj, &softwarecomposition.ContainerProfile{}, 0))
	}

	// Reset peak after the Creates above (which also go through PreSave) so
	// this measures only the concurrent GuaranteedUpdate wave below.
	atomic.StoreInt32(&proc.peak, 0)

	var wg sync.WaitGroup
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			out := &softwarecomposition.ContainerProfile{}
			err := si.GuaranteedUpdate(ctx, key, out, false, nil,
				func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
					cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
					if cur.Labels == nil {
						cur.Labels = map[string]string{}
					}
					cur.Labels["touched"] = "1"
					return cur, nil, nil
				}, nil)
			require.NoError(t, err)
		}(keys[i])
	}
	wg.Wait()
	elapsed := time.Since(start)

	peak := atomic.LoadInt32(&proc.peak)
	require.Greater(t, peak, int32(1), "expected multiple PreSave calls in flight concurrently across different keys, got peak=%d", peak)
	// If prepare were serialized (like the commit phase is), n sleeps of
	// 100ms would take at least n*100ms. Parallel prepare should finish in
	// well under that.
	require.Less(t, elapsed, time.Duration(n)*100*time.Millisecond,
		"prepare phases for different keys appear to be serialized (took %s for n=%d sleeps of 100ms)", elapsed, n)
}

// TestSingleWriter_PriorityOrdering submits several low-priority jobs (for
// distinct keys), then -- while they are still queued behind an
// artificially slowed-down commit -- a high-priority job for another key.
// The high-priority job must commit before the remaining queued
// low-priority ones.
func TestSingleWriter_PriorityOrdering(t *testing.T) {
	enableSingleWriter(t)
	si, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()
	ctx := context.Background()

	// Slow every commit down so jobs queue up behind the first one instead of
	// completing before the next is submitted.
	realRename := si.appFs.Rename
	si.renamePayloadFn = func(oldpath, newpath string) error {
		time.Sleep(40 * time.Millisecond)
		return realRename(oldpath, newpath)
	}

	const lowCount = 5
	var mu sync.Mutex
	var completionOrder []string

	record := func(label string) {
		mu.Lock()
		completionOrder = append(completionOrder, label)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	// Fire off lowCount low-priority Creates for distinct keys, all roughly
	// at once: the first dequeued occupies the writer for ~40ms while the
	// rest sit queued in the low lane.
	for i := 0; i < lowCount; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := testProfileKey(fmt.Sprintf("prio-low-%d", i))
			obj := &softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("prio-low-%d", i), Namespace: "ns1"},
			}
			err := si.createSingleWriter(ctx, key, obj, &softwarecomposition.ContainerProfile{}, priorityLow)
			require.NoError(t, err)
			record(fmt.Sprintf("low-%d", i))
		}(i)
	}

	// Give the low jobs a moment to be submitted and start queuing, then
	// submit one high-priority job.
	time.Sleep(10 * time.Millisecond)
	wg.Add(1)
	go func() {
		defer wg.Done()
		key := testProfileKey("prio-high")
		obj := &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "prio-high", Namespace: "ns1"},
		}
		err := si.createSingleWriter(ctx, key, obj, &softwarecomposition.ContainerProfile{}, priorityHigh)
		require.NoError(t, err)
		record("high")
	}()

	wg.Wait()

	highIdx := -1
	for i, label := range completionOrder {
		if label == "high" {
			highIdx = i
		}
	}
	require.GreaterOrEqual(t, highIdx, 0, "high-priority job never completed")
	// The high job arrived after the low jobs were already submitted, so at
	// least one low job may already be in flight (non-preemptible) ahead of
	// it, but it must still beat the majority of the remaining queued low
	// jobs.
	lowAfterHigh := 0
	for _, label := range completionOrder[highIdx+1:] {
		if label != "high" {
			lowAfterHigh++
		}
	}
	require.GreaterOrEqual(t, lowAfterHigh, lowCount-2,
		"expected the high-priority job to overtake most of the queued low-priority jobs, completion order: %v", completionOrder)
}

// TestSingleWriter_CommitLockPreventsTornRead exercises the mitigation added
// to close the spike report's Q2 gap: commit() acquires the same per-key
// utils.MapMutex that Get's RLock uses, so a concurrent Get cannot observe a
// state where the payload file has been renamed into place but the SQLite
// metadata row has not (or vice versa). A slow renamePayloadFn widens the
// window a torn read would need to land in; every concurrent Get during that
// window must see either the fully-old or fully-new object, never an error
// or a mixed state.
func TestSingleWriter_CommitLockPreventsTornRead(t *testing.T) {
	enableSingleWriter(t)
	si, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()
	ctx := context.Background()
	key := testProfileKey("torn-read")

	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "torn-read", Namespace: "ns1", Labels: map[string]string{"v": "0"}},
	}
	require.NoError(t, si.Create(ctx, key, obj, &softwarecomposition.ContainerProfile{}, 0))

	realRename := si.appFs.Rename
	si.renamePayloadFn = func(oldpath, newpath string) error {
		time.Sleep(30 * time.Millisecond)
		return realRename(oldpath, newpath)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		out := &softwarecomposition.ContainerProfile{}
		err := si.GuaranteedUpdate(ctx, key, out, false, nil,
			func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
				cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
				cur.Labels["v"] = "1"
				return cur, nil, nil
			}, nil)
		require.NoError(t, err)
	}()

	var badReads int32
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got := &softwarecomposition.ContainerProfile{}
			if err := si.Get(ctx, key, storage.GetOptions{}, got); err != nil {
				atomic.AddInt32(&badReads, 1)
				continue
			}
			v := got.Labels["v"]
			rv := got.ResourceVersion
			// The only two consistent (metadata,payload) pairs are
			// (rv=1,v=0) and (rv=2,v=1). Anything else is a torn read.
			if !((rv == "1" && v == "0") || (rv == "2" && v == "1")) {
				atomic.AddInt32(&badReads, 1)
			}
		}
	}()

	wg.Wait()
	close(stop)
	readerWg.Wait()

	require.Equal(t, int32(0), atomic.LoadInt32(&badReads), "a concurrent Get observed a torn (metadata,payload) state")
}

// TestSingleWriter_DeleteDuringUpdateCommit_NoResurrection is a partial
// mitigation check for the spike report's Q3 (documented, not fully solved)
// gap: Delete (old MapMutex path, unmodified) and the single writer's commit
// now share the same per-key lock, so a Delete cannot interleave with (only
// precede or follow) a commit's write. If Delete completes AFTER the
// prepare phase read its base resourceVersion but BEFORE commit's lock is
// acquired, commit's compare-and-commit must see the row is gone and reject
// with a conflict rather than resurrecting the deleted object.
func TestSingleWriter_DeleteDuringUpdateCommit_NoResurrection(t *testing.T) {
	enableSingleWriter(t)
	si, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()
	ctx := context.Background()
	key := testProfileKey("delete-race")

	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "delete-race", Namespace: "ns1", Labels: map[string]string{"v": "0"}},
	}
	require.NoError(t, si.Create(ctx, key, obj, &softwarecomposition.ContainerProfile{}, 0))

	// Read the current state directly (mirrors getCurrentState's read) so we
	// can drive prepare+commit by hand with a Delete injected in between.
	cur := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, cur))

	updated := cur.DeepCopy()
	updated.Labels["v"] = "1"
	candidate, tmpPath, finalPath, err := si.prepareSingleWriterPayload(key, updated, "")
	require.NoError(t, err)

	rv, err := si.versioner.ObjectResourceVersion(cur)
	require.NoError(t, err)

	// Now delete the object out from under the in-flight prepared write.
	require.NoError(t, si.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))

	job := &commitJob{
		ctx:              ctx,
		key:              key,
		create:           false,
		baseRV:           rv,
		newObj:           candidate,
		newObjFactory:    newObjFactoryFor(candidate),
		tmpPayloadPath:   tmpPath,
		finalPayloadPath: finalPath,
		resultCh:         make(chan commitResult, 1),
	}
	res, err := si.ensureWriter().submit(ctx, job, priorityHigh)
	require.NoError(t, err)
	require.ErrorIs(t, res.err, errWriteConflict, "a commit racing a concurrent Delete must be rejected as a conflict, not resurrect the object")

	// The object must stay deleted.
	got := &softwarecomposition.ContainerProfile{}
	getErr := si.Get(ctx, key, storage.GetOptions{}, got)
	require.Error(t, getErr)
	require.True(t, storage.IsNotFound(getErr))
}
