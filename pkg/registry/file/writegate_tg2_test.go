package file

// T-G2, T-G5 and T-G6 of .omc/plans/write-gate-sharing.md: the gate's
// re-entrancy detectors, the cold-path pool bound and shutdown ordering.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/component-base/metrics/legacyregistry"
	"zombiezen.com/go/sqlite"
)

func seedOrphanSBOM(e *acg2Env, key string) {
	obj := newSBOM()
	_, _, _, _, _, name := K8sPathToKeys(key)
	obj.(metav1.Object).SetName(name)
	acg2StateByName(e.t, "orphan").seed(e, key, obj)
}

// TestTG2_ReentrantAcquireThroughThreadedCtxFailsFast: a gated fn that
// re-enters the store through the ctx it was handed (a repair on an orphan
// key, W4) is refused at O(1) on the ctx marker — under the test binary as a
// panic the outer transaction recovers into an error — instead of queuing
// behind its own holder for the whole request deadline.
func TestTG2_ReentrantAcquireThroughThreadedCtxFailsFast(t *testing.T) {
	e := newACG2OnEnv(t)
	key := sbomKey(e, "tg2-threaded")
	seedOrphanSBOM(e, key)

	start := time.Now()
	err := e.legacy.write(e.ctx, nil, priorityLow, "test", "test", false, func(ctx context.Context, _ *sqlite.Conn) error {
		return e.legacy.Get(ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	})
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-entrant", "the nested acquire must be refused on the marker, got: %v", err)
	assert.Less(t, elapsed, acg2Prompt, "the refusal is O(1), not a queue wait")
	assert.False(t, e.gate.held(), "the outer transaction released the gate")
	assert.Zero(t, e.gate.watchdogFired.Load())
}

// TestTG2_ReentrantAcquireThroughCapturedCtxIsCaughtByWatchdog: the marker
// cannot see a closure that captured the pre-ticket ctx; that nested acquire
// queues behind its own holder until the ctx expires, and the watchdog is
// what names it — the hold outlives the threshold and the holder is logged
// with every goroutine's stack.
func TestTG2_ReentrantAcquireThroughCapturedCtxIsCaughtByWatchdog(t *testing.T) {
	oldInterval, oldThreshold := gateWatchdogInterval, gateWatchdogThreshold
	gateWatchdogInterval, gateWatchdogThreshold = 10*time.Millisecond, 100*time.Millisecond
	t.Cleanup(func() { gateWatchdogInterval, gateWatchdogThreshold = oldInterval, oldThreshold })

	e := newACG2OnEnv(t)
	key := sbomKey(e, "tg2-captured")
	seedOrphanSBOM(e, key)

	const bound = 700 * time.Millisecond
	outer, cancel := context.WithTimeout(e.ctx, bound)
	defer cancel()
	start := time.Now()
	err := e.legacy.write(outer, nil, priorityLow, "test", "test", false, func(context.Context, *sqlite.Conn) error {
		// The captured ctx carries no marker: the repair queues behind us.
		return e.legacy.Get(outer, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	})
	elapsed := time.Since(start)
	// The queued repair gives up when the ctx expires; get() swallows that
	// and reports the orphan as not found, which the outer fn returns.
	assert.True(t, storage.IsNotFound(err), "got %v", err)
	assert.GreaterOrEqual(t, elapsed, bound, "the nested acquire hung until the ctx bound")
	assert.GreaterOrEqual(t, e.gate.watchdogFired.Load(), int64(1), "the watchdog must have logged the hold")
	assert.False(t, e.gate.held())
}

// TestTG5_ColdRepairsUnderSaturatedGateDoNotExhaustThePool (PM-G3): twenty
// readers each GET a distinct orphaned key while the gate is saturated by
// ContainerProfile creates. Every repair queues holding its pool connection
// (§3.3's relaxation); the bound is that this never turns into pool
// exhaustion — zero pool-wait timeouts — and every GET returns inside one
// low-lane turn (≤ highBurstLimit high commits) plus the read.
func TestTG5_ColdRepairsUnderSaturatedGateDoNotExhaustThePool(t *testing.T) {
	e := newObjectStoreEnv(t) // pool DefaultPoolSize, busy 5 s, legacy guarded
	const readers = 20
	keys := make([]string, readers)
	for i := range keys {
		keys[i] = K8sKeysToPath("", acg2Group, acg2Kind, "", "kubescape", fmt.Sprintf("tg5-%d", i))
		raw, err := json.Marshal(extractFields(&softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("tg5-%d", i), Namespace: "kubescape"}}, []string{"ObjectMeta", "SchemaVersion"}))
		require.NoError(t, err)
		require.NoError(t, WriteJSON(e.fixture, keys[i], raw))
	}

	timeoutsBefore := poolWaitTimeouts(t)
	stop := make(chan struct{})
	var creator sync.WaitGroup
	creator.Add(1)
	go func() {
		defer creator.Done()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			p := e.plain(fmt.Sprintf("sat-%d", n))
			p.UID = ""
			_ = e.store.Create(e.ctx, e.key(p.Name), p, nil, 0)
		}
	}()
	// Let the creator saturate the gate before the readers arrive.
	require.Eventually(t, func() bool { return e.gate.held() }, 5*time.Second, time.Millisecond)

	var wg sync.WaitGroup
	durations := make([]time.Duration, readers)
	errs := make([]error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			t0 := time.Now()
			errs[i] = e.legacy.Get(e.ctx, keys[i], storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
			durations[i] = time.Since(t0)
		}(i)
	}
	wg.Wait()
	close(stop)
	creator.Wait()

	assert.Equal(t, timeoutsBefore, poolWaitTimeouts(t), "pool-wait timeouts must stay at zero with the cold paths exercised")
	for i := range keys {
		assert.True(t, storage.IsNotFound(errs[i]), "reader %d: %v", i, errs[i])
		assert.Less(t, durations[i], 2*time.Second, "reader %d took %s: a queued repair, not a busy-wait", i, durations[i])
		_, err := ReadMetadata(e.fixture, keys[i])
		assert.ErrorIs(t, err, ErrMetadataNotFound, "reader %d: the orphan row was repaired", i)
	}
	var maxD time.Duration
	for _, d := range durations {
		if d > maxD {
			maxD = d
		}
	}
	t.Logf("T-G5: %d readers under a saturated gate, slowest GET %s, pool-wait timeouts %d", readers, maxD, poolWaitTimeouts(t)-timeoutsBefore)
}

// poolWaitTimeouts sums storage_pool_wait_duration_seconds{outcome="timeout"}
// over every kind.
func poolWaitTimeouts(t *testing.T) uint64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	require.NoError(t, err)
	var n uint64
	for _, mf := range families {
		if mf.GetName() != "storage_pool_wait_duration_seconds" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "outcome" && l.GetValue() == "timeout" {
					n += m.GetHistogram().GetSampleCount()
				}
			}
		}
	}
	return n
}

// TestTG6_CloseWithQueuedWritersAndCleanupMidWalk (PM-G7): a legacy commit
// queued on the high lane and a cleanup tick queued on the low lane when the
// gate closes both fail with errGateClosed, the holder finishes, Close
// returns, and the pool closes (the env's cleanup asserts Pool.Close
// returns: every connection is back).
func TestTG6_CloseWithQueuedWritersAndCleanupMidWalk(t *testing.T) {
	e := newACG2OnEnv(t)
	// The rows live in a non-default namespace: CleanupTask drops the default
	// namespace's error (`err = h.cleanupNamespace(...); return nil`,
	// cleanup.go), a pre-existing flag-off behaviour this change leaves alone.
	for _, n := range []string{"tg6-a", "tg6-b"} {
		key := K8sKeysToPath("", acg2Group, acg2Kind, "", "other", n)
		obj := &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: n, Namespace: "other"}}
		acg2StateByName(t, "present-unreferenced").seed(e, key, obj)
	}

	held := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- e.gate.run(e.ctx, priorityHigh, "tg6-holder", "test", func(context.Context, *sqlite.Conn) error {
			close(held)
			<-releaseHolder
			return nil
		})
	}()
	<-held

	createErr := make(chan error, 1)
	go func() {
		obj := newSBOM().(*softwarecomposition.SBOMSyft)
		obj.Name = "tg6-create"
		createErr <- e.legacy.Create(e.ctx, sbomKey(e, obj.Name), obj, nil, 0)
	}()
	cleanupErr := make(chan error, 1)
	go func() {
		cleanupErr <- e.cleanup.CleanupTask(e.ctx, map[string][]TypeCleanupHandlerFunc{acg2Kind: {deleteByImageId}})
	}()
	require.Eventually(t, func() bool { h, l := e.gate.queued(); return h == 1 && l == 1 }, 5*time.Second, time.Millisecond, "one writer queued in each lane")

	closed := make(chan struct{})
	go func() { _ = e.gate.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while the holder was still inside its transaction")
	case <-time.After(50 * time.Millisecond):
	}
	cErr, clErr := <-createErr, <-cleanupErr
	t.Logf("queued create: %v; queued cleanup: %v", cErr, clErr)
	assert.ErrorIs(t, cErr, errGateClosed, "the queued legacy commit")
	assert.ErrorIs(t, clErr, errGateClosed, "the mid-walk cleanup tick")
	close(releaseHolder)
	require.NoError(t, <-holderDone)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the holder released")
	}
	// Reads survive a closed gate. The aborted tick removed the first file
	// before its row delete was refused (cleanup's file-then-row order, kept
	// as today), so that row is an orphan the next tick repairs.
	assert.NoError(t, e.legacy.Get(e.ctx, K8sKeysToPath("", acg2Group, acg2Kind, "", "other", "tg6-b"), storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &softwarecomposition.SBOMSyft{}))
}

// TestStorageImpl_GateRequiresSingleWriter (RC-6): a StorageImpl sharing the
// gate refuses Create/GuaranteedUpdate while the single-writer path is off
// with a sentinel (errors.Is, not string matching), and keeps serving reads
// and deletes — the signal a test that flipped the package variable gets.
func TestStorageImpl_GateRequiresSingleWriter(t *testing.T) {
	e := newACG2OnEnv(t)
	key := sbomKey(e, "rc6")
	obj := newSBOM()
	obj.(metav1.Object).SetName("rc6")
	acg2StateByName(t, "present").seed(e, key, obj)

	old := singleWriterEnabled
	singleWriterEnabled = false
	t.Cleanup(func() { singleWriterEnabled = old })

	err := e.legacy.Create(e.ctx, sbomKey(e, "rc6-new"), &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: "rc6-new", Namespace: acg2DefaultNS}}, nil, 0)
	assert.ErrorIs(t, err, ErrGateRequiresSingleWriter)
	err = e.legacy.GuaranteedUpdate(e.ctx, key, &softwarecomposition.SBOMSyft{}, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			return input, nil, nil
		}, nil)
	assert.ErrorIs(t, err, ErrGateRequiresSingleWriter)
	assert.NoError(t, e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{}))
	assert.NoError(t, e.legacy.Delete(e.ctx, key, &softwarecomposition.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{}))
}

var _ = errors.Is
var _ = helpersv1.ImageIDMetadataKey
