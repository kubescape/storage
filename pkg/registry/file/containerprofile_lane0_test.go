package file

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	kmetrics "k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/testutil"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
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

// ---------------------------------------------------------------------------
// Finding X — completed-immutability by construction: X-A the frozen gate,
// X-B the refusal inside the write, the caller-side slug skip.
// E3 — the payload-ahead divergence heal; B16 (metadata-ahead) observed only.
// ---------------------------------------------------------------------------

func counterValue(t *testing.T, m kmetrics.CounterMetric) float64 {
	t.Helper()
	v, err := testutil.GetCounterMetricValue(m)
	require.NoError(t, err)
	return v
}

type lane0Counters struct {
	reclaimedRows, reclaimedObjects, refusals, payloadAhead, metadataAhead float64
}

func snapshotCounters(t *testing.T) lane0Counters {
	t.Helper()
	return lane0Counters{
		reclaimedRows:    counterValue(t, metrics.ConsolidationFrozenReclaimedTotal.WithLabelValues(metrics.FrozenReclaimedRow)),
		reclaimedObjects: counterValue(t, metrics.ConsolidationFrozenReclaimedTotal.WithLabelValues(metrics.FrozenReclaimedObject)),
		refusals:         counterValue(t, metrics.ConsolidationFrozenRefusalsTotal),
		payloadAhead:     counterValue(t, metrics.ConsolidationDivergenceTotal.WithLabelValues(metrics.DivergencePayloadAhead)),
		metadataAhead:    counterValue(t, metrics.ConsolidationDivergenceTotal.WithLabelValues(metrics.DivergenceMetadataAhead)),
	}
}

func (c lane0Counters) delta(t *testing.T) lane0Counters {
	t.Helper()
	n := snapshotCounters(t)
	return lane0Counters{
		reclaimedRows:    n.reclaimedRows - c.reclaimedRows,
		reclaimedObjects: n.reclaimedObjects - c.reclaimedObjects,
		refusals:         n.refusals - c.refusals,
		payloadAhead:     n.payloadAhead - c.payloadAhead,
		metadataAhead:    n.metadataAhead - c.metadataAhead,
	}
}

// wireSlugChannel makes the processor send consolidated slugs (HostType
// Kubernetes) into a buffered channel the test inspects.
func (h *lane0Harness) wireSlugChannel() chan ConsolidatedSlugData {
	ch := make(chan ConsolidatedSlugData, 8)
	h.proc.HostType = armotypes.HostTypeKubernetes
	h.proc.ConsolidatedSlugChannel = ch
	return ch
}

func requireEqualProfiles(t *testing.T, want, got softwarecomposition.ContainerProfile) {
	t.Helper()
	require.Equal(t, want.ResourceVersion, got.ResourceVersion, "resourceVersion")
	require.Equal(t, want.Annotations, got.Annotations, "annotations")
	require.Equal(t, want.Spec, got.Spec, "spec")
}

// X-1: a persisted Completed/Full base reclaims every listed row and every
// HasData object unmerged: nothing read, nothing written, zero events, no slug.
// Fails today: the rows are merged, the profile re-saved and Modified sent.
func TestConsolidate_FrozenProfile_ReclaimsRowsAndObjectsWithoutMerge(t *testing.T) {
	run := func(t *testing.T, expired, flagOn bool) {
		old := singleWriterEnabled
		singleWriterEnabled = flagOn
		t.Cleanup(func() { singleWriterEnabled = old })

		h := newLane0Harness(t, 0)
		const ns, name = "ns1", "x1"
		key := lane0Key(ns, name)
		h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Completed, helpersv1.Full, "base"))
		before := h.readPayload(t, key)
		h.seedTsRow(t, ns, name, "A", "A2", lane0Ts(1), lane0Ts(2), helpersv1.Learning, helpersv1.Partial, true)
		h.seedTsRow(t, ns, name, "A", "A1", lane0Ts(2), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, false)
		h.seedTsRow(t, ns, name, "B", "B1", lane0Ts(1), lane0ZeroTime, helpersv1.Completed, helpersv1.Full, true)
		tsA2 := h.writeTsObject(t, key, "A2", "ts-A2", true)
		tsB1 := h.writeTsObject(t, key, "B1", "ts-B1", true)
		reads := 0
		h.hooks.getTs = func(ctx context.Context, k string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
			reads++
			return next()
		}
		slugs := h.wireSlugChannel()
		w := h.watchKey(t, key)
		c0 := snapshotCounters(t)

		if expired {
			require.NoError(t, h.proc.consolidateKeyTimeSeries(context.Background(), key, true))
		} else {
			require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
		}

		requireEqualProfiles(t, before, h.readPayload(t, key))
		row := h.readRow(t, key)
		require.Equal(t, before.ResourceVersion, row.ResourceVersion)
		require.Equal(t, before.Annotations, row.Annotations)
		require.Empty(t, drainEvents(w, 100*time.Millisecond), "a frozen tick dispatches nothing")
		require.Equal(t, 0, countRows(h.listRows(t, key)), "every listed row is reclaimed")
		require.False(t, h.objectExists(t, tsA2))
		require.False(t, h.objectExists(t, tsB1))
		require.Equal(t, 0, reads, "no TS object is read")
		d := c0.delta(t)
		require.Equal(t, float64(3), d.reclaimedRows)
		require.Equal(t, float64(2), d.reclaimedObjects)
		require.Zero(t, d.refusals)
		require.Empty(t, slugs, "a frozen tick sends no slug")
	}
	t.Run("active", func(t *testing.T) { run(t, false, true) })
	t.Run("expired", func(t *testing.T) { run(t, true, true) })
	t.Run("single writer off", func(t *testing.T) { run(t, false, false) })
}

// X-2 (pin): the completing tick itself is not refused -- the gate reads the
// persisted state, never the copy the pass stamps. One Modified, one slug.
func TestConsolidate_CompletingTick_IsNotRefused(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "x2"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	before := h.readPayload(t, key)
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0ZeroTime, helpersv1.Completed, helpersv1.Full, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-1", true)
	slugs := h.wireSlugChannel()
	w := h.watchKey(t, key)
	c0 := snapshotCounters(t)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	after := h.readPayload(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(after.Annotations))
	require.Equal(t, rvOf(t, before)+1, rvOf(t, after))
	require.True(t, specHasExec(after.Spec, "ts-1"))
	require.True(t, softwarecomposition.IsCompletedFull(h.readRow(t, key).Annotations))
	events := drainEvents(w, 100*time.Millisecond)
	require.Len(t, events, 1)
	require.Equal(t, watch.Modified, events[0].Type)
	require.Len(t, slugs, 1, "the completing tick sends exactly one slug")
	require.False(t, h.objectExists(t, tsKey))
	d := c0.delta(t)
	require.Zero(t, d.refusals)
	require.Zero(t, d.reclaimedRows)
}

// X-3: the race X-A cannot see. The base is stamped Completed/Full on its own
// connection after the pass read it as Learning and before the pass's first
// statement; the pass's save must be refused on the persisted state, the
// transaction rolled back, and the next tick reclaims through X-A.
// Fails today: the merge is written into the Completed/Full profile.
func TestSaveContainerProfile_RefusesWhenPersistedIsCompletedFull(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "x3"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-1", true)
	w := h.watchKey(t, key)
	stamped := false
	h.hooks.getProfile = func(ctx context.Context, k string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
		p, err := next()
		if !stamped {
			stamped = true
			h.stampCompletedFull(t, k)
		}
		return p, err
	}
	c0 := snapshotCounters(t)

	err := h.proc.ConsolidateTimeSeries(context.Background())
	require.ErrorIs(t, err, ProfileFrozenError)
	require.True(t, stamped)

	stampedPayload := h.readPayload(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(stampedPayload.Annotations))
	require.False(t, specHasExec(stampedPayload.Spec, "ts-1"), "the merge must not be written")
	r, ok := findRow(h.listRows(t, key), "A", "1")
	require.True(t, ok, "the transaction rolled back: the row survives")
	require.True(t, r.HasData)
	require.True(t, h.objectExists(t, tsKey), "nothing was scheduled for deletion")
	events := drainEvents(w, 100*time.Millisecond)
	require.Len(t, events, 1, "exactly the stamp's Modified, none from the pass")
	require.Equal(t, float64(1), c0.delta(t).refusals)

	// Next tick: X-A reclaims, the profile is untouched.
	c1 := snapshotCounters(t)
	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	requireEqualProfiles(t, stampedPayload, h.readPayload(t, key))
	require.Equal(t, 0, countRows(h.listRows(t, key)))
	require.False(t, h.objectExists(t, tsKey))
	require.Empty(t, drainEvents(w, 100*time.Millisecond))
	d := c1.delta(t)
	require.Equal(t, float64(1), d.reclaimedRows)
	require.Equal(t, float64(1), d.reclaimedObjects)
}

// X-4 (pin): Completed/Partial is not frozen -- a Full series completes it.
func TestConsolidate_CompletedPartialProfile_IsNotFrozen(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "x4"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Completed, helpersv1.Partial, "base"))
	before := h.readPayload(t, key)
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0ZeroTime, helpersv1.Completed, helpersv1.Full, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-1", true)
	w := h.watchKey(t, key)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	after := h.readPayload(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(after.Annotations))
	require.Equal(t, rvOf(t, before)+1, rvOf(t, after))
	require.True(t, specHasExec(after.Spec, "ts-1"), "the payload is merged")
	require.Len(t, drainEvents(w, 100*time.Millisecond), 1)
	require.False(t, h.objectExists(t, tsKey))
}

// X-5 (pin): a row written after the list during a frozen tick is untouched
// by that tick (Replace's delete is scoped to the listed suffixes) and is
// reclaimed, with its object, on the next.
func TestConsolidate_FrozenGate_UnlistedRowSurvivesToNextTick(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "x5"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Completed, helpersv1.Full, "base"))
	before := h.readPayload(t, key)
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(2), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	ts1 := h.writeTsObject(t, key, "1", "ts-1", true)
	var tsLate string
	h.hooks.listTs = func(ctx context.Context, k string, next func() (map[string][]softwarecomposition.TimeSeriesContainers, error)) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
		rows, err := next()
		if tsLate == "" {
			h.seedTsRow(t, ns, name, "A", "late", lane0Ts(1), lane0Ts(2), helpersv1.Learning, helpersv1.Partial, true)
			tsLate = h.writeTsObject(t, key, "late", "ts-late", true)
		}
		return rows, err
	}
	c0 := snapshotCounters(t)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	rows := h.listRows(t, key)
	_, ok := findRow(rows, "A", "1")
	require.False(t, ok)
	require.False(t, h.objectExists(t, ts1))
	late, ok := findRow(rows, "A", "late")
	require.True(t, ok, "the unlisted row survives the frozen tick")
	require.True(t, late.HasData)
	require.True(t, h.objectExists(t, tsLate))
	d := c0.delta(t)
	require.Equal(t, float64(1), d.reclaimedRows)
	require.Equal(t, float64(1), d.reclaimedObjects)

	c1 := snapshotCounters(t)
	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	require.Equal(t, 0, countRows(h.listRows(t, key)))
	require.False(t, h.objectExists(t, tsLate))
	d = c1.delta(t)
	require.Equal(t, float64(1), d.reclaimedRows)
	require.Equal(t, float64(1), d.reclaimedObjects)
	requireEqualProfiles(t, before, h.readPayload(t, key))
}

// makePayloadAhead persists a base at RV n (Learning), completes it at RV n+1
// through a real GuaranteedUpdate, then rewrites the metadata row back to the
// RV-n Learning state -- exactly the rolled-back commit a crash between the
// payload rename and the outer COMMIT leaves. Returns the Completed/Full row
// as it was committed at n+1, for tests that replay a concurrent completer.
func (h *lane0Harness) makePayloadAhead(t *testing.T, ns, name string) (key string, fullRow []byte) {
	t.Helper()
	key = lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	learningRow := h.readRowRaw(t, key)
	h.stampCompletedFull(t, key)
	fullRow = h.readRowRaw(t, key)
	h.writeRowRaw(t, key, learningRow)
	payload := h.readPayload(t, key)
	row := h.readRow(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(payload.Annotations))
	require.False(t, softwarecomposition.IsCompletedFull(row.Annotations))
	require.Equal(t, rvOf(t, row)+1, rvOf(t, payload))
	return key, fullRow
}

// E3-1: payload-ahead is healed without a merge: the payload is re-persisted
// as-is at n+2, the row lands Completed/Full at n+2, the lost Modified is
// dispatched after COMMIT, and X-A reclaims the late rows in the same tick.
// Fails today (the surviving row is re-merged: duplicated Spec, no counter)
// and on X-A alone (the row stays Learning forever, zero Modified).
func TestConsolidate_PayloadAheadDivergence_HealsWithoutMerge(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "e3-1"
	key, _ := h.makePayloadAhead(t, ns, name)
	before := h.readPayload(t, key)
	n := rvOf(t, h.readRow(t, key))
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-1", true)
	slugs := h.wireSlugChannel()
	w := h.watchKey(t, key)
	c0 := snapshotCounters(t)

	// Observe the row at the moment the event is received: a Modified
	// dispatched before COMMIT would still show the Learning row here.
	type receipt struct {
		ev  watch.Event
		row softwarecomposition.ContainerProfile
	}
	received := make(chan receipt, 8)
	go func() {
		for ev := range w.ResultChan() {
			received <- receipt{ev: ev, row: h.readRow(t, key)}
		}
	}()

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	row := h.readRow(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(row.Annotations))
	require.Equal(t, n+2, rvOf(t, row))
	after := h.readPayload(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(after.Annotations))
	require.Equal(t, n+2, rvOf(t, after))
	require.Equal(t, before.Spec, after.Spec, "no merge, no duplicates")
	require.Equal(t, before.Annotations[helpersv1.InstanceIDMetadataKey], after.Annotations[helpersv1.InstanceIDMetadataKey])

	var got []receipt
	timeout := time.After(2 * time.Second)
	for len(got) == 0 {
		select {
		case r := <-received:
			got = append(got, r)
		case <-timeout:
			t.Fatal("the lost completion event was not dispatched")
		}
	}
	time.Sleep(100 * time.Millisecond)
	for len(received) > 0 {
		got = append(got, <-received)
	}
	require.Len(t, got, 1, "exactly one Modified")
	require.Equal(t, watch.Modified, got[0].ev.Type)
	require.True(t, softwarecomposition.IsCompletedFull(got[0].row.Annotations), "the event is dispatched after COMMIT: the row is already Full on receipt")

	d := c0.delta(t)
	require.Equal(t, float64(1), d.payloadAhead)
	require.Zero(t, d.metadataAhead)
	require.Equal(t, float64(1), d.reclaimedRows, "X-A reclaims in the same tick")
	require.Equal(t, float64(1), d.reclaimedObjects)
	require.Equal(t, 0, countRows(h.listRows(t, key)))
	require.False(t, h.objectExists(t, tsKey))
	require.Empty(t, slugs)
}

// E3-2: metadata-ahead (B16) is counted, then today's behaviour is pinned as
// the code does it: the row is merged and re-saved through
// GuaranteedUpdateWithConn, Status Completed restored from the row by
// PreSave's non-TS revert (the setters hand Learning to the save), Completion
// Full from the merged series row, and the RV lands at n+1 -- colliding with
// the row's own n+1, not advancing past it. Fails today on the counter only.
func TestConsolidate_MetadataAheadDivergence_IsObservedNotHealed(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "e3-2"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	n := rvOf(t, h.readPayload(t, key))
	row := h.readRow(t, key)
	row.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Completed
	row.Annotations[helpersv1.CompletionMetadataKey] = helpersv1.Full
	require.NoError(t, storage.APIObjectVersioner{}.UpdateObject(&row, n+1))
	raw, err := json.Marshal(&row)
	require.NoError(t, err)
	h.writeRowRaw(t, key, raw)
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Full, true)
	tsKey := h.writeTsObject(t, key, "1", "ts-1", true)
	var handedStatus string
	h.hooks.save = func(ctx context.Context, k string, p *softwarecomposition.ContainerProfile, next func() error) error {
		handedStatus = p.Annotations[helpersv1.StatusMetadataKey]
		return next()
	}
	w := h.watchKey(t, key)
	c0 := snapshotCounters(t)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	d := c0.delta(t)
	require.Equal(t, float64(1), d.metadataAhead)
	require.Zero(t, d.payloadAhead)
	require.Equal(t, helpersv1.Learning, handedStatus, "the setters hand Learning to the save; PreSave's revert restores Completed")
	after := h.readRow(t, key)
	require.Equal(t, helpersv1.Completed, after.Annotations[helpersv1.StatusMetadataKey])
	require.Equal(t, helpersv1.Full, after.Annotations[helpersv1.CompletionMetadataKey])
	require.Equal(t, n+1, rvOf(t, after), "RV n+1: the row's own RV, overwritten in place")
	payload := h.readPayload(t, key)
	require.Equal(t, n+1, rvOf(t, payload))
	require.True(t, softwarecomposition.IsCompletedFull(payload.Annotations))
	require.True(t, specHasExec(payload.Spec, "ts-1"), "the row is merged")
	require.Len(t, drainEvents(w, 100*time.Millisecond), 1)
	require.False(t, h.objectExists(t, tsKey))
}

// E3-3 (pin, defence in depth): a completer holding an uncommitted write
// transaction that already stamped the row Completed/Full makes the heal wait
// at BEGIN IMMEDIATE -- with Lock(key) held -- until it commits; the heal
// then re-reads Full, writes nothing, dispatches nothing, counts nothing.
func TestHealDivergence_ConcurrentCompleterWins(t *testing.T) {
	h := newLane0Harness(t, 3*time.Second)
	const ns, name = "ns1", "e3-3"
	key, fullRow := h.makePayloadAhead(t, ns, name)
	payloadBefore := h.readPayload(t, key)
	w := h.watchKey(t, key)
	c0 := snapshotCounters(t)

	completer, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(completer)
	require.NoError(t, sqlitex.Execute(completer, "BEGIN IMMEDIATE;", nil))
	require.NoError(t, WriteJSON(completer, key, fullRow))

	ctx, cleanup, err := h.proc.ContainerProfileStorage.WithConnection(context.Background())
	require.NoError(t, err)
	defer cleanup()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- h.proc.ContainerProfileStorage.HealDivergence(ctx, key) }()

	// The heal is parked at BEGIN IMMEDIATE behind the completer's write lock,
	// and Lock(key) is already held (lock-order probe).
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("heal returned %v while the completer's transaction is open", err)
	default:
	}
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer probeCancel()
	require.Error(t, h.s.locks.Lock(probeCtx, key), "Lock(key) must be held across the BEGIN IMMEDIATE wait")

	require.NoError(t, sqlitex.Execute(completer, "COMMIT;", nil))
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("heal did not return after the completer committed")
	}
	require.GreaterOrEqual(t, time.Since(start), 300*time.Millisecond)

	requireEqualProfiles(t, payloadBefore, h.readPayload(t, key))
	row := h.readRow(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(row.Annotations))
	require.Equal(t, rvOf(t, payloadBefore), rvOf(t, row), "the completer's row stands")
	require.Empty(t, drainEvents(w, 100*time.Millisecond), "nothing dispatched")
	require.Zero(t, c0.delta(t).payloadAhead, "nothing counted")
}

// E3-4 (pin): a heal that crashes after its own rename (B3's shape: the row
// rolled back, the payload one version ahead again) leaves the same
// payload-ahead shape, and the next heal converges: row Full at n+3, one Modified.
func TestHealDivergence_CrashInsideHeal_Converges(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "e3-4"
	key, _ := h.makePayloadAhead(t, ns, name)
	n := rvOf(t, h.readRow(t, key))
	injected := errors.New("injected failure after the rename")
	failNext := true
	h.s.renamePayloadFn = func(oldpath, newpath string) error {
		if err := h.s.appFs.Rename(oldpath, newpath); err != nil {
			return err
		}
		if failNext {
			failNext = false
			return injected
		}
		return nil
	}
	w := h.watchKey(t, key)
	c0 := snapshotCounters(t)
	ctx, cleanup, err := h.proc.ContainerProfileStorage.WithConnection(context.Background())
	require.NoError(t, err)
	defer cleanup()

	err = h.proc.ContainerProfileStorage.HealDivergence(ctx, key)
	require.ErrorIs(t, err, injected)
	require.Equal(t, metrics.HealFailedSave, healFailureReason(err))
	row := h.readRow(t, key)
	require.False(t, softwarecomposition.IsCompletedFull(row.Annotations), "the row rolled back")
	require.Equal(t, n, rvOf(t, row))
	payload := h.readPayload(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(payload.Annotations))
	require.Equal(t, n+2, rvOf(t, payload), "the renamed payload is one version further ahead")
	require.Empty(t, drainEvents(w, 100*time.Millisecond))
	require.Zero(t, c0.delta(t).payloadAhead)

	require.NoError(t, h.proc.ContainerProfileStorage.HealDivergence(ctx, key))
	row = h.readRow(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(row.Annotations))
	require.Equal(t, n+3, rvOf(t, row))
	require.Equal(t, n+3, rvOf(t, h.readPayload(t, key)))
	events := drainEvents(w, 100*time.Millisecond)
	require.Len(t, events, 1)
	require.Equal(t, watch.Modified, events[0].Type)
	require.Equal(t, float64(1), c0.delta(t).payloadAhead)
}

// ---------------------------------------------------------------------------
// PC-TS-4 — the terminal branch executes no whole-key delete: Replace's
// list-scoped delete is the only time_series delete on the consolidation path.
// ---------------------------------------------------------------------------

// TS4-B: two series; A takes the terminal branch first (order forced). After
// tick 1 B's row and object survive and the profile is Completed/Full; tick 2
// reclaims B unmerged through the frozen gate. Fails today on "B's row
// survives tick 1" (the whole-key delete removed it and orphaned its object).
func TestConsolidate_TerminalBranch_LeavesUnreachedSeriesIntact(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "ts4b"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	before := h.readPayload(t, key)
	h.seedTsRow(t, ns, name, "A", "A1", lane0Ts(1), lane0ZeroTime, helpersv1.Completed, helpersv1.Full, true)
	tsA := h.writeTsObject(t, key, "A1", "ts-A1", true)
	h.seedTsRow(t, ns, name, "B", "B1", lane0Ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	tsB := h.writeTsObject(t, key, "B1", "ts-B1", true)
	h.proc.seriesOrder = func(timeSeries map[string][]softwarecomposition.TimeSeriesContainers) []string {
		require.Len(t, timeSeries, 2)
		return []string{"A", "B"}
	}
	w := h.watchKey(t, key)
	c0 := snapshotCounters(t)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	completed := h.readPayload(t, key)
	require.True(t, softwarecomposition.IsCompletedFull(completed.Annotations))
	require.Equal(t, rvOf(t, before)+1, rvOf(t, completed))
	require.True(t, specHasExec(completed.Spec, "ts-A1"))
	require.False(t, specHasExec(completed.Spec, "ts-B1"), "B is unreached: never merged")
	rows := h.listRows(t, key)
	_, ok := findRow(rows, "A", "A1")
	require.False(t, ok, "A's listed rows are gone")
	require.False(t, h.objectExists(t, tsA))
	rB, ok := findRow(rows, "B", "B1")
	require.True(t, ok, "B's row survives the terminal branch")
	require.True(t, rB.HasData)
	require.True(t, h.objectExists(t, tsB), "B's object survives (not orphaned)")
	require.Len(t, drainEvents(w, 100*time.Millisecond), 1)
	require.Zero(t, c0.delta(t).reclaimedRows)

	// Tick 2: the frozen gate reclaims B unmerged, with its object.
	c1 := snapshotCounters(t)
	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	requireEqualProfiles(t, completed, h.readPayload(t, key))
	require.Empty(t, drainEvents(w, 100*time.Millisecond))
	require.Equal(t, 0, countRows(h.listRows(t, key)))
	require.False(t, h.objectExists(t, tsB))
	d := c1.delta(t)
	require.Equal(t, float64(1), d.reclaimedRows)
	require.Equal(t, float64(1), d.reclaimedObjects)
}

// TS4-C (pin): a row that lands after the pass's list, during a NON-terminal
// pass, is neither deleted nor merged by that pass; the next tick merges it.
// Passes today (the whole-key delete ran on terminal branches alone); kept
// as the only guard of the non-terminal branch against a future whole-key
// statement.
func TestConsolidate_RowArrivingDuringPass_IsNotDeleted(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "ts4c"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	h.seedTsRow(t, ns, name, "A", "1", lane0Ts(2), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	ts1 := h.writeTsObject(t, key, "1", "ts-1", true)
	var tsLate string
	h.hooks.getProfile = func(ctx context.Context, k string, next func() (softwarecomposition.ContainerProfile, error)) (softwarecomposition.ContainerProfile, error) {
		p, err := next()
		if tsLate == "" {
			h.seedTsRow(t, ns, name, "A", "late", lane0Ts(1), lane0Ts(2), helpersv1.Learning, helpersv1.Partial, true)
			tsLate = h.writeTsObject(t, key, "late", "ts-late", true)
		}
		return p, err
	}

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	p := h.readPayload(t, key)
	require.True(t, specHasExec(p.Spec, "ts-1"))
	require.False(t, specHasExec(p.Spec, "ts-late"))
	require.False(t, h.objectExists(t, ts1))
	late, ok := findRow(h.listRows(t, key), "A", "late")
	require.True(t, ok, "the late row survives the pass")
	require.True(t, late.HasData)
	require.True(t, h.objectExists(t, tsLate))

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	p = h.readPayload(t, key)
	require.True(t, specHasExec(p.Spec, "ts-late"), "the next tick merges it")
	require.False(t, h.objectExists(t, tsLate))
	rows := h.listRows(t, key)
	require.Equal(t, 1, countRows(rows), "late collapsed with the merged chain into one row")
	r, ok := findRow(rows, "A", "late")
	require.True(t, ok)
	require.False(t, r.HasData)
	require.Equal(t, lane0ZeroTime, r.PreviousReportTimestamp)
}
