package file

// Tier A of the storage measurement harness: work budgets.
//
// TestWorkBudget runs eight single-goroutine scenarios over the hot paths of
// this package and counts, per scenario, the SQL statements executed by Go
// call site, the per-key lock acquisitions by mode, the connection-pool takes,
// the payload-file operations and the watch events. The counts are compared
// EXACTLY against testdata/workbudget.golden.json. Counts are machine
// independent, so this runs on every `go test ./...` with no env gate.
//
// A golden change is a review item: the commit that changes it states each
// delta and why, and the reviewer checks the attribution. Regenerate with
//
//	go test ./pkg/registry/file -run TestWorkBudget -update
//
// Four invariants hold regardless of the golden (see checkFixedInvariants):
// a no-op writes nothing (S2, S7); a frozen tick writes nothing to the base
// (S3); one REST read is one read lock and one connection (S5); the
// single-writer ROLLBACK recovery never fires on a passing path (all).
//
// Design: .omc/plans/raw-write-bypass-elimination.md, A.13.3.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/component-base/metrics/legacyregistry"
	"zombiezen.com/go/sqlite"
)

var updateWorkBudget = flag.Bool("update", false, "rewrite testdata/workbudget.golden.json from this run's counts")

const (
	workBudgetGoldenPath = "testdata/workbudget.golden.json"
	poolWaitMetricName   = "storage_pool_wait_duration_seconds"
	// budgetSettleWindow is how long the observers stay installed after a
	// scenario returns: any observation arriving in it is contamination.
	budgetSettleWindow = 50 * time.Millisecond
	// budgetEventDrainQuiet / budgetEventDrainMax bound the watch drain.
	budgetEventDrainQuiet = 50 * time.Millisecond
	budgetEventDrainMax   = 2 * time.Second
)

// workBudgetMu serialises budget scenarios: the two hooks and the metrics
// registry are process-global.
var workBudgetMu sync.Mutex

// frozenTickInvariant enables fixed invariant 2 (a frozen tick writes nothing
// to the base: no rename, no WriteJSON, no Modified). The property is the
// frozen gate's (X-A, kubescape/storage#399); on a tree without it the tick
// merges late reports into a Completed/Full base and rewrites it, which the
// S3 golden row records. Flip to true when #399 lands.
const frozenTickInvariant = false

type lockBudget struct {
	RLock   int `json:"rlock"`
	Lock    int `json:"lock"`
	Timeout int `json:"timeout"`
}

type fsBudget struct {
	Open   int `json:"open"`
	Rename int `json:"rename"`
	Remove int `json:"remove"`
}

type eventBudget struct {
	Added    int `json:"Added"`
	Modified int `json:"Modified"`
	Deleted  int `json:"Deleted"`
}

// workBudget is one scenario's row in the golden. Stmt holds only non-zero
// sites; an absent site is zero.
type workBudget struct {
	Stmt     map[string]int `json:"stmt"`
	Lock     lockBudget     `json:"lock"`
	PoolTake int            `json:"pool_take"`
	Fs       fsBudget       `json:"fs"`
	Events   eventBudget    `json:"events"`
}

// budgetRecorder is the sink behind every hook. It is mutex-guarded, never
// touches testing.T, and once frozen records late arrivals by name instead of
// counting them: a hook may fire from a shard goroutine, or from a goroutine
// leaked by an earlier test that loaded the observer pointer before it was
// cleared, after the scenario (or the test) has returned.
type budgetRecorder struct {
	mu     sync.Mutex
	frozen bool
	stmt   map[string]int
	lock   lockBudget
	fs     fsBudget
	late   []string
}

func newBudgetRecorder() *budgetRecorder {
	return &budgetRecorder{stmt: map[string]int{}}
}

func (r *budgetRecorder) hit(site string, bump func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		r.late = append(r.late, site)
		return
	}
	bump()
}

func (r *budgetRecorder) observeStmt(site string) {
	r.hit("stmt:"+site, func() { r.stmt[site]++ })
}

func (r *budgetRecorder) observeLock(mode, outcome string) {
	r.hit("lock:"+mode+"/"+outcome, func() {
		switch {
		case outcome == utils.LockOutcomeTimeout:
			r.lock.Timeout++
		case mode == utils.LockModeRead:
			r.lock.RLock++
		default:
			r.lock.Lock++
		}
	})
}

func (r *budgetRecorder) observeFs(op string) {
	r.hit("fs:"+op, func() {
		switch op {
		case "open":
			r.fs.Open++
		case "rename":
			r.fs.Rename++
		case "remove":
			r.fs.Remove++
		}
	})
}

// freeze stops counting and returns the counts so far.
func (r *budgetRecorder) freeze() (map[string]int, lockBudget, fsBudget) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	stmt := make(map[string]int, len(r.stmt))
	for k, v := range r.stmt {
		stmt[k] = v
	}
	return stmt, r.lock, r.fs
}

func (r *budgetRecorder) lateArrivals() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.late...)
}

// countingFs counts the payload-file operations StorageImpl issues through
// its afero.Fs. One payload read is one open; one save is one open (the
// staged temp file) plus one rename.
type countingFs struct {
	afero.Fs
	rec *budgetRecorder
}

func (c *countingFs) Open(name string) (afero.File, error) {
	c.rec.observeFs("open")
	return c.Fs.Open(name)
}

func (c *countingFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	c.rec.observeFs("open")
	return c.Fs.OpenFile(name, flag, perm)
}

func (c *countingFs) Rename(oldname, newname string) error {
	c.rec.observeFs("rename")
	return c.Fs.Rename(oldname, newname)
}

func (c *countingFs) Remove(name string) error {
	c.rec.observeFs("remove")
	return c.Fs.Remove(name)
}

// poolTakeSamples sums the sample count of storage_pool_wait_duration_seconds
// over every label set. *sqlitemigration.Pool is concrete and cannot be
// hooked, so pool takes are the delta of this histogram around a scenario;
// "observed takes" are the sites that call metrics.ObservePoolWait.
func poolTakeSamples(t *testing.T) int {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	require.NoError(t, err)
	total := 0
	for _, mf := range families {
		if mf.GetName() != poolWaitMetricName {
			continue
		}
		for _, m := range mf.GetMetric() {
			total += int(m.GetHistogram().GetSampleCount())
		}
	}
	return total
}

// budgetEnv is one scenario's freshly built storage plus its fixture keys.
type budgetEnv struct {
	t         *testing.T
	ctx       context.Context
	s         *StorageImpl
	processor *ContainerProfileProcessor
	rec       *budgetRecorder

	tpl     softwarecomposition.ContainerProfile
	baseNm  string
	ns      string
	baseKey string
	now     time.Time
}

const zeroReportTimestamp = "0001-01-01 00:00:00 +0000 UTC"

// newBudgetEnv builds the scenario storage on newLoadStorage's production
// shape (pool 10) with Workers 1 (single goroutine, deterministic), a positive
// DeleteThreshold (so the tick's expired listing executes, as in production;
// fixtures are stamped at run time so nothing is expired) and the counting
// filesystem installed before anything is written.
func newBudgetEnv(t *testing.T, rec *budgetRecorder) *budgetEnv {
	t.Helper()
	s, processor, pool := newLoadStorage(t, DefaultPoolSize)
	t.Cleanup(func() { _ = pool.Close() })
	s.appFs = &countingFs{Fs: s.appFs, rec: rec}
	processor.Workers = 1
	processor.DeleteThreshold = 24 * time.Hour

	content, err := os.ReadFile("testdata/p1.json")
	require.NoError(t, err)
	var tpl softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(content, &tpl))
	baseNm, _ := SplitProfileName(tpl.Name)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	env := &budgetEnv{
		t:         t,
		ctx:       ctx,
		s:         s,
		processor: processor,
		rec:       rec,
		tpl:       tpl,
		baseNm:    baseNm,
		ns:        tpl.Namespace,
		baseKey:   "/spdx.softwarecomposition.kubescape.io/containerprofile/" + tpl.Namespace + "/" + baseNm,
		now:       time.Now().Round(0),
	}
	// Warm the CollapseConfiguration cache (10 s TTL) so no scenario pays the
	// CR lookup: production steady state, and independent of scenario order.
	processor.CollapseSettings()
	return env
}

func (e *budgetEnv) tsKey(suffix string) string { return e.baseKey + "-" + suffix }

// reportTimestamp stamps report n of the series in the same format
// ListTimeSeriesExpired compares against (time.Time.String, local zone), so
// the lexical expiry check is consistent and nothing seeded here is expired.
func (e *budgetEnv) reportTimestamp(n int) string {
	return e.now.Add(time.Duration(n-10) * time.Minute).String()
}

// ts clones the template as report n of its series under suffix. Reports
// chain (previous = report n-1) so consolidation sees one continuous series.
func (e *budgetEnv) ts(suffix string, n int, status, completion string) *softwarecomposition.ContainerProfile {
	p := e.tpl.DeepCopy()
	p.Name = e.baseNm + "-" + suffix
	p.ResourceVersion = ""
	prev := zeroReportTimestamp
	if n > 1 {
		prev = e.reportTimestamp(n - 1)
	}
	p.Annotations[helpersv1.ReportTimestampMetadataKey] = e.reportTimestamp(n)
	p.Annotations[helpersv1.PreviousReportTimestampMetadataKey] = prev
	p.Annotations[helpersv1.StatusMetadataKey] = status
	p.Annotations[helpersv1.CompletionMetadataKey] = completion
	return p
}

// restCreate is the node-agent write: StorageImpl.Create of a TS profile.
func (e *budgetEnv) restCreate(p *softwarecomposition.ContainerProfile) {
	e.t.Helper()
	key := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + p.Namespace + "/" + p.Name
	require.NoError(e.t, e.s.Create(e.ctx, key, p, nil, 0))
}

// seedTSDirect writes a TS profile and its time_series row without PreSave's
// gate -- the shape a Create that raced ahead of consolidation leaves behind
// (a late row under a Completed/Full base cannot be created through REST).
func (e *budgetEnv) seedTSDirect(p *softwarecomposition.ContainerProfile) {
	e.t.Helper()
	conn, err := e.s.pool.Take(e.ctx)
	require.NoError(e.t, err)
	defer e.s.pool.Put(conn)
	_, suffix := SplitProfileName(p.Name)
	key := e.tsKey(suffix)
	_, err = e.s.saveObject(context.Background(), conn, key, p, nil, "", priorityLow, holdPathLegacyCommit)
	require.NoError(e.t, err)
	require.NoError(e.t, WriteTimeSeriesEntry(conn, ContainerProfileKind, p.Namespace, e.baseNm,
		p.Annotations[helpersv1.ReportSeriesIdMetadataKey], suffix,
		p.Annotations[helpersv1.ReportTimestampMetadataKey],
		p.Annotations[helpersv1.StatusMetadataKey],
		p.Annotations[helpersv1.CompletionMetadataKey],
		p.Annotations[helpersv1.PreviousReportTimestampMetadataKey], true))
}

func (e *budgetEnv) tick() {
	e.t.Helper()
	require.NoError(e.t, e.processor.ConsolidateTimeSeries(e.ctx))
}

// seedLearningBase creates the base profile the common tick operates on:
// report 1 of the series, consolidated once, leaving a Learning base.
func (e *budgetEnv) seedLearningBase() {
	e.t.Helper()
	e.restCreate(e.ts("r1", 1, helpersv1.Learning, helpersv1.Partial))
	e.tick()
}

// seedCompletedFullBase consolidates a Completed/Full report 1, leaving a
// Completed/Full base with no time_series rows.
func (e *budgetEnv) seedCompletedFullBase() {
	e.t.Helper()
	e.restCreate(e.ts("r1", 1, helpersv1.Completed, helpersv1.Full))
	e.tick()
}

func (e *budgetEnv) getBase() softwarecomposition.ContainerProfile {
	e.t.Helper()
	var out softwarecomposition.ContainerProfile
	require.NoError(e.t, e.s.Get(e.ctx, e.baseKey, storage.GetOptions{}, &out))
	return out
}

func (e *budgetEnv) withConn(fn func(conn *sqlite.Conn)) {
	e.t.Helper()
	conn, err := e.s.pool.Take(e.ctx)
	require.NoError(e.t, err)
	defer e.s.pool.Put(conn)
	fn(conn)
}

// budgetScenario is one golden row: setup runs unobserved, run is measured.
type budgetScenario struct {
	name  string
	setup func(e *budgetEnv)
	run   func(e *budgetEnv)
}

func identityUpdate(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
	return input, nil, nil
}

var budgetScenarios = []budgetScenario{
	{
		// The common tick and the per-visited-key floor: a Learning base,
		// three new reports of its one series, all with data.
		name: "S1_learning_tick",
		setup: func(e *budgetEnv) {
			e.seedLearningBase()
			for n := 2; n <= 4; n++ {
				e.restCreate(e.ts(fmt.Sprintf("r%d", n), n, helpersv1.Learning, helpersv1.Partial))
			}
		},
		run: func(e *budgetEnv) { e.tick() },
	},
	{
		// The tick's own floor: nothing pending, so no key is visited.
		name:  "S2_empty_tick",
		setup: func(e *budgetEnv) {},
		run:   func(e *budgetEnv) { e.tick() },
	},
	{
		// A Completed/Full base with two late reports that carry data.
		name: "S3_frozen_tick",
		setup: func(e *budgetEnv) {
			e.seedCompletedFullBase()
			e.seedTSDirect(e.ts("r2", 2, helpersv1.Learning, helpersv1.Partial))
			e.seedTSDirect(e.ts("r3", 3, helpersv1.Learning, helpersv1.Partial))
		},
		run: func(e *budgetEnv) { e.tick() },
	},
	{
		// Divergent base: payload Completed/Full at RV n+1, metadata row
		// Learning at RV n, plus S3's two late reports.
		name: "S4_divergent_tick",
		setup: func(e *budgetEnv) {
			e.seedLearningBase()
			var rowAtN []byte
			e.withConn(func(conn *sqlite.Conn) {
				var err error
				rowAtN, err = ReadMetadata(conn, e.baseKey)
				require.NoError(e.t, err)
			})
			require.NoError(e.t, e.s.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, false, nil,
				func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
					p := input.(*softwarecomposition.ContainerProfile)
					p.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Completed
					p.Annotations[helpersv1.CompletionMetadataKey] = helpersv1.Full
					return p, nil, nil
				}, nil))
			e.withConn(func(conn *sqlite.Conn) {
				require.NoError(e.t, WriteJSON(conn, e.baseKey, rowAtN))
			})
			e.seedTSDirect(e.ts("r2", 2, helpersv1.Learning, helpersv1.Partial))
			e.seedTSDirect(e.ts("r3", 3, helpersv1.Learning, helpersv1.Partial))
		},
		run: func(e *budgetEnv) { e.tick() },
	},
	{
		name:  "S5_rest_get",
		setup: func(e *budgetEnv) { e.seedLearningBase() },
		run:   func(e *budgetEnv) { e.getBase() },
	},
	{
		name:  "S6_rest_create_ts",
		setup: func(e *budgetEnv) { e.seedLearningBase() },
		run: func(e *budgetEnv) {
			e.restCreate(e.ts("r2", 2, helpersv1.Learning, helpersv1.Partial))
		},
	},
	{
		name:  "S7_noop_guaranteed_update",
		setup: func(e *budgetEnv) { e.seedLearningBase() },
		run: func(e *budgetEnv) {
			require.NoError(e.t, e.s.GuaranteedUpdate(e.ctx, e.baseKey, &softwarecomposition.ContainerProfile{}, true, nil, identityUpdate, nil))
		},
	},
	{
		// One processed TS profile deleted the way the consolidation pass
		// deletes it, under the worker's connection.
		name: "S8_delete_ts",
		setup: func(e *budgetEnv) {
			e.seedLearningBase()
			e.restCreate(e.ts("r2", 2, helpersv1.Learning, helpersv1.Partial))
		},
		run: func(e *budgetEnv) {
			ctx, cleanup, err := e.processor.ContainerProfileStorage.WithConnection(e.ctx)
			require.NoError(e.t, err)
			defer cleanup()
			require.NoError(e.t, e.processor.deleteProcessedTimeSeries(ctx, []string{e.tsKey("r2")}))
		},
	},
}

// runBudgetScenario builds a fresh storage, runs setup unobserved, installs
// the observers, runs the scenario, then settles: drains the watch with a
// deadline, keeps the observers installed for budgetSettleWindow, clears
// them, and takes one final locked read. Any observation after the scenario
// returned -- statement, lock, file op or pool take -- fails the test as
// `contaminated: <site>` rather than surfacing as a golden mismatch.
func runBudgetScenario(t *testing.T, sc budgetScenario) workBudget {
	t.Helper()
	workBudgetMu.Lock()
	defer workBudgetMu.Unlock()

	// Other tests flip this package var with a deferred restore; under false
	// S6/S8 take a different path entirely, which must fail by name.
	require.True(t, singleWriterEnabled, "%s: singleWriterEnabled must be true", sc.name)

	rec := newBudgetRecorder()
	env := newBudgetEnv(t, rec)
	sc.setup(env)

	w, err := env.s.Watch(env.ctx, "/", storage.ListOptions{})
	require.NoError(t, err)
	defer w.Stop()

	poolBefore := poolTakeSamples(t)
	setStmtObserver(rec.observeStmt)
	utils.SetLockObserver(rec.observeLock)
	// The counting fs was installed at construction so setup's writes go
	// through it too; counting starts when the recorder is unfrozen, which it
	// is from construction -- so reset what setup accumulated.
	rec.mu.Lock()
	rec.stmt = map[string]int{}
	rec.lock = lockBudget{}
	rec.fs = fsBudget{}
	rec.mu.Unlock()

	sc.run(env)

	stmt, lock, fs := rec.freeze()
	poolAfter := poolTakeSamples(t)
	events := drainWatchEvents(w)

	time.Sleep(budgetSettleWindow)
	setStmtObserver(nil)
	utils.SetLockObserver(nil)
	if late := rec.lateArrivals(); len(late) > 0 {
		t.Fatalf("%s: contaminated: %s (%d late observations after the scenario returned)", sc.name, late[0], len(late))
	}
	if settled := poolTakeSamples(t); settled != poolAfter {
		t.Fatalf("%s: contaminated: pool_take (moved %d -> %d after the scenario returned)", sc.name, poolAfter, settled)
	}

	for site, n := range stmt {
		if n == 0 {
			delete(stmt, site)
		}
	}
	return workBudget{
		Stmt:     stmt,
		Lock:     lock,
		PoolTake: poolAfter - poolBefore,
		Fs:       fs,
		Events:   events,
	}
}

// drainWatchEvents reads the watcher until it has been quiet for
// budgetEventDrainQuiet, or budgetEventDrainMax in total. The dispatcher's
// send is synchronous to the watcher's inCh; the receive from outCh is not.
func drainWatchEvents(w watch.Interface) eventBudget {
	var ev eventBudget
	deadline := time.NewTimer(budgetEventDrainMax)
	defer deadline.Stop()
	quiet := time.NewTimer(budgetEventDrainQuiet)
	defer quiet.Stop()
	for {
		select {
		case e, ok := <-w.ResultChan():
			if !ok {
				return ev
			}
			switch e.Type {
			case watch.Added:
				ev.Added++
			case watch.Modified:
				ev.Modified++
			case watch.Deleted:
				ev.Deleted++
			}
			if !quiet.Stop() {
				<-quiet.C
			}
			quiet.Reset(budgetEventDrainQuiet)
		case <-quiet.C:
			return ev
		case <-deadline.C:
			return ev
		}
	}
}

// checkFixedInvariants are the floors no golden update can lower.
func checkFixedInvariants(t *testing.T, name string, b workBudget) {
	t.Helper()
	// 4: the dirty-connection ROLLBACK recovery never fires on a passing path.
	require.Zero(t, b.Stmt["ROLLBACK"], "%s: invariant 4: ROLLBACK must be 0 in every scenario", name)
	switch name {
	case "S2_empty_tick", "S7_noop_guaranteed_update":
		// 1: a no-op writes nothing.
		require.Zero(t, b.Fs.Rename, "%s: invariant 1: a no-op renames nothing", name)
		require.Zero(t, b.Stmt["WriteJSON"], "%s: invariant 1: a no-op writes no metadata", name)
		require.Equal(t, eventBudget{}, b.Events, "%s: invariant 1: a no-op emits no events", name)
	case "S3_frozen_tick":
		// 2: a frozen tick writes nothing to the base.
		if frozenTickInvariant {
			require.Zero(t, b.Fs.Rename, "%s: invariant 2: a frozen tick renames nothing", name)
			require.Zero(t, b.Stmt["WriteJSON"], "%s: invariant 2: a frozen tick writes no metadata", name)
			require.Zero(t, b.Events.Modified, "%s: invariant 2: a frozen tick modifies nothing", name)
		}
	case "S5_rest_get":
		// 3: one REST read is one read lock and one connection.
		require.Equal(t, 1, b.Lock.RLock, "%s: invariant 3: one REST read is one read lock", name)
		require.Zero(t, b.Lock.Lock, "%s: invariant 3: a REST read takes no write lock", name)
		require.Equal(t, 1, b.PoolTake, "%s: invariant 3: one REST read is one connection", name)
	}
}

func TestWorkBudget(t *testing.T) {
	got := make(map[string]workBudget, len(budgetScenarios))
	for _, sc := range budgetScenarios {
		got[sc.name] = runBudgetScenario(t, sc)
		checkFixedInvariants(t, sc.name, got[sc.name])
	}

	if *updateWorkBudget {
		data, err := json.MarshalIndent(got, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(workBudgetGoldenPath), 0o755))
		require.NoError(t, os.WriteFile(workBudgetGoldenPath, append(data, '\n'), 0o644))
		t.Logf("wrote %s", workBudgetGoldenPath)
		return
	}

	data, err := os.ReadFile(workBudgetGoldenPath)
	require.NoError(t, err, "missing golden; run with -update to record it")
	var want map[string]workBudget
	require.NoError(t, json.Unmarshal(data, &want))
	if diff := diffWorkBudgets(want, got); diff != "" {
		t.Fatalf("work budget differs from %s (golden -> got); a golden change is a review item, state each delta and why:\n%s", workBudgetGoldenPath, diff)
	}
}

// diffWorkBudgets lists every field that differs, one line each, in a stable
// order. Empty means equal.
func diffWorkBudgets(want, got map[string]workBudget) string {
	var lines []string
	names := map[string]struct{}{}
	for n := range want {
		names[n] = struct{}{}
	}
	for n := range got {
		names[n] = struct{}{}
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		w, wok := want[n]
		g, gok := got[n]
		if !wok || !gok {
			lines = append(lines, fmt.Sprintf("%s: in golden=%v in run=%v", n, wok, gok))
			continue
		}
		sites := map[string]struct{}{}
		for s := range w.Stmt {
			sites[s] = struct{}{}
		}
		for s := range g.Stmt {
			sites[s] = struct{}{}
		}
		siteList := make([]string, 0, len(sites))
		for s := range sites {
			siteList = append(siteList, s)
		}
		sort.Strings(siteList)
		for _, s := range siteList {
			if w.Stmt[s] != g.Stmt[s] {
				lines = append(lines, fmt.Sprintf("%s.stmt.%s: %d -> %d", n, s, w.Stmt[s], g.Stmt[s]))
			}
		}
		cmp := func(field string, a, b int) {
			if a != b {
				lines = append(lines, fmt.Sprintf("%s.%s: %d -> %d", n, field, a, b))
			}
		}
		cmp("lock.rlock", w.Lock.RLock, g.Lock.RLock)
		cmp("lock.lock", w.Lock.Lock, g.Lock.Lock)
		cmp("lock.timeout", w.Lock.Timeout, g.Lock.Timeout)
		cmp("pool_take", w.PoolTake, g.PoolTake)
		cmp("fs.open", w.Fs.Open, g.Fs.Open)
		cmp("fs.rename", w.Fs.Rename, g.Fs.Rename)
		cmp("fs.remove", w.Fs.Remove, g.Fs.Remove)
		cmp("events.Added", w.Events.Added, g.Events.Added)
		cmp("events.Modified", w.Events.Modified, g.Events.Modified)
		cmp("events.Deleted", w.Events.Deleted, g.Events.Deleted)
	}
	return strings.Join(lines, "\n")
}
