package file

// Same-key concurrency on the ObjectStore write path (§3.4 GuaranteedUpdate's
// CAS + singleWriterConflictBackoff retry, §3.7's consolidation write set).
// The legacy StorageImpl has TestSingleWriter_ConcurrentUpdatesSameKey_
// NoLostUpdates; under config.ContainerProfileSqliteBackend that path is
// bypassed entirely, so these run the same shapes against the ObjectStore.
//
// Every test asserts the conflict counter moved: a run in which the CAS never
// failed would pass the no-lost-update check without exercising the retry
// path at all, which is the silent short-circuit these tests exist to catch.

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// casConflicts snapshots storage_cp_cas_conflict_total by op.
type casConflicts struct{ update, delete float64 }

func snapshotCASConflicts(t *testing.T) casConflicts {
	t.Helper()
	return casConflicts{
		update: counterValue(t, metrics.CPCASConflictTotal.WithLabelValues("update")),
		delete: counterValue(t, metrics.CPCASConflictTotal.WithLabelValues("delete")),
	}
}

func (c casConflicts) delta(t *testing.T) casConflicts {
	t.Helper()
	n := snapshotCASConflicts(t)
	return casConflicts{update: n.update - c.update, delete: n.delete - c.delete}
}

// incrementCounter is the tryUpdate every writer runs: read the label, add
// one. Two writers that both prepared from the same row produce the same
// value; the CAS must let exactly one of them through.
func incrementCounter(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
	cp := input.(*softwarecomposition.ContainerProfile).DeepCopy()
	n, _ := strconv.Atoi(cp.Labels["counter"])
	cp.Labels["counter"] = strconv.Itoa(n + 1)
	return cp, nil, nil
}

// isContentionTimeout reports whether err is what newContentionTimeoutError
// returns (a ServerTimeout, or the InternalError of a cancelled ctx).
func isContentionTimeout(err error) bool {
	return err != nil && (apierrors.IsServerTimeout(err) || apierrors.IsInternalError(err))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(float64(len(sorted)-1) * p)
	return sorted[i]
}

// runSameKeyWriters launches n GuaranteedUpdate calls on key, released
// together by a start barrier so they all read the same row version, and
// returns the per-call latencies (sorted) and errors.
func runSameKeyWriters(e *objectStoreEnv, key string, n int) ([]time.Duration, []error) {
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	lat := make([]time.Duration, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			t0 := time.Now()
			errs[i] = e.store.GuaranteedUpdate(e.ctx, key, &softwarecomposition.ContainerProfile{}, false, nil, incrementCounter, nil)
			lat[i] = time.Since(t0)
		}(i)
	}
	close(start)
	wg.Wait()
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	return lat, errs
}

// TestObjectStore_ConcurrentUpdatesSameKey_NoLostUpdates is the ObjectStore
// port of TestSingleWriter_ConcurrentUpdatesSameKey_NoLostUpdates: 40 writers
// on one key, every one must land exactly once.
func TestObjectStore_ConcurrentUpdatesSameKey_NoLostUpdates(t *testing.T) {
	// A pool larger than the writer count for the same reason the legacy test
	// gives: guaranteedUpdate holds its pool connection across the gate wait
	// (the re-read on conflict needs it), so with the default pool of 10 most
	// of 40 simultaneous writers would first queue on pool.Take, and the
	// property under test is the CAS, not pool sizing.
	e := newObjectStoreEnv(t, withPoolSize(64))
	const n = 40
	key := e.key("concurrent-same-key")
	p := e.plain("concurrent-same-key")
	p.Labels["counter"] = "0"
	e.create(p)

	before := snapshotCASConflicts(t)
	lat, errs := runSameKeyWriters(e, key, n)
	conflicts := before.delta(t)

	for i, err := range errs {
		require.NoError(t, err, "update %d failed", i)
		require.False(t, isContentionTimeout(err))
	}
	got := e.mustGet(key)
	require.Equal(t, strconv.Itoa(n), got.Labels["counter"], "no update lost or double-applied")
	require.Equal(t, strconv.Itoa(n+1), got.ResourceVersion, "1 create + n updates")
	e.withFixture(func(conn *sqlite.Conn) { assertINV2(t, conn, key) })
	require.Greater(t, conflicts.update, 0.0, "storage_cp_cas_conflict_total{op=update} did not move: the CAS was never contended, so the retry path was not exercised")
	require.False(t, e.gate.held(), "the gate is still held after every writer returned")
	maxLat := lat[len(lat)-1]
	require.Less(t, maxLat, 10*time.Second, "max latency %s is not well under the 60s ctx deadline", maxLat)
	t.Logf("n=%d conflicts(update)=%.0f latency p50=%s p99=%s max=%s", n, conflicts.update, percentile(lat, 0.5), percentile(lat, 0.99), maxLat)
}

// TestObjectStore_ConcurrentUpdatesSameKey_Stress is the LOAD_TEST=1 variant:
// 200 writers, reporting p99 and the conflict count.
func TestObjectStore_ConcurrentUpdatesSameKey_Stress(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "1" {
		t.Skip("set LOAD_TEST=1 to run the 200-writer same-key stress")
	}
	e := newObjectStoreEnv(t, withPoolSize(256))
	const n = 200
	key := e.key("stress-same-key")
	p := e.plain("stress-same-key")
	p.Labels["counter"] = "0"
	e.create(p)

	before := snapshotCASConflicts(t)
	retriesBefore := counterValue(t, metrics.SingleWriterConflictRetryTotal.WithLabelValues(ContainerProfileKindPlural))
	t0 := time.Now()
	lat, errs := runSameKeyWriters(e, key, n)
	wall := time.Since(t0)
	conflicts := before.delta(t)
	retries := counterValue(t, metrics.SingleWriterConflictRetryTotal.WithLabelValues(ContainerProfileKindPlural)) - retriesBefore

	timeouts := 0
	for i, err := range errs {
		if isContentionTimeout(err) {
			timeouts++
		}
		require.NoError(t, err, "update %d failed", i)
	}
	got := e.mustGet(key)
	require.Equal(t, strconv.Itoa(n), got.Labels["counter"])
	require.Equal(t, strconv.Itoa(n+1), got.ResourceVersion)
	e.withFixture(func(conn *sqlite.Conn) { assertINV2(t, conn, key) })
	require.Greater(t, conflicts.update, 0.0)
	require.False(t, e.gate.held())
	require.Equal(t, 0, timeouts)
	t.Logf("n=%d wall=%s conflicts(update)=%.0f retries=%.0f timeouts=%d latency p50=%s p90=%s p99=%s max=%s",
		n, wall, conflicts.update, retries, timeouts, percentile(lat, 0.5), percentile(lat, 0.9), percentile(lat, 0.99), lat[len(lat)-1])
}

// pendingTSRows counts key's time_series rows a tick would still process
// (hasData=1); a consolidated report's row stays behind with hasData=0.
func (e *objectStoreEnv) pendingTSRows(key string) int {
	e.t.Helper()
	_, _, kind, _, ns, name := K8sPathToKeys(key)
	var n int
	e.withFixture(func(conn *sqlite.Conn) {
		require.NoError(e.t, sqlitex.Execute(conn,
			`SELECT count(*) FROM time_series WHERE kind = ? AND namespace = ? AND name = ? AND hasData = 1`,
			&sqlitex.ExecOptions{Args: []any{NormalizeContainerProfileKind(kind), ns, name}, ResultFunc: func(stmt *sqlite.Stmt) error { n = int(stmt.ColumnInt64(0)); return nil }}))
	})
	return n
}

// tickRace drives the §9 "same-key REST-Update-vs-consolidation race": ticks
// consolidating pending reports of one series while writers mutate the same
// key. One committed tick merges every pending row, so the race runs in
// rounds: seed perRound reports, tick until none is pending (bounded), repeat.
//
// injectOnce, when set, runs on the tick goroutine from the processor's
// BeforeProcessedDeletes hook — after the pass's Phase 1 reads and before its
// staged commit, outside the gate — on the FIRST attempt of every tick only:
// a deterministic write in the window the CAS exists for. The pass's retry
// (N=2) then re-reads and must commit.
type tickRace struct {
	e                *objectStoreEnv
	rounds, perRound int
	maxTicksPerRound int
	injectOnce       func(tick, newest int)

	reports    int // newest report number created
	ticks      int
	worstRound int
	tickErrs   []error
	injected   int
}

func (r *tickRace) run(t *testing.T) {
	t.Helper()
	e := r.e
	var firedThisTick bool
	e.processor.Hooks.BeforeProcessedDeletes = func(string) {
		if r.injectOnce == nil || firedThisTick {
			return
		}
		firedThisTick = true
		r.injected++
		r.injectOnce(r.ticks, r.reports)
	}
	t.Cleanup(func() { e.processor.Hooks.BeforeProcessedDeletes = nil })

	for round := 1; round <= r.rounds; round++ {
		for i := 0; i < r.perRound; i++ {
			r.reports++
			e.createReport(r.reports)
		}
		roundTicks := 0
		for e.pendingTSRows(e.baseKey) > 0 {
			require.Less(t, roundTicks, r.maxTicksPerRound,
				"round %d: %d pending time_series rows did not drain in %d ticks (livelock); tick errors so far: %v",
				round, e.pendingTSRows(e.baseKey), r.maxTicksPerRound, r.tickErrs)
			roundTicks++
			r.ticks++
			firedThisTick = false
			if err := e.processor.ConsolidateTimeSeries(e.ctx); err != nil {
				require.ErrorIs(t, err, ErrWriteConflict, "a tick failed for something other than a CAS conflict")
				r.tickErrs = append(r.tickErrs, err)
			}
		}
		r.worstRound = max(r.worstRound, roundTicks)
	}
}

// seedLearningBase materialises the base from report 1 (consolidated once)
// and stamps counter=0 on it.
func (e *objectStoreEnv) seedLearningBase() {
	e.t.Helper()
	e.create(e.ts("r1", 1, helpersv1.Learning, helpersv1.Partial))
	e.tick()
	require.Equal(e.t, 0, e.pendingTSRows(e.baseKey), "a consolidated series keeps its rows with hasData=0; none may be pending")
	require.Equal(e.t, helpersv1.Learning, e.mustGet(e.baseKey).Annotations[helpersv1.StatusMetadataKey])
	require.NoError(e.t, e.store.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, false, nil, setLabel("counter", "0"), nil))
}

// assertTickRaceOutcome is the common post-condition: the base carries every
// counter increment (no writer's update was lost to a tick's commit), every
// report was merged and deleted, INV-2 holds on every key, the gate is free.
func assertTickRaceOutcome(t *testing.T, e *objectStoreEnv, reports int, wantCounter int64) {
	t.Helper()
	final := e.mustGet(e.baseKey)
	require.Equal(t, strconv.FormatInt(wantCounter, 10), final.Labels["counter"], "the base lost a writer's update to a consolidation commit")
	for n := 1; n <= reports; n++ {
		_, err := e.get(e.tsKey(fmt.Sprintf("r%d", n)))
		require.True(t, storage.IsNotFound(err), "report r%d survived consolidation", n)
		if n > 1 {
			require.True(t, hasExec(final, reportExec(n)), "report r%d was deleted without its observation reaching the base", n)
		}
	}
	e.withFixture(func(conn *sqlite.Conn) {
		for _, k := range allCPKeys(t, conn) {
			assertINV2(t, conn, k)
		}
	})
	require.False(t, e.gate.held())
}

func reportExec(n int) string { return fmt.Sprintf("/bin/report-%d", n) }

// createReport creates report n of the series. Every report carries one
// observation of its own, so every tick's merge changes the base and stages
// the save-base CAS (identical reports merge to a no-op: no write, nothing
// to race).
func (e *objectStoreEnv) createReport(n int) {
	e.t.Helper()
	p := e.ts(fmt.Sprintf("r%d", n), n, helpersv1.Learning, helpersv1.Partial)
	p.Spec.Execs = append(p.Spec.Execs, softwarecomposition.ExecCalls{Path: reportExec(n)})
	e.create(p)
}

// measureTickWindow consolidates reports first+1..first+n one at a time and
// returns the slowest tick: the Phase 1 read → merge → encode → gate →
// commit window a same-key writer has to miss, on this machine and build
// (the race detector stretches it several-fold).
func (e *objectStoreEnv) measureTickWindow(first, n int) time.Duration {
	e.t.Helper()
	var worst time.Duration
	for i := 1; i <= n; i++ {
		e.createReport(first + i)
		t0 := time.Now()
		e.tick()
		worst = max(worst, time.Since(t0))
	}
	require.Equal(e.t, 0, e.pendingTSRows(e.baseKey))
	return worst
}

func hasExec(cp *softwarecomposition.ContainerProfile, path string) bool {
	for _, x := range cp.Spec.Execs {
		if x.Path == path {
			return true
		}
	}
	return false
}

// TestObjectStore_UpdateVsConsolidationTick_SameBaseKey is the §9 "same-key
// REST-Update-vs-consolidation race" on the ObjectStore: one commits, the
// other conflicts once, no update is lost, and the rows still drain.
func TestObjectStore_UpdateVsConsolidationTick_SameBaseKey(t *testing.T) {
	const rounds, perRound = 8, 3

	// Deterministic: a base update lands between the tick's Phase 1 read and
	// its commit, every tick. The save-base CAS fails (op=update), the retry
	// re-reads the base — with the update — and commits: one conflict per
	// tick, one tick per round, the update in the merged base.
	t.Run("base update between Phase 1 and commit", func(t *testing.T) {
		e := newObjectStoreEnv(t)
		e.seedLearningBase()
		before := snapshotCASConflicts(t)
		race := &tickRace{e: e, rounds: rounds, perRound: perRound, maxTicksPerRound: 25}
		race.injectOnce = func(tick, _ int) {
			err := e.store.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, false, nil,
				func(input runtime.Object, m storage.ResponseMeta) (runtime.Object, *uint64, error) {
					out, _, err := incrementCounter(input, m)
					cp := out.(*softwarecomposition.ContainerProfile)
					cp.Spec.Execs = append(cp.Spec.Execs, softwarecomposition.ExecCalls{Path: fmt.Sprintf("/bin/base-update-%d", tick)})
					return cp, nil, err
				}, nil)
			if err != nil {
				t.Errorf("injected base update (tick %d): %v", tick, err)
			}
		}
		race.run(t)
		conflicts := before.delta(t)

		require.Equal(t, rounds, race.ticks, "every round drains in exactly one tick: the retry commits")
		require.Equal(t, rounds, race.injected)
		require.Equal(t, float64(rounds), conflicts.update, "exactly one save-base CAS conflict per tick")
		require.Equal(t, 0.0, conflicts.delete)
		require.Empty(t, race.tickErrs, "no tick failed: the once-retry absorbed every conflict")
		assertTickRaceOutcome(t, e, race.reports, int64(rounds))
		final := e.mustGet(e.baseKey)
		for tick := 1; tick <= rounds; tick++ {
			require.True(t, hasExec(final, fmt.Sprintf("/bin/base-update-%d", tick)), "the base update of tick %d survived the retried merge", tick)
		}
		t.Logf("rounds=%d ticks=%d conflicts(update)=%.0f conflicts(delete)=%.0f", rounds, race.ticks, conflicts.update, conflicts.delete)
	})

	// Deterministic, R4: the newest pending report is updated in the same
	// window. The pass's staged delete carries the (rv, uid) it read the
	// report with; it matches no row (op=delete), the tick rolls back, the
	// retry merges the updated report.
	t.Run("TS update between Phase 1 and commit (R4)", func(t *testing.T) {
		e := newObjectStoreEnv(t)
		e.seedLearningBase()
		before := snapshotCASConflicts(t)
		race := &tickRace{e: e, rounds: rounds, perRound: perRound, maxTicksPerRound: 25}
		race.injectOnce = func(tick, newest int) {
			err := e.store.GuaranteedUpdate(e.ctx, e.tsKey(fmt.Sprintf("r%d", newest)), &softwarecomposition.ContainerProfile{}, false, nil,
				func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
					cp := input.(*softwarecomposition.ContainerProfile).DeepCopy()
					cp.Spec.Execs = append(cp.Spec.Execs, softwarecomposition.ExecCalls{Path: fmt.Sprintf("/bin/ts-update-%d", tick)})
					return cp, nil, nil
				}, nil)
			if err != nil {
				t.Errorf("injected report update (tick %d): %v", tick, err)
			}
		}
		race.run(t)
		conflicts := before.delta(t)

		require.Equal(t, rounds, race.ticks)
		require.Equal(t, float64(rounds), conflicts.delete, "exactly one per-TS delete CAS conflict per tick")
		require.Equal(t, 0.0, conflicts.update)
		require.Empty(t, race.tickErrs)
		assertTickRaceOutcome(t, e, race.reports, 0)
		final := e.mustGet(e.baseKey)
		for tick := 1; tick <= rounds; tick++ {
			require.True(t, hasExec(final, fmt.Sprintf("/bin/ts-update-%d", tick)), "the report update of tick %d was merged by the retry, not deleted unmerged", tick)
		}
		t.Logf("rounds=%d ticks=%d conflicts(update)=%.0f conflicts(delete)=%.0f", rounds, race.ticks, conflicts.update, conflicts.delete)
	})

	// Stochastic overlay: paced background writers on the base and on the
	// newest report, plus the deterministic base injection, so conflicts of
	// both kinds and double conflicts (a failed tick) can happen; the rows
	// must still drain in a bounded number of ticks and no update is lost.
	// The writers' gap is 4× the tick window measured on this build: a
	// writer whose gap is shorter than the window starves the pass (see the
	// unpaced diagnostic), and a fixed wall-clock gap crosses that line
	// under the race detector. Production producers of base updates are
	// orders of magnitude sparser than either.
	t.Run("paced background writers", func(t *testing.T) {
		e := newObjectStoreEnv(t, withPoolSize(16))
		e.seedLearningBase()
		const warmup = 3
		window := e.measureTickWindow(1, warmup)
		pace := max(time.Millisecond, 4*window)
		before := snapshotCASConflicts(t)
		stop := make(chan struct{})
		var stopOnce sync.Once
		var bgBaseUpdates, bgTSUpdates atomic.Int64
		var newest atomic.Int64
		newest.Store(1 + warmup)
		var wg sync.WaitGroup
		wg.Add(2)
		t.Cleanup(func() { stopOnce.Do(func() { close(stop) }); wg.Wait() })
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(pace):
				}
				if err := e.store.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, false, nil, incrementCounter, nil); err != nil {
					t.Errorf("background base update: %v", err)
					return
				}
				bgBaseUpdates.Add(1)
			}
		}()
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				case <-time.After(pace):
				}
				// ignoreNotFound + an unchanged object for an absent key (already
				// merged and deleted) is a logged-nothing no-op.
				out := &softwarecomposition.ContainerProfile{}
				err := e.store.GuaranteedUpdate(e.ctx, e.tsKey(fmt.Sprintf("r%d", newest.Load())), out, true, nil,
					func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						cp := input.(*softwarecomposition.ContainerProfile)
						if cp.Name == "" {
							return cp, nil, nil
						}
						cp = cp.DeepCopy()
						cp.Labels["ts-touch"] = strconv.FormatInt(time.Now().UnixNano(), 10)
						return cp, nil, nil
					}, nil)
				if err != nil {
					t.Errorf("background report update: %v", err)
					return
				}
				if out.Name != "" {
					bgTSUpdates.Add(1)
				}
			}
		}()

		race := &tickRace{e: e, rounds: rounds, perRound: perRound, maxTicksPerRound: 25, reports: 1 + warmup}
		var injectedUpdates int64
		race.injectOnce = func(tick, n int) {
			newest.Store(int64(n))
			if err := e.store.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, false, nil, incrementCounter, nil); err != nil {
				t.Errorf("injected base update (tick %d): %v", tick, err)
				return
			}
			injectedUpdates++
		}
		race.run(t)
		stopOnce.Do(func() { close(stop) })
		wg.Wait()
		conflicts := before.delta(t)

		require.GreaterOrEqual(t, conflicts.update, float64(rounds), "at least the injected conflict of every round's first tick")
		require.Greater(t, bgBaseUpdates.Load(), int64(0))
		assertTickRaceOutcome(t, e, race.reports, injectedUpdates+bgBaseUpdates.Load())
		t.Logf("tickWindow=%s pace=%s rounds=%d ticks=%d worstRoundTicks=%d failedTicks=%d bgBaseUpdates=%d bgTSUpdates=%d conflicts(update)=%.0f conflicts(delete)=%.0f",
			window, pace, rounds, race.ticks, race.worstRound, len(race.tickErrs), bgBaseUpdates.Load(), bgTSUpdates.Load(), conflicts.update, conflicts.delete)
	})
}

// TestObjectStore_UpdateVsConsolidationTick_UnpacedWriterDiagnostic is the
// LOAD_TEST=1 diagnostic of the case the test above deliberately paces
// around: a writer committing base updates back-to-back (no gap; ~0.6 ms per
// commit here). The tick's Phase 1 read → merge → encode → gate → commit
// window is 1–3 ms, the pass retries once and then yields to the next tick,
// and the writer holds the gate's high lane, so the pass rarely finds the row
// where it read it: measured 30/30 ticks conflicting in most runs, a drain
// after a dozen failed ticks in the rest. Nothing is lost or corrupted
// (asserted); the series simply does not consolidate while the writer keeps
// that pace. Logged, not asserted: whether the ObjectStore must guarantee
// progress against such a writer is a design decision, not a property this
// branch claims.
func TestObjectStore_UpdateVsConsolidationTick_UnpacedWriterDiagnostic(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "1" {
		t.Skip("set LOAD_TEST=1 to run the unpaced same-key writer vs tick diagnostic")
	}
	e := newObjectStoreEnv(t, withPoolSize(16))
	e.seedLearningBase()
	before := snapshotCASConflicts(t)
	stop := make(chan struct{})
	var stopOnce sync.Once
	var bgUpdates atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	t.Cleanup(func() { stopOnce.Do(func() { close(stop) }); wg.Wait() })
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := e.store.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, false, nil, incrementCounter, nil); err != nil {
				t.Errorf("background base update: %v", err)
				return
			}
			bgUpdates.Add(1)
		}
	}()
	const reports, maxTicks = 3, 30
	for n := 2; n <= reports+1; n++ {
		e.create(e.ts(fmt.Sprintf("r%d", n), n, helpersv1.Learning, helpersv1.Partial))
	}
	ticks, failed := 0, 0
	for ticks < maxTicks && e.pendingTSRows(e.baseKey) > 0 {
		ticks++
		if err := e.processor.ConsolidateTimeSeries(e.ctx); err != nil {
			require.ErrorIs(t, err, ErrWriteConflict)
			failed++
		}
	}
	stopOnce.Do(func() { close(stop) })
	wg.Wait()
	conflicts := before.delta(t)
	pending := e.pendingTSRows(e.baseKey)

	require.Equal(t, strconv.FormatInt(bgUpdates.Load(), 10), e.mustGet(e.baseKey).Labels["counter"], "no lost update")
	e.withFixture(func(conn *sqlite.Conn) {
		for _, k := range allCPKeys(t, conn) {
			assertINV2(t, conn, k)
		}
	})
	require.False(t, e.gate.held())
	if pending > 0 {
		t.Logf("STARVED: %d of %d reports still pending after %d ticks (%d failed) against %d back-to-back base updates; conflicts(update)=%.0f",
			pending, reports, ticks, failed, bgUpdates.Load(), conflicts.update)
	} else {
		t.Logf("drained in %d ticks (%d failed) against %d back-to-back base updates; conflicts(update)=%.0f", ticks, failed, bgUpdates.Load(), conflicts.update)
	}
}
