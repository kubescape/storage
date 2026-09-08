package file

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// Lane 0 (L0-A) test harness: a real SQLite pool, a real StorageImpl and a
// real ContainerProfileProcessor, plus a hook-able ContainerProfileStorage
// decorator so a test can observe or perturb one call on the consolidation path.

const lane0Group = "spdx.softwarecomposition.kubescape.io"

type lane0Harness struct {
	proc *ContainerProfileProcessor
	s    *StorageImpl
	pool *sqlitemigration.Pool
	// hooks wraps the real ContainerProfileStorage; tests set its fields.
	hooks *hookedCPStorage
}

// newLane0Harness builds the harness. busyTimeout <= 0 keeps DefaultBusyTimeout.
func newLane0Harness(t *testing.T, busyTimeout time.Duration) *lane0Harness {
	t.Helper()
	pool := NewPool(filepath.Join(t.TempDir(), "test.sq3"), 0, busyTimeout)
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	proc := &ContainerProfileProcessor{
		DeleteThreshold:         time.Hour,
		MaxContainerProfileSize: 40000,
		Workers:                 1,
	}
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       proc,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	hooks := &hookedCPStorage{ContainerProfileStorage: NewContainerProfileStorageImpl(s, pool)}
	// Interval stays 0 so SetStorage does not spawn the maintenance goroutine.
	proc.SetStorage(hooks)
	return &lane0Harness{proc: proc, s: s, pool: pool, hooks: hooks}
}

func lane0Key(ns, name string) string {
	return K8sKeysToPath("", lane0Group, "containerprofile", "", ns, name)
}

// hookedCPStorage forwards every ContainerProfileStorage call to the wrapped
// implementation; a non-nil hook is called instead and receives `next`, the
// forwarding call, so it can observe, decorate or replace the result.
type hookedCPStorage struct {
	ContainerProfileStorage
	getProfile func(ctx context.Context, key string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error)
	getTs      func(ctx context.Context, key string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error)
	listTs     func(ctx context.Context, key string, next func() (map[string][]softwarecomposition.TimeSeriesContainers, error)) (map[string][]softwarecomposition.TimeSeriesContainers, error)
	replace    func(ctx context.Context, key, seriesID string, del []string, ins []softwarecomposition.TimeSeriesContainers, next func() error) error
	save       func(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile, next func() error) error
}

func (h *hookedCPStorage) GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	next := func() (softwarecomposition.ContainerProfile, error) {
		return h.ContainerProfileStorage.GetContainerProfile(ctx, key)
	}
	if h.getProfile != nil {
		return h.getProfile(ctx, key, next)
	}
	return next()
}

func (h *hookedCPStorage) GetTsContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	next := func() (softwarecomposition.ContainerProfile, error) {
		return h.ContainerProfileStorage.GetTsContainerProfile(ctx, key)
	}
	if h.getTs != nil {
		return h.getTs(ctx, key, next)
	}
	return next()
}

func (h *hookedCPStorage) ListTimeSeriesContainers(ctx context.Context, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	next := func() (map[string][]softwarecomposition.TimeSeriesContainers, error) {
		return h.ContainerProfileStorage.ListTimeSeriesContainers(ctx, key)
	}
	if h.listTs != nil {
		return h.listTs(ctx, key, next)
	}
	return next()
}

func (h *hookedCPStorage) ReplaceTimeSeriesContainerEntries(ctx context.Context, key, seriesID string, del []string, ins []softwarecomposition.TimeSeriesContainers) error {
	next := func() error {
		return h.ContainerProfileStorage.ReplaceTimeSeriesContainerEntries(ctx, key, seriesID, del, ins)
	}
	if h.replace != nil {
		return h.replace(ctx, key, seriesID, del, ins, next)
	}
	return next()
}

func (h *hookedCPStorage) SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error {
	next := func() error { return h.ContainerProfileStorage.SaveContainerProfile(ctx, key, profile) }
	if h.save != nil {
		return h.save(ctx, key, profile, next)
	}
	return next()
}

// lane0Spec is a distinctive Spec fragment that survives DeflateContainerProfileSpec unchanged.
func lane0Spec(tag string) softwarecomposition.ContainerProfileSpec {
	return softwarecomposition.ContainerProfileSpec{
		Execs: []softwarecomposition.ExecCalls{{Path: "/bin/" + tag, Args: []string{tag}}},
	}
}

const lane0InstanceID = "apiVersion-apps/v1/namespace-ns1/kind-Deployment/name-app"

// newBaseProfile builds a base (consolidated) ContainerProfile; annotations
// carry status/completion plus an InstanceID so updateProfile's gate 1 passes.
func newBaseProfile(ns, name, status, completion, tag string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey:     status,
				helpersv1.CompletionMetadataKey: completion,
				helpersv1.InstanceIDMetadataKey: lane0InstanceID,
			},
		},
		Spec: lane0Spec(tag),
	}
}

// createBase persists a base profile through the REST Create path (PreSave runs).
func (h *lane0Harness) createBase(t *testing.T, key string, p *softwarecomposition.ContainerProfile) {
	t.Helper()
	require.NoError(t, h.s.Create(context.Background(), key, p, &softwarecomposition.ContainerProfile{}, 0))
}

// seedTsRow writes one time_series row for (ns, name).
func (h *lane0Harness) seedTsRow(t *testing.T, ns, name, seriesID, suffix, report, prev, status, completion string, hasData bool) {
	t.Helper()
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	require.NoError(t, WriteTimeSeriesEntry(conn, "containerprofile", ns, name, seriesID, suffix, report, status, completion, prev, hasData))
}

// writeTsObject persists a TS ContainerProfile object at baseKey+"-"+suffix
// directly through saveObject (no PreSave admission, no time_series row), so a
// test controls the row and the object independently. withInstanceID=false
// builds the finding-T shape (a merge that cannot yield a slug).
func (h *lane0Harness) writeTsObject(t *testing.T, baseKey, suffix, tag string, withInstanceID bool) string {
	t.Helper()
	_, _, _, _, ns, name := K8sPathToKeys(baseKey)
	tsKey := baseKey + "-" + suffix
	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name + "-" + suffix,
			Namespace:   ns,
			Annotations: map[string]string{helpersv1.ReportSeriesIdMetadataKey: "series"},
		},
		Spec: lane0Spec(tag),
	}
	if withInstanceID {
		obj.Annotations[helpersv1.InstanceIDMetadataKey] = lane0InstanceID
	}
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	_, err = h.s.saveObject(conn, tsKey, obj, &softwarecomposition.ContainerProfile{}, "")
	require.NoError(t, err)
	return tsKey
}

func (h *lane0Harness) objectExists(t *testing.T, key string) bool {
	t.Helper()
	err := h.s.Get(context.Background(), key, storage.GetOptions{}, &softwarecomposition.ContainerProfile{})
	if err == nil {
		return true
	}
	require.True(t, storage.IsNotFound(err), "unexpected error reading %s: %v", key, err)
	return false
}

func (h *lane0Harness) readPayload(t *testing.T, key string) softwarecomposition.ContainerProfile {
	t.Helper()
	var p softwarecomposition.ContainerProfile
	require.NoError(t, h.s.Get(context.Background(), key, storage.GetOptions{}, &p))
	return p
}

// readRow decodes the metadata row (what LIST, WATCH and PreSave read).
func (h *lane0Harness) readRow(t *testing.T, key string) softwarecomposition.ContainerProfile {
	t.Helper()
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	raw, err := ReadMetadata(conn, key)
	require.NoError(t, err)
	var p softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(raw, &p))
	return p
}

func (h *lane0Harness) readRowRaw(t *testing.T, key string) []byte {
	t.Helper()
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	raw, err := ReadMetadata(conn, key)
	require.NoError(t, err)
	return raw
}

func (h *lane0Harness) writeRowRaw(t *testing.T, key string, raw []byte) {
	t.Helper()
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	require.NoError(t, WriteJSON(conn, key, raw))
}

func (h *lane0Harness) listRows(t *testing.T, key string) map[string][]softwarecomposition.TimeSeriesContainers {
	t.Helper()
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	rows, err := ListTimeSeriesContainers(conn, key)
	require.NoError(t, err)
	return rows
}

func findRow(rows map[string][]softwarecomposition.TimeSeriesContainers, seriesID, suffix string) (softwarecomposition.TimeSeriesContainers, bool) {
	for _, r := range rows[seriesID] {
		if r.TsSuffix == suffix {
			return r, true
		}
	}
	return softwarecomposition.TimeSeriesContainers{}, false
}

func countRows(rows map[string][]softwarecomposition.TimeSeriesContainers) int {
	n := 0
	for _, s := range rows {
		n += len(s)
	}
	return n
}

// stampCompletedFull rewrites the persisted base to Completed/Full through a
// real GuaranteedUpdate on its own pool connection (Lock(key) held, PreSave run).
func (h *lane0Harness) stampCompletedFull(t *testing.T, key string) {
	t.Helper()
	err := h.s.GuaranteedUpdate(context.Background(), key, &softwarecomposition.ContainerProfile{}, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cp := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			cp.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Completed
			cp.Annotations[helpersv1.CompletionMetadataKey] = helpersv1.Full
			return cp, nil, nil
		}, nil)
	require.NoError(t, err)
}

// watchKey registers a full-object watcher on exactly key (sibling TS keys are
// not children of it, so their events do not arrive here).
func (h *lane0Harness) watchKey(t *testing.T, key string) *watcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	w := newWatcher(ctx, true)
	h.s.watchDispatcher.Register(key, w)
	return w
}

// drainEvents collects every event delivered within settle.
func drainEvents(w *watcher, settle time.Duration) []watch.Event {
	var out []watch.Event
	timer := time.NewTimer(settle)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-w.ResultChan():
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timer.C:
			return out
		}
	}
}

func rvOf(t *testing.T, p softwarecomposition.ContainerProfile) uint64 {
	t.Helper()
	rv, err := storage.APIObjectVersioner{}.ObjectResourceVersion(&p)
	require.NoError(t, err)
	return rv
}

const (
	lane0ZeroTime = "0001-01-01T00:00:00Z"
)

func lane0Ts(minutesAgo int) string {
	return time.Now().Add(-time.Duration(minutesAgo) * time.Minute).UTC().Format(time.RFC3339)
}

// ---------------------------------------------------------------------------
// PRE-3 — endFn(&err) deferred: a panic inside updateProfile leaves no open
// transaction.
// ---------------------------------------------------------------------------

// TestProcessTimeSeriesInTransaction_PanicLeavesNoOpenTransaction pins PRE-3.
// The panic is injected from the SECOND series' TS read, after the first
// series' Replace DELETE has taken SQLite's write lock inside the pass's
// deferred transaction; a panic before any statement would hold no lock and
// pass on today's code. Called directly with recover(): errgroup does not
// recover and the test binary would die through ConsolidateTimeSeries.
//
// Fails today: the connection goes back to the pool with the transaction open,
// and the second connection's write blocks until the busy timeout (SQLITE_BUSY).
func TestProcessTimeSeriesInTransaction_PanicLeavesNoOpenTransaction(t *testing.T) {
	h := newLane0Harness(t, 500*time.Millisecond)
	const ns, name = "ns1", "pre3"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	for _, series := range []string{"A", "B"} {
		h.seedTsRow(t, ns, name, series, series+"1", lane0Ts(5), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
		h.writeTsObject(t, key, series+"1", "ts-"+series, true)
	}

	const injected = "injected panic after the first series' DELETE"
	var reads int
	h.hooks.getTs = func(ctx context.Context, tsKey string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
		reads++
		if reads == 2 {
			panic(injected)
		}
		return next()
	}

	recovered := func() (r any) {
		defer func() { r = recover() }()
		_ = h.proc.consolidateKeyTimeSeries(context.Background(), key, false)
		return nil
	}()
	require.Equal(t, injected, recovered, "the panic must propagate (PRE-3 fixes the lock, not the crash)")
	require.Equal(t, 2, reads)

	// A write on another connection must not wait on the panicked pass's transaction.
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	start := time.Now()
	err = WriteTimeSeriesEntry(conn, "containerprofile", ns, "other", "C", "1", lane0Ts(1), helpersv1.Learning, helpersv1.Partial, "", true)
	require.NoError(t, err, "a subsequent write must succeed immediately; SQLITE_BUSY means the transaction was left open")
	require.Less(t, time.Since(start), 400*time.Millisecond)

	// And the pass's own statements were rolled back: both rows untouched.
	rows := h.listRows(t, key)
	require.Equal(t, 2, countRows(rows))
	for _, series := range []string{"A", "B"} {
		r, ok := findRow(rows, series, series+"1")
		require.True(t, ok)
		require.True(t, r.HasData, "series %s row must be rolled back to HasData=true", series)
	}
}

// ---------------------------------------------------------------------------
// Finding T — INV-PROCESSED: gate 1 (no InstanceID, nothing saved) returns no
// processed keys, so the unsaved merge's objects are not deleted.
// ---------------------------------------------------------------------------

// TestUpdateProfile_MissingInstanceID_ProcessedIsNil pins finding T. The base
// does not exist and the TS profiles carry NO InstanceID annotation
// (mergeContainerProfileTS merges annotations, so one on a TS object would put
// the key on the profile and gate 1 would never be reached).
//
// Fails today: processed names the merged TS keys, their objects are deleted,
// and the merge they carried was never persisted.
func TestUpdateProfile_MissingInstanceID_ProcessedIsNil(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "finding-t"
	key := lane0Key(ns, name)
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(5), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-A", false)
	w := h.watchKey(t, key)

	ctx, cleanup, err := h.proc.ContainerProfileStorage.WithConnection(context.Background())
	require.NoError(t, err)
	defer cleanup()
	rows, err := h.proc.ContainerProfileStorage.ListTimeSeriesContainers(ctx, key)
	require.NoError(t, err)
	profile, id, prefix, root, err := h.proc.loadOrInitializeProfile(ctx, key)
	require.NoError(t, err)

	processed, err := h.proc.processTimeSeriesInTransaction(ctx, rows, key, profile, prefix, root, id, false)
	require.NoError(t, err)
	require.Nil(t, processed, "nothing was persisted, so nothing may be scheduled for deletion")

	require.NoError(t, h.proc.deleteProcessedTimeSeries(ctx, processed))
	require.True(t, h.objectExists(t, tsKey), "the TS object must survive an unsaved merge")
	require.False(t, h.objectExists(t, key), "gate 1 saves nothing")
	require.Empty(t, drainEvents(w, 100*time.Millisecond))
}

// ---------------------------------------------------------------------------
// Finding U — a transiently unreadable TS row is neither deleted unmerged nor
// consolidated across; it is retried next tick.
// ---------------------------------------------------------------------------

// seedChain writes a continuous three-row series R2 -> R1 -> R0 (newest
// first, tail at the zero time) with objects, and returns the TS keys by suffix.
func (h *lane0Harness) seedChain(t *testing.T, ns, name, series string, newestStatus, newestCompletion string) map[string]string {
	t.Helper()
	key := lane0Key(ns, name)
	h.seedTsRow(t, ns, name, series, "2", lane0Ts(1), lane0Ts(2), newestStatus, newestCompletion, true)
	h.seedTsRow(t, ns, name, series, "1", lane0Ts(2), lane0Ts(3), helpersv1.Learning, helpersv1.Partial, true)
	h.seedTsRow(t, ns, name, series, "0", lane0Ts(3), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	return map[string]string{
		"2": h.writeTsObject(t, key, "2", "ts-2", true),
		"1": h.writeTsObject(t, key, "1", "ts-1", true),
		"0": h.writeTsObject(t, key, "0", "ts-0", true),
	}
}

// failTsReadOnce makes the read of tsKey fail once with a non-NotFound error.
func (h *lane0Harness) failTsReadOnce(tsKey string) *int {
	failures := 0
	h.hooks.getTs = func(ctx context.Context, k string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
		if k == tsKey && failures == 0 {
			failures++
			return softwarecomposition.ContainerProfile{}, errors.New("injected transient read error")
		}
		return next()
	}
	return &failures
}

func specHasExec(spec softwarecomposition.ContainerProfileSpec, tag string) bool {
	for _, e := range spec.Execs {
		if e.Path == "/bin/"+tag {
			return true
		}
	}
	return false
}

// U-1: the transient row is not the chain head. Today it is collapsed into
// its newer neighbour, its suffix is deleted and its object never merged.
func TestMergeTimeSeries_TransientReadOnNonHeadRow_SurvivesAndHealsWithoutFork(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "u1"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	tsKeys := h.seedChain(t, ns, name, "A", helpersv1.Completed, helpersv1.Full)
	before := h.listRows(t, key)
	r1Before, _ := findRow(before, "A", "1")
	r2Before, _ := findRow(before, "A", "2")
	failures := h.failTsReadOnce(tsKeys["1"])

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	require.Equal(t, 1, *failures)

	rows := h.listRows(t, key)
	r1, ok := findRow(rows, "A", "1")
	require.True(t, ok, "R1's row must survive the transient read")
	require.True(t, r1.HasData)
	require.Equal(t, r1Before.PreviousReportTimestamp, r1.PreviousReportTimestamp)
	r2, ok := findRow(rows, "A", "2")
	require.True(t, ok)
	require.Equal(t, r2Before.PreviousReportTimestamp, r2.PreviousReportTimestamp, "R2 must not be collapsed across R1")
	require.True(t, h.objectExists(t, tsKeys["1"]), "R1's object must survive")
	p := h.readPayload(t, key)
	require.NotEqual(t, helpersv1.Completed, p.Annotations[helpersv1.StatusMetadataKey], "the profile cannot complete without R1's data")

	// Next tick the read succeeds: the chain collapses to nothing left to do and the profile is Completed/Full with R1's payload.
	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	require.Equal(t, 1, *failures)
	p = h.readPayload(t, key)
	require.Equal(t, helpersv1.Completed, p.Annotations[helpersv1.StatusMetadataKey])
	require.Equal(t, helpersv1.Full, p.Annotations[helpersv1.CompletionMetadataKey])
	require.True(t, specHasExec(p.Spec, "ts-1"), "R1's distinctive payload must be merged")
	require.Equal(t, 0, countRows(h.listRows(t, key)), "the finished series is cleared")
	require.False(t, h.objectExists(t, tsKeys["1"]))
}

// U-2: the transient row is the chain head and Completed/Full. Today the
// chain collapses onto it, the terminal branch fires on the unmerged row and
// the profile is stamped Full without its data (U-b).
func TestMergeTimeSeries_TransientReadOnHeadRow_DoesNotCompleteUnmerged(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "u2"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	tsKeys := h.seedChain(t, ns, name, "A", helpersv1.Completed, helpersv1.Full)
	before := h.listRows(t, key)
	r2Before, _ := findRow(before, "A", "2")
	var processedSeen []string
	h.hooks.replace = func(ctx context.Context, k, seriesID string, del []string, ins []softwarecomposition.TimeSeriesContainers, next func() error) error {
		processedSeen = append(processedSeen, del...)
		return next()
	}
	failures := h.failTsReadOnce(tsKeys["2"])

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	require.Equal(t, 1, *failures)

	rows := h.listRows(t, key)
	r2, ok := findRow(rows, "A", "2")
	require.True(t, ok, "R2's row must survive")
	require.True(t, r2.HasData)
	require.Equal(t, r2Before.PreviousReportTimestamp, r2.PreviousReportTimestamp)
	require.NotContains(t, processedSeen, "2", "R2 must not be in the pass's delete list")
	require.True(t, h.objectExists(t, tsKeys["2"]), "R2's object must survive")
	// R1/R0 are legitimately merged and collapsed into one HasData=false row.
	require.Equal(t, 2, countRows(rows))
	p := h.readPayload(t, key)
	require.NotEqual(t, helpersv1.Completed, p.Annotations[helpersv1.StatusMetadataKey], "the profile must not complete on an unmerged head row")

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	p = h.readPayload(t, key)
	require.Equal(t, helpersv1.Completed, p.Annotations[helpersv1.StatusMetadataKey])
	require.Equal(t, helpersv1.Full, p.Annotations[helpersv1.CompletionMetadataKey])
	require.True(t, specHasExec(p.Spec, "ts-2"))
}

// U-3: pins the !HasData arm's append — an already-merged row that
// consolidateContinuousTimeSeries collapses is deleted after the pass.
func TestMergeTimeSeries_HasDataFalseRow_CollapsedRowIsDeleted(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "u3"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0Ts(2), helpersv1.Learning, helpersv1.Partial, true)
	h.writeTsObject(t, key, "1", "ts-1", true)
	h.seedTsRow(t, ns, name, "A", "0", lane0Ts(2), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, false)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	rows := h.listRows(t, key)
	_, ok := findRow(rows, "A", "0")
	require.False(t, ok, "the collapsed HasData=false row must be deleted")
	r1, ok := findRow(rows, "A", "1")
	require.True(t, ok)
	require.False(t, r1.HasData)
	require.Equal(t, lane0ZeroTime, r1.PreviousReportTimestamp, "R0 was absorbed into R1")
}

// U-4: every row of one series fails; no panic, no statement for that series,
// its rows untouched; a sibling series under the same key is processed.
func TestMergeTimeSeries_AllRowsTransient_NoWriteNoPanic(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "u4"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	h.seedTsRow(t, ns, name, "A", "A2", lane0Ts(1), lane0Ts(2), helpersv1.Learning, helpersv1.Partial, true)
	h.seedTsRow(t, ns, name, "A", "A1", lane0Ts(2), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	h.writeTsObject(t, key, "A2", "ts-A2", true)
	h.writeTsObject(t, key, "A1", "ts-A1", true)
	h.seedTsRow(t, ns, name, "B", "B1", lane0Ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	tsB := h.writeTsObject(t, key, "B1", "ts-B1", true)

	h.hooks.getTs = func(ctx context.Context, k string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
		if k == key+"-A2" || k == key+"-A1" {
			return softwarecomposition.ContainerProfile{}, errors.New("injected transient read error")
		}
		return next()
	}
	replaces := map[string]int{}
	h.hooks.replace = func(ctx context.Context, k, seriesID string, del []string, ins []softwarecomposition.TimeSeriesContainers, next func() error) error {
		replaces[seriesID]++
		return next()
	}

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	require.Equal(t, 0, replaces["A"], "no statement may run for an all-transient series")
	require.Equal(t, 1, replaces["B"])
	rows := h.listRows(t, key)
	for _, suffix := range []string{"A2", "A1"} {
		r, ok := findRow(rows, "A", suffix)
		require.True(t, ok)
		require.True(t, r.HasData, "series A rows must be untouched")
	}
	require.True(t, h.objectExists(t, key+"-A2"))
	rB, ok := findRow(rows, "B", "B1")
	require.True(t, ok)
	require.False(t, rB.HasData)
	require.False(t, h.objectExists(t, tsB), "series B was merged and its object deleted")
	require.True(t, specHasExec(h.readPayload(t, key).Spec, "ts-B1"))
}

// U-5 (characterisation): a read that fails on every tick keeps its row and
// keeps the key enumerated; it is retried each tick (the Warning per tick is
// not asserted: the package logger has no capture seam).
func TestMergeTimeSeries_PermanentReadFailure_RowSurvivesAndKeyStaysEnumerated(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "u5"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-1", true)
	reads := 0
	h.hooks.getTs = func(ctx context.Context, k string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
		reads++
		return softwarecomposition.ContainerProfile{}, errors.New("injected permanent read error")
	}

	for tick := 1; tick <= 3; tick++ {
		require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
		require.Equal(t, tick, reads, "the read is retried every tick")
		r, ok := findRow(h.listRows(t, key), "A", "1")
		require.True(t, ok)
		require.True(t, r.HasData)
		require.True(t, h.objectExists(t, tsKey))
		ctx, cleanup, err := h.proc.ContainerProfileStorage.WithConnection(context.Background())
		require.NoError(t, err)
		keys, err := h.proc.ContainerProfileStorage.ListTimeSeriesWithData(ctx)
		cleanup()
		require.NoError(t, err)
		require.Contains(t, keys, key, "the key stays enumerated")
	}
}
