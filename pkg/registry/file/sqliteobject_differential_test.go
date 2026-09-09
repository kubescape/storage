package file

// Differential suite at the storage.Interface / ContainerProfileStorage level
// (design §9): identical operation sequences against the legacy StorageImpl
// (singleWriterEnabled in its default position) and the ObjectStore, asserting
// identical observable results — returned objects, RVs, errors, LIST pages and
// continue tokens, watch events — EXCEPT the enumerated, intended divergences,
// each of which is asserted as WHAT differs, not tolerated:
//
//  1. AfterCreate crash atomicity (a TS create whose time_series write fails
//     leaves an object without a series row on the old store; nothing on the
//     new one);
//  2. TS admission after base completion (old admits a TS profile whose base
//     completed between PreSave and commit; new refuses inside the transaction);
//  3. rowid-stable pagination (old INSERT OR REPLACE moves an updated object
//     to the end of a paginated LIST; new UPDATE keeps its place);
//  4. GET creationTimestamp truncated to whole seconds on the new store;
//  5. the R4 per-TS CAS (old silently deletes a TS object updated during the
//     tick; new conflicts once, retries and merges the update);
//  6. (NOT in the design's list — found by this suite) v1beta1 spec collections
//     without omitempty decode as empty slices from JSON and as nil from gob.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// backend is one side of the differential: a storage.Interface plus the
// processor and pool behind it, and a watcher on "/" collecting every event.
type backend struct {
	name      string
	store     storage.Interface
	processor *ContainerProfileProcessor
	pool      *sqlitemigration.Pool
	wd        *WatchDispatcher
	watcher   watch.Interface
	// wrap lets a test interpose on the processor before the store is built.
	env *objectStoreEnv // nil for the legacy backend
}

type processorWrap func(inner Processor) Processor

func newLegacyBackend(t *testing.T, wrap processorWrap) *backend {
	t.Helper()
	dir := t.TempDir()
	pool := NewPoolWithOptions(filepath.Join(dir, "legacy.sq3"), PoolOptions{BusyTimeout: 5 * time.Second})
	t.Cleanup(func() { _ = pool.Close() })
	sch := runtime.NewScheme()
	install.Install(sch)
	wd := NewWatchDispatcher()
	processor := NewContainerProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxContainerProfileSize: 40000}, nil)
	processor.Interval = 0
	processor.Workers = 1
	processor.DeleteThreshold = 24 * time.Hour
	var p Processor = processor
	if wrap != nil {
		p = wrap(processor)
	}
	s := NewStorageImplWithCollector(afero.NewMemMapFs(), DefaultStorageRoot, pool, wd, sch, p)
	b := &backend{name: "legacy", store: s, processor: processor, pool: pool, wd: wd}
	b.watcher, _ = s.Watch(context.Background(), "/", storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec})
	return b
}

func newObjectBackend(t *testing.T, wrap processorWrap) *backend {
	t.Helper()
	e := newObjectStoreEnv(t)
	if wrap != nil {
		// Rebuild the store's processor view through the wrapper; the
		// ContainerProfileStorage stays wired to the real processor.
		e.store.processor = wrap(e.processor)
	}
	b := &backend{name: "objectstore", store: e.store, processor: e.processor, pool: e.pool, wd: e.wd, env: e}
	b.watcher, _ = e.store.Watch(context.Background(), "/", storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec})
	return b
}

// drainEvents collects (type, name, rv) of the events delivered so far.
func (b *backend) drainEvents(t *testing.T) []string {
	t.Helper()
	var out []string
	for {
		select {
		case ev := <-b.watcher.ResultChan():
			cp := ev.Object.(*softwarecomposition.ContainerProfile)
			out = append(out, fmt.Sprintf("%s %s rv=%s", ev.Type, cp.Name, cp.ResourceVersion))
		case <-time.After(150 * time.Millisecond):
			return out
		}
	}
}

func (b *backend) tsRows(t *testing.T, key string) int {
	t.Helper()
	conn, err := b.pool.Take(context.Background())
	require.NoError(t, err)
	defer b.pool.Put(conn)
	_, _, kind, _, ns, name := K8sPathToKeys(key)
	var n int
	require.NoError(t, sqlitex.Execute(conn, `SELECT count(*) FROM time_series WHERE kind=? AND namespace=? AND name=?`,
		&sqlitex.ExecOptions{Args: []any{NormalizeContainerProfileKind(kind), ns, name}, ResultFunc: func(stmt *sqlite.Stmt) error { n = int(stmt.ColumnInt64(0)); return nil }}))
	return n
}

func (b *backend) metaRowExists(t *testing.T, key string) bool {
	t.Helper()
	conn, err := b.pool.Take(context.Background())
	require.NoError(t, err)
	defer b.pool.Put(conn)
	return inspectRow(t, conn, key).metaExists
}

// fixtures shared by both sides
type diffFixtures struct {
	tpl     softwarecomposition.ContainerProfile
	baseNm  string
	ns      string
	baseKey string
	now     time.Time
}

func loadDiffFixtures(t *testing.T) diffFixtures {
	t.Helper()
	content, err := os.ReadFile("testdata/p1.json")
	require.NoError(t, err)
	var tpl softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(content, &tpl))
	baseNm, _ := SplitProfileName(tpl.Name)
	// Endpoint header values come out of AnalyzeEndpoints in map-iteration
	// order, so a re-merge of identical data can randomly look "changed" to the
	// #315 DeepEqual short-circuit and bump the RV — on either backend. Drop
	// them so RV sequences are deterministic.
	tpl.Spec.Endpoints = nil
	return diffFixtures{tpl: tpl, baseNm: baseNm, ns: tpl.Namespace, baseKey: testCPPrefix + tpl.Namespace + "/" + baseNm, now: time.Now().Round(0)}
}

func (f diffFixtures) ts(suffix string, n int, status, completion string) *softwarecomposition.ContainerProfile {
	p := f.tpl.DeepCopy()
	p.Name = f.baseNm + "-" + suffix
	p.ResourceVersion = ""
	p.UID = uuid.NewUUID()
	prev := "0001-01-01 00:00:00 +0000 UTC"
	if n > 1 {
		prev = f.now.Add(time.Duration(n-11) * time.Minute).String()
	}
	p.Annotations[helpersv1.ReportTimestampMetadataKey] = f.now.Add(time.Duration(n-10) * time.Minute).String()
	p.Annotations[helpersv1.PreviousReportTimestampMetadataKey] = prev
	p.Annotations[helpersv1.StatusMetadataKey] = status
	p.Annotations[helpersv1.CompletionMetadataKey] = completion
	return p
}

func (f diffFixtures) plain(name string) *softwarecomposition.ContainerProfile {
	p := f.tpl.DeepCopy()
	p.Name = name
	p.ResourceVersion = ""
	p.UID = uuid.NewUUID()
	delete(p.Annotations, helpersv1.ReportSeriesIdMetadataKey)
	return p
}

func (f diffFixtures) key(name string) string { return testCPPrefix + f.ns + "/" + name }

func (f diffFixtures) tsKey(suffix string) string { return f.baseKey + "-" + suffix }

// errClass classifies an error the way the REST layer would.
func errClass(err error) string {
	switch {
	case err == nil:
		return "nil"
	case storage.IsNotFound(err):
		return "NotFound"
	case storage.IsExist(err):
		return "KeyExists"
	case storage.IsConflict(err):
		return "Conflict"
	case errors.Is(err, ObjectCompletedError):
		return "ObjectCompleted"
	case errors.Is(err, ObjectTooLargeError):
		return "ObjectTooLarge"
	default:
		return "other:" + err.Error()
	}
}

// TestDifferential_Storage_IdenticalSequence runs the same script on both
// backends and compares every observable, pinning divergences 4 and 6.
func TestDifferential_Storage_IdenticalSequence(t *testing.T) {
	f := loadDiffFixtures(t)
	sides := []*backend{newLegacyBackend(t, nil), newObjectBackend(t, nil)}
	ctx := context.Background()

	type result struct {
		errs    []string
		rvs     []string
		objs    []*softwarecomposition.ContainerProfile // raw GET results
		lists   [][]string
		conts   []string
		events  []string
		tsRows  []int
		metaRow []bool
	}
	run := func(b *backend) result {
		var r result
		rec := func(err error) { r.errs = append(r.errs, errClass(err)) }
		out := &softwarecomposition.ContainerProfile{}
		// TS creates (three chained reports)
		for i, sfx := range []string{"r1", "r2", "r3"} {
			rec(b.store.Create(ctx, f.tsKey(sfx), f.ts(sfx, i+1, helpersv1.Learning, helpersv1.Partial), out, 0))
			r.rvs = append(r.rvs, out.ResourceVersion)
		}
		rec(b.store.Create(ctx, f.tsKey("r1"), f.ts("r1", 1, helpersv1.Learning, helpersv1.Partial), out, 0)) // KeyExists
		r.tsRows = append(r.tsRows, b.tsRows(t, f.baseKey))
		// consolidation
		rec(b.processor.ConsolidateTimeSeries(ctx))
		r.tsRows = append(r.tsRows, b.tsRows(t, f.baseKey))
		for _, sfx := range []string{"r1", "r2", "r3"} {
			r.metaRow = append(r.metaRow, b.metaRowExists(t, f.tsKey(sfx)))
		}
		got := &softwarecomposition.ContainerProfile{}
		rec(b.store.Get(ctx, f.baseKey, storage.GetOptions{}, got))
		r.objs = append(r.objs, got.DeepCopy())
		r.rvs = append(r.rvs, got.ResourceVersion)
		// second tick: no new data → no write, no RV bump
		rec(b.processor.ConsolidateTimeSeries(ctx))
		got2 := &softwarecomposition.ContainerProfile{}
		rec(b.store.Get(ctx, f.baseKey, storage.GetOptions{}, got2))
		r.rvs = append(r.rvs, got2.ResourceVersion)
		// REST update of the base, then a no-op update
		rec(b.store.GuaranteedUpdate(ctx, f.baseKey, out, false, nil, setLabel("diff", "1"), nil))
		r.rvs = append(r.rvs, out.ResourceVersion)
		rec(b.store.GuaranteedUpdate(ctx, f.baseKey, out, false, nil, identityTryUpdate, nil))
		r.rvs = append(r.rvs, out.ResourceVersion)
		// stale-RV precondition
		stale := &storage.Preconditions{ResourceVersion: strPtr("1")}
		rec(b.store.GuaranteedUpdate(ctx, f.baseKey, out, false, stale, setLabel("diff", "2"), nil))
		// update / get / delete of an absent key
		rec(b.store.GuaranteedUpdate(ctx, f.key("absent"), out, false, nil, setLabel("x", "y"), nil))
		rec(b.store.Get(ctx, f.key("absent"), storage.GetOptions{}, got))
		rec(b.store.Get(ctx, f.key("absent"), storage.GetOptions{IgnoreNotFound: true}, got))
		// plain objects for LIST
		for _, n := range []string{"pa", "pb"} {
			rec(b.store.Create(ctx, f.key(n), f.plain(n), out, 0))
		}
		for _, rv := range []string{softwarecomposition.ResourceVersionMetadata, softwarecomposition.ResourceVersionFullSpec} {
			l := &softwarecomposition.ContainerProfileList{}
			rec(b.store.GetList(ctx, testCPPrefix+f.ns, storage.ListOptions{ResourceVersion: rv, Recursive: true, Predicate: storage.SelectionPredicate{Limit: 2}}, l))
			var names []string
			for _, it := range l.Items {
				names = append(names, it.Name)
			}
			r.lists = append(r.lists, names)
			r.conts = append(r.conts, l.Continue)
			l2 := &softwarecomposition.ContainerProfileList{}
			rec(b.store.GetList(ctx, testCPPrefix+f.ns, storage.ListOptions{ResourceVersion: rv, Recursive: true, Predicate: storage.SelectionPredicate{Limit: 2, Continue: l.Continue}}, l2))
			names = nil
			for _, it := range l2.Items {
				names = append(names, it.Name)
			}
			r.lists = append(r.lists, names)
			r.conts = append(r.conts, l2.Continue)
		}
		// delete
		del := &softwarecomposition.ContainerProfile{}
		rec(b.store.Delete(ctx, f.key("pa"), del, nil, nil, nil, storage.DeleteOptions{}))
		r.rvs = append(r.rvs, del.ResourceVersion)
		rec(b.store.Get(ctx, f.key("pa"), storage.GetOptions{}, got))
		r.events = b.drainEvents(t)
		return r
	}
	old, nw := run(sides[0]), run(sides[1])

	assert.Equal(t, old.errs, nw.errs, "error classes")
	assert.Equal(t, old.rvs, nw.rvs, "resource versions")
	assert.Equal(t, old.lists, nw.lists, "LIST pages")
	// Continue tokens are rowids. Divergence 3's side effect: the one REST
	// update of the base re-inserted its row on the old store (INSERT OR
	// REPLACE), so every later rowid — and token — is exactly one higher there.
	require.Len(t, nw.conts, len(old.conts))
	for i := range old.conts {
		if old.conts[i] == "" || nw.conts[i] == "" {
			assert.Equal(t, old.conts[i], nw.conts[i], "continue token emptiness")
			continue
		}
		o, err := strconv.Atoi(old.conts[i])
		require.NoError(t, err)
		n, err := strconv.Atoi(nw.conts[i])
		require.NoError(t, err)
		assert.Equal(t, n+1, o, "old continue token is the new one plus the single re-insert")
	}
	assert.Equal(t, old.tsRows, nw.tsRows, "time_series rows")
	assert.Equal(t, old.metaRow, nw.metaRow, "processed TS objects deleted")
	assert.Equal(t, old.events, nw.events, "watch events (type, name, rv)")

	// The consolidated base: identical modulo the two codec deltas.
	require.Len(t, old.objs, 1)
	o, n := old.objs[0], nw.objs[0]
	// UIDs are generated independently per backend; compare everything else.
	n.UID, o.UID = "", ""
	delete(o.Annotations, helpersv1.SyncChecksumMetadataKey)
	delete(n.Annotations, helpersv1.SyncChecksumMetadataKey)
	// Divergence 4: sub-second creationTimestamp. Each backend stamped its
	// own metav1.Now() during its tick, so only the precision property is
	// comparable, not the instant (the two runs may straddle a second).
	assert.NotEqual(t, 0, o.CreationTimestamp.Nanosecond(), "old GET keeps the nanoseconds consolidation stamped")
	assert.Equal(t, 0, n.CreationTimestamp.Nanosecond(), "new GET truncates to whole seconds")
	assert.WithinDuration(t, o.CreationTimestamp.Time, n.CreationTimestamp.Time, 5*time.Second)
	o.CreationTimestamp, n.CreationTimestamp = metav1.Time{}, metav1.Time{}
	assert.Equal(t, canonicalCP(o), canonicalCP(n), "consolidated base differs beyond the documented codec deltas")
	// Divergence 6: nil vs empty for non-omitempty spec collections (PreSave's
	// deflate leaves Execs as an empty, non-nil slice; gob drops it, JSON keeps it).
	assert.Nil(t, o.Spec.Execs, "old: empty execs decode to nil (gob)")
	assert.NotNil(t, n.Spec.Execs, "new: empty execs decode to [] (json, no omitempty)")
	assert.Empty(t, n.Spec.Execs)
}

func strPtr(s string) *string { return &s }

// failingAfterCreate makes the legacy processor's post-commit time_series
// write fail: divergence 1's old-side injection.
type failingAfterCreate struct{ Processor }

func (f failingAfterCreate) AfterCreate(context.Context, runtime.Object) error {
	return errors.New("injected AfterCreate failure")
}

func (f failingAfterCreate) TimeSeriesRowFor(o runtime.Object) (TimeSeriesRow, string, bool) {
	return f.Processor.(TimeSeriesRowProvider).TimeSeriesRowFor(o)
}

// TestDifferential_1_AfterCreateCrashAtomicity: the old store commits the TS
// object and then fails to record its time_series row on a second connection
// (an object no consolidation tick will ever list); the new store's row joins
// the object's transaction, so the same failure leaves nothing at all.
func TestDifferential_1_AfterCreateCrashAtomicity(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()

	old := newLegacyBackend(t, func(p Processor) Processor { return failingAfterCreate{p} })
	err := old.store.Create(ctx, f.tsKey("r1"), f.ts("r1", 1, helpersv1.Learning, helpersv1.Partial), nil, 0)
	require.Error(t, err)
	assert.True(t, old.metaRowExists(t, f.tsKey("r1")), "OLD: the object is committed …")
	assert.Equal(t, 0, old.tsRows(t, f.baseKey), "OLD: … without its time_series row (the divergent state)")

	nw := newObjectBackend(t, nil)
	nw.env.store.hooks.beforeStatement = func(_ *sqlite.Conn, path string, _ int, name string) error {
		if path == holdPathCreate && name == "insert-time-series" {
			return errors.New("injected time_series failure")
		}
		return nil
	}
	err = nw.store.Create(ctx, f.tsKey("r1"), f.ts("r1", 1, helpersv1.Learning, helpersv1.Partial), nil, 0)
	require.Error(t, err)
	assert.False(t, nw.metaRowExists(t, f.tsKey("r1")), "NEW: the object is not committed either")
	assert.Equal(t, 0, nw.tsRows(t, f.baseKey))
}

// completingPreSave lets the inner PreSave admit the TS profile, then flips
// the base to Completed/Full — the "gap 1" race between PreSave's unlocked
// metadata read and the create's commit.
type completingPreSave struct {
	Processor
	flip func()
}

func (c *completingPreSave) TimeSeriesRowFor(o runtime.Object) (TimeSeriesRow, string, bool) {
	return c.Processor.(TimeSeriesRowProvider).TimeSeriesRowFor(o)
}

func (c *completingPreSave) PreSave(ctx context.Context, o runtime.Object) error {
	if err := c.Processor.PreSave(ctx, o); err != nil {
		return err
	}
	if cp, ok := o.(*softwarecomposition.ContainerProfile); ok && cp.Annotations[helpersv1.ReportSeriesIdMetadataKey] != "" && c.flip != nil {
		flip := c.flip
		c.flip = nil
		flip()
	}
	return nil
}

// TestDifferential_2_TSAdmissionAfterBaseCompletion: old admits (then reclaims
// on a later tick), new refuses inside the transaction.
func TestDifferential_2_TSAdmissionAfterBaseCompletion(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()
	completeBase := func(s storage.Interface) func() {
		return func() {
			out := &softwarecomposition.ContainerProfile{}
			err := s.GuaranteedUpdate(ctx, f.baseKey, out, false, nil, func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
				cp := in.(*softwarecomposition.ContainerProfile)
				cp.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Completed
				cp.Annotations[helpersv1.CompletionMetadataKey] = helpersv1.Full
				return cp, nil, nil
			}, nil)
			require.NoError(t, err)
		}
	}

	oldWrap := &completingPreSave{}
	old := newLegacyBackend(t, func(p Processor) Processor { oldWrap.Processor = p; return oldWrap })
	require.NoError(t, old.store.Create(ctx, f.key(f.baseNm), f.plain(f.baseNm), nil, 0)) // Learning base
	oldWrap.flip = completeBase(old.store)
	err := old.store.Create(ctx, f.tsKey("late"), f.ts("late", 1, helpersv1.Learning, helpersv1.Partial), nil, 0)
	assert.NoError(t, err, "OLD: admitted — PreSave saw a Learning base")
	assert.True(t, old.metaRowExists(t, f.tsKey("late")))
	assert.Equal(t, 1, old.tsRows(t, f.baseKey), "OLD: a time_series row for a Completed/Full base")

	nwWrap := &completingPreSave{}
	nw := newObjectBackend(t, func(p Processor) Processor { nwWrap.Processor = p; return nwWrap })
	require.NoError(t, nw.store.Create(ctx, f.key(f.baseNm), f.plain(f.baseNm), nil, 0))
	nwWrap.flip = completeBase(nw.store)
	err = nw.store.Create(ctx, f.tsKey("late"), f.ts("late", 1, helpersv1.Learning, helpersv1.Partial), nil, 0)
	assert.ErrorIs(t, err, ObjectCompletedError, "NEW: refused by the in-transaction admission check")
	assert.False(t, nw.metaRowExists(t, f.tsKey("late")))
	assert.Equal(t, 0, nw.tsRows(t, f.baseKey))
}

// TestDifferential_3_RowidStablePagination: page 1 = [a b]; update a; page 2
// is [c a] on the old store (INSERT OR REPLACE re-inserts a at a new rowid) and
// [c] on the new one (UPDATE keeps the rowid).
func TestDifferential_3_RowidStablePagination(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()
	page := func(b *backend, cont string) ([]string, string) {
		l := &softwarecomposition.ContainerProfileList{}
		require.NoError(t, b.store.GetList(ctx, testCPPrefix+f.ns, storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata, Recursive: true, Predicate: storage.SelectionPredicate{Limit: 2, Continue: cont}}, l))
		var names []string
		for _, it := range l.Items {
			names = append(names, it.Name)
		}
		return names, l.Continue
	}
	run := func(b *backend) (p1, p2 []string) {
		for _, n := range []string{"pg-a", "pg-b", "pg-c"} {
			require.NoError(t, b.store.Create(ctx, f.key(n), f.plain(n), nil, 0))
		}
		p1, cont := page(b, "")
		require.NoError(t, b.store.GuaranteedUpdate(ctx, f.key("pg-a"), &softwarecomposition.ContainerProfile{}, false, nil, setLabel("k", "v"), nil))
		p2, _ = page(b, cont)
		return p1, p2
	}
	op1, op2 := run(newLegacyBackend(t, nil))
	np1, np2 := run(newObjectBackend(t, nil))
	assert.Equal(t, []string{"pg-a", "pg-b"}, op1)
	assert.Equal(t, []string{"pg-a", "pg-b"}, np1)
	assert.Equal(t, []string{"pg-c", "pg-a"}, op2, "OLD: the updated object reappears on page 2")
	assert.Equal(t, []string{"pg-c"}, np2, "NEW: each object exactly once")
}

// TestDifferential_5_PerTSCASConflict: a TS object updated between the tick's
// reads and its deletes. Old: the update lands after the commit and the
// unconditional delete discards it. New: the staged DELETE … WHERE rv=:rv
// conflicts, the tick rolls back and retries once, merging the update.
func TestDifferential_5_PerTSCASConflict(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()
	marker := softwarecomposition.ExecCalls{Path: "/bin/injected-during-tick", Args: []string{"/bin/injected-during-tick"}}
	addExec := func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		cp := in.(*softwarecomposition.ContainerProfile)
		cp.Spec.Execs = append(cp.Spec.Execs, marker)
		return cp, nil, nil
	}
	hasMarker := func(cp *softwarecomposition.ContainerProfile) bool {
		for _, e := range cp.Spec.Execs {
			if e.Path == marker.Path {
				return true
			}
		}
		return false
	}
	run := func(b *backend) (baseHasUpdate bool, tsGone bool, conflicts int) {
		require.NoError(t, b.store.Create(ctx, f.tsKey("r1"), f.ts("r1", 1, helpersv1.Learning, helpersv1.Partial), nil, 0))
		fired := 0
		b.processor.Hooks.BeforeProcessedDeletes = func(key string) {
			fired++
			if fired > 1 {
				return // the retry must not race again
			}
			require.NoError(t, b.store.GuaranteedUpdate(ctx, f.tsKey("r1"), &softwarecomposition.ContainerProfile{}, false, nil, addExec, nil))
		}
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		base := &softwarecomposition.ContainerProfile{}
		require.NoError(t, b.store.Get(ctx, f.baseKey, storage.GetOptions{}, base))
		return hasMarker(base), !b.metaRowExists(t, f.tsKey("r1")), fired - 1
	}
	oldUpd, oldGone, oldRetries := run(newLegacyBackend(t, nil))
	assert.False(t, oldUpd, "OLD: the update is silently lost")
	assert.True(t, oldGone, "OLD: the TS object is deleted by key")
	assert.Equal(t, 0, oldRetries)

	nwUpd, nwGone, nwRetries := run(newObjectBackend(t, nil))
	assert.True(t, nwUpd, "NEW: the retried tick merged the update")
	assert.True(t, nwGone, "NEW: the TS object is deleted after the merge")
	assert.Equal(t, 1, nwRetries, "NEW: exactly one conflict → one retry")
}

// TestINV3_FrozenBaseParity: X-A (a Completed/Full base is never merged into)
// is now on origin/main (Lane 0, PR #399) as the frozen gate at the top of
// ContainerProfileProcessor.updateProfile, which reclaims a late report
// unmerged before either backend's SaveContainerProfile is reached — shared,
// backend-uniform code. This asserts the real guarantee, identically on both
// backends: a late report arriving after the base is already Completed/Full
// changes nothing (RV, spec, annotations, no Modified event) and its series
// rows are reclaimed (deleted unmerged), not left pending.
//
// This exercises only the steady-state case: the base is already frozen
// BEFORE the tick that reads it starts. It does not exercise a completer
// racing IN BETWEEN a tick's frozen-gate read and its own save — see
// TestX_A_SaveRefusesConcurrentlyFrozenBase for that window.
func TestINV3_FrozenBaseParity(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()
	type outcome struct {
		rvBefore, rvAfter string
		status, compl     string
		execs             int
		events            []string
		tsRows            int
	}
	run := func(b *backend) outcome {
		require.NoError(t, b.store.Create(ctx, f.tsKey("r1"), f.ts("r1", 1, helpersv1.Completed, helpersv1.Full), nil, 0))
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		frozen := &softwarecomposition.ContainerProfile{}
		require.NoError(t, b.store.Get(ctx, f.baseKey, storage.GetOptions{}, frozen))
		require.Equal(t, helpersv1.Completed, frozen.Annotations[helpersv1.StatusMetadataKey])
		require.Equal(t, helpersv1.Full, frozen.Annotations[helpersv1.CompletionMetadataKey])
		b.drainEvents(t)

		// A late report cannot be created through the guarded path (PreSave
		// refuses); seed its object and time_series row directly, the state a
		// create that raced ahead of consolidation leaves behind.
		late := f.ts("late", 2, helpersv1.Learning, helpersv1.Partial)
		late.Spec.Execs = append(late.Spec.Execs, softwarecomposition.ExecCalls{Path: "/bin/late"})
		// The seed row goes through the ObjectStore side's fixture handle (its
		// pool is gated: a pool connection would be an ungated writer under
		// AC-G1); the legacy side has no gate and seeds on a pool connection.
		var conn *sqlite.Conn
		put := func() {}
		if b.env != nil {
			saved := b.env.store.processor
			b.env.store.processor = DefaultProcessor{}
			require.NoError(t, b.store.Create(ctx, f.tsKey("late"), late, nil, 0))
			b.env.store.processor = saved
			conn = b.env.fixture
		} else {
			c, err := b.pool.Take(ctx)
			require.NoError(t, err)
			conn, put = c, func() { b.pool.Put(c) }
			_, err = b.store.(*StorageImpl).saveObject(conn, f.tsKey("late"), late, nil, "")
			require.NoError(t, err)
		}
		require.NoError(t, WriteTimeSeriesEntry(conn, ContainerProfileKind, f.ns, f.baseNm, late.Annotations[helpersv1.ReportSeriesIdMetadataKey], "late",
			late.Annotations[helpersv1.ReportTimestampMetadataKey], helpersv1.Learning, helpersv1.Partial, late.Annotations[helpersv1.PreviousReportTimestampMetadataKey], true))
		put()
		b.drainEvents(t) // the seeding itself dispatched on one backend only

		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		after := &softwarecomposition.ContainerProfile{}
		require.NoError(t, b.store.Get(ctx, f.baseKey, storage.GetOptions{}, after))
		return outcome{
			rvBefore: frozen.ResourceVersion, rvAfter: after.ResourceVersion,
			status: after.Annotations[helpersv1.StatusMetadataKey], compl: after.Annotations[helpersv1.CompletionMetadataKey],
			execs: len(after.Spec.Execs), events: b.drainEvents(t), tsRows: b.tsRows(t, f.baseKey),
		}
	}
	old := run(newLegacyBackend(t, nil))
	nw := run(newObjectBackend(t, nil))
	assert.Equal(t, old, nw, "frozen-base handling must be identical on both backends")
	// X-A's real guarantee, now that Lane 0 is merged: the late report changes
	// nothing. RV is untouched, no Execs merged, no Modified event, and its
	// series row was reclaimed (deleted unmerged) rather than left pending.
	for _, r := range []struct {
		name string
		o    outcome
	}{{"legacy", old}, {"objectstore", nw}} {
		assert.Equal(t, r.o.rvBefore, r.o.rvAfter, "%s: a frozen base's RV must not move", r.name)
		assert.Equal(t, helpersv1.Completed, r.o.status, "%s", r.name)
		assert.Equal(t, helpersv1.Full, r.o.compl, "%s", r.name)
		assert.Zero(t, r.o.execs, "%s: the late report's Execs must never be merged into a frozen base", r.name)
		for _, ev := range r.o.events {
			assert.NotContains(t, ev, "MODIFIED "+f.baseNm, "%s: a reclaimed-unmerged tick dispatches no Modified event on the base (a DELETED event for the reclaimed TS object is expected): %v", r.name, r.o.events)
		}
		assert.Zero(t, r.o.tsRows, "%s: the late report's series row is reclaimed (deleted unmerged), not left pending", r.name)
	}
}

// TestX_A_SaveRefusesConcurrentlyFrozenBase exercises the window
// TestINV3_FrozenBaseParity does not: a completer that lands IN BETWEEN a
// consolidation pass's own frozen-gate read (updateProfile, at the top of the
// tick) and that same pass's SaveContainerProfile call. The pass's merge was
// prepared against a base that was NOT yet Completed/Full; by the time it
// saves, another replica finished it first. X-A requires the save itself to
// refuse (ErrProfileFrozen) and leave the persisted state untouched — this is
// enforced inside SaveContainerProfile's own tryUpdate closure (re-checking
// the row's state at write time, not the pass's stale read), independently of
// the frozen gate in updateProfile, which by construction cannot see this
// race (it already ran, before the completer landed).
func TestX_A_SaveRefusesConcurrentlyFrozenBase(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()
	type outcome struct {
		refused               bool
		rvBeforeRace, rvAfter string
		status, compl         string
		events                []string
	}
	run := func(b *backend) outcome {
		// 1. One partial report: the base lands Learning, not yet Full.
		require.NoError(t, b.store.Create(ctx, f.tsKey("r1"), f.ts("r1", 1, helpersv1.Learning, helpersv1.Partial), nil, 0))
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		stale := &softwarecomposition.ContainerProfile{}
		require.NoError(t, b.store.Get(ctx, f.baseKey, storage.GetOptions{}, stale))
		require.Equal(t, helpersv1.Learning, stale.Annotations[helpersv1.StatusMetadataKey], "sanity: base must not already be frozen")
		// stale is exactly what a consolidation pass's loadOrInitializeProfile
		// would have read and merged into, at the instant before a concurrent
		// completer lands.
		stale = stale.DeepCopy()
		stale.Spec.Execs = append(stale.Spec.Execs, softwarecomposition.ExecCalls{Path: "/bin/late-merge"})

		// 2. A concurrent completer finishes the base first.
		require.NoError(t, b.store.Create(ctx, f.tsKey("r2"), f.ts("r2", 2, helpersv1.Completed, helpersv1.Full), nil, 0))
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		frozen := &softwarecomposition.ContainerProfile{}
		require.NoError(t, b.store.Get(ctx, f.baseKey, storage.GetOptions{}, frozen))
		require.Equal(t, helpersv1.Completed, frozen.Annotations[helpersv1.StatusMetadataKey])
		require.Equal(t, helpersv1.Full, frozen.Annotations[helpersv1.CompletionMetadataKey])
		b.drainEvents(t)

		// 3. The delayed pass now tries to save its stale merge directly
		// through the storage layer, bypassing updateProfile's own (already
		// stale) frozen-gate read — exactly the race window.
		err := b.processor.ContainerProfileStorage.SaveContainerProfile(ctx, f.baseKey, stale)

		after := &softwarecomposition.ContainerProfile{}
		require.NoError(t, b.store.Get(ctx, f.baseKey, storage.GetOptions{}, after))
		return outcome{
			refused:      errors.Is(err, ErrProfileFrozen),
			rvBeforeRace: frozen.ResourceVersion, rvAfter: after.ResourceVersion,
			status: after.Annotations[helpersv1.StatusMetadataKey], compl: after.Annotations[helpersv1.CompletionMetadataKey],
			events: b.drainEvents(t),
		}
	}
	old := run(newLegacyBackend(t, nil))
	nw := run(newObjectBackend(t, nil))
	assert.Equal(t, old, nw, "the race-window refusal must be identical on both backends")
	for _, r := range []struct {
		name string
		o    outcome
	}{{"legacy", old}, {"objectstore", nw}} {
		assert.True(t, r.o.refused, "%s: SaveContainerProfile must refuse a stale merge against a concurrently-completed base", r.name)
		assert.Equal(t, r.o.rvBeforeRace, r.o.rvAfter, "%s: the completer's commit must survive untouched", r.name)
		assert.Equal(t, helpersv1.Completed, r.o.status, "%s", r.name)
		assert.Equal(t, helpersv1.Full, r.o.compl, "%s", r.name)
		assert.Empty(t, r.o.events, "%s: a refused save dispatches no Modified event", r.name)
	}
}

// TestDifferential_ConsolidationGolden runs the consolidation golden windows
// of the legacy store's oracle through the ObjectStore and compares the base's
// spec and annotations after each tick with the legacy result.
func TestDifferential_ConsolidationWindows(t *testing.T) {
	f := loadDiffFixtures(t)
	ctx := context.Background()
	type snap struct {
		rv     string
		status string
		compl  string
		execs  int
		tsRows int
	}
	run := func(b *backend) []snap {
		var out []snap
		snapshot := func() {
			base := &softwarecomposition.ContainerProfile{}
			_ = b.store.Get(ctx, f.baseKey, storage.GetOptions{IgnoreNotFound: true}, base)
			out = append(out, snap{base.ResourceVersion, base.Annotations[helpersv1.StatusMetadataKey], base.Annotations[helpersv1.CompletionMetadataKey], len(base.Spec.Execs), b.tsRows(t, f.baseKey)})
		}
		// window 1: two chained learning reports
		for i, sfx := range []string{"r1", "r2"} {
			p := f.ts(sfx, i+1, helpersv1.Learning, helpersv1.Partial)
			p.Spec.Execs = append(p.Spec.Execs, softwarecomposition.ExecCalls{Path: "/bin/" + sfx})
			require.NoError(t, b.store.Create(ctx, f.tsKey(sfx), p, nil, 0))
		}
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		snapshot()
		// window 2: a gap (report 5 without 3,4) keeps it Learning
		p := f.ts("r5", 5, helpersv1.Learning, helpersv1.Partial)
		require.NoError(t, b.store.Create(ctx, f.tsKey("r5"), p, nil, 0))
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		snapshot()
		// window 3: the completing report closes the chain
		for i, sfx := range []string{"r3", "r4"} {
			require.NoError(t, b.store.Create(ctx, f.tsKey(sfx), f.ts(sfx, i+3, helpersv1.Learning, helpersv1.Partial), nil, 0))
		}
		require.NoError(t, b.store.Create(ctx, f.tsKey("r6"), f.ts("r6", 6, helpersv1.Completed, helpersv1.Full), nil, 0))
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		snapshot()
		// window 4: nothing pending
		require.NoError(t, b.processor.ConsolidateTimeSeries(ctx))
		snapshot()
		return out
	}
	old := run(newLegacyBackend(t, nil))
	nw := run(newObjectBackend(t, nil))
	assert.Equal(t, old, nw)
	assert.Equal(t, helpersv1.Completed, nw[2].status)
	assert.Equal(t, helpersv1.Full, nw[2].compl)
}

var _ = metav1.Now
