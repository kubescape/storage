package file

import (
	"context"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

// recordingProcessor notes whether the gate was held when a callback ran.
type recordingProcessor struct {
	inner Processor
	gate  *writeGate
	mu    sync.Mutex
	calls []string
}

func (r *recordingProcessor) note(what string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gate.held() {
		r.calls = append(r.calls, what+" (GATE HELD)")
	} else {
		r.calls = append(r.calls, what)
	}
}
func (r *recordingProcessor) AfterCreate(ctx context.Context, o runtime.Object) error {
	r.note("AfterCreate")
	return r.inner.AfterCreate(ctx, o)
}
func (r *recordingProcessor) PreSave(ctx context.Context, o runtime.Object) error {
	r.note("PreSave")
	return r.inner.PreSave(ctx, o)
}
func (r *recordingProcessor) SetStorage(s ContainerProfileStorage) { r.inner.SetStorage(s) }
func (r *recordingProcessor) TimeSeriesRowFor(o runtime.Object) (TimeSeriesRow, string, bool) {
	r.note("TimeSeriesRowFor")
	return r.inner.(TimeSeriesRowProvider).TimeSeriesRowFor(o)
}

type recordingDispatcher struct {
	inner eventDispatcher
	gate  *writeGate
	mu    sync.Mutex
	calls []string
}

func (r *recordingDispatcher) note(what string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gate.held() {
		r.calls = append(r.calls, what+" (GATE HELD)")
	} else {
		r.calls = append(r.calls, what)
	}
}
func (r *recordingDispatcher) Added(k string, m, o runtime.Object) {
	r.note("Added")
	r.inner.Added(k, m, o)
}
func (r *recordingDispatcher) Modified(k string, m, o runtime.Object) {
	r.note("Modified")
	r.inner.Modified(k, m, o)
}
func (r *recordingDispatcher) Deleted(k string, m runtime.Object) {
	r.note("Deleted")
	r.inner.Deleted(k, m)
}

// TestINV1_GateHolderExecutesOnlySQL: between BEGIN IMMEDIATE and COMMIT on
// the gate's connection, the only activity in the process is SQL on that
// connection against metadata/payloads/time_series — no PreSave, no
// AfterCreate, no watch dispatch, no statement on any other connection (a pool
// take), and the store owns no per-key MapMutex at all.
func TestINV1_GateHolderExecutesOnlySQL(t *testing.T) {
	e := newObjectStoreEnv(t)
	rp := &recordingProcessor{inner: e.processor, gate: e.store.gate}
	rd := &recordingDispatcher{inner: e.wd, gate: e.store.gate}
	e.store.processor = rp
	e.store.watchDispatcher = rd

	// MapMutex: assert absent by type.
	st := reflect.TypeOf(ObjectStore{})
	for i := 0; i < st.NumField(); i++ {
		assert.NotEqual(t, reflect.TypeOf(utils.MapMutex[string]{}), st.Field(i).Type, "ObjectStore must not carry a per-key MapMutex")
	}

	var poolTakesUnderGate atomic.Int64
	var poolTakes atomic.Int64
	e.store.hooks.onPoolTake = func() {
		poolTakes.Add(1)
		if e.store.gate.held() {
			poolTakesUnderGate.Add(1)
		}
	}

	mark := e.rec.mark()
	// Every write path: TS create (with AfterCreate's row), REST update, a
	// consolidation tick (write set), and a delete.
	e.create(e.ts("r1", 1, helpersv1.Learning, helpersv1.Partial))
	require.NoError(t, e.store.GuaranteedUpdate(e.ctx, e.tsKey("r1"), &softwarecomposition.ContainerProfile{}, false, nil, setLabel("inv", "1"), nil))
	e.tick()
	require.NoError(t, e.store.Delete(e.ctx, e.baseKey, nil, nil, nil, nil, storage.DeleteOptions{}))
	actions := e.rec.since(mark)

	for _, c := range append(rp.calls, rd.calls...) {
		assert.NotContains(t, c, "GATE HELD", "callback ran while the gate was held: %s", c)
	}
	assert.Contains(t, rp.calls, "PreSave")
	assert.Contains(t, rd.calls, "Added")
	assert.Contains(t, rd.calls, "Modified")
	assert.Contains(t, rd.calls, "Deleted")
	assert.NotContains(t, rp.calls, "AfterCreate", "the row provider path never calls AfterCreate")

	assert.Greater(t, poolTakes.Load(), int64(0))
	assert.Equal(t, int64(0), poolTakesUnderGate.Load(), "a pool connection was taken while the gate was held (acquisition order violated)")

	// The gate connection is used for nothing but the three tables. (The
	// authorizer fires at prepare time; every statement the gate connection
	// ever runs is prepared on it first, so the set of tables is exact.)
	now := writeStmtSeq.Load()
	for _, a := range actions {
		if !e.store.gate.owns(a.conn, now) || a.table == "" {
			continue
		}
		assert.Contains(t, []string{"metadata", "payloads", "time_series", "json_each"}, a.table, "unexpected table on the gate connection")
	}
}
