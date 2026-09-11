package file

// INV-2 as a property: for every ContainerProfile key, at every point observable
// from a second connection, `metadata row exists ⇔ payloads row exists`,
// `rv == json_extract(metadata,'$.resourceVersion')` and
// `uid == json_extract(metadata,'$.uid')`. A rapid state machine generates
// Create / Update / Delete / TS-create / consolidation-tick sequences against
// the ObjectStore with a model of the expected key set, and a crash injector
// interrupts the gated transaction at EVERY statement boundary in three ways:
// returning an error (ROLLBACK), panicking (containment + ROLLBACK), and
// rolling the transaction back underneath the holder (what a process crash
// looks like to SQLite: the uncommitted WAL frames are discarded). After every
// step INV-2 is asserted from a fresh connection, along with the model.

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/storage"
	"pgregory.net/rapid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type crashKind int

const (
	crashNone crashKind = iota
	crashError
	crashPanic
	crashRollbackUnderneath
)

var errInjectedCrash = errors.New("injected crash")

// inv2Machine is the rapid state machine.
type inv2Machine struct {
	t   *rapid.T
	env *objectStoreEnv
	// live is the model: keys the store must hold (base and TS), with the
	// last RV observed for each.
	live map[string]string
	// crash configures the next gated transaction's interruption.
	crashAt   int
	crashKind crashKind
	armed     bool
	// tsBase remembers which TS suffixes have been created for the base.
	tsSuffixes []string
	names      []string
}

func (m *inv2Machine) armCrash(idx int, kind crashKind) {
	m.crashAt, m.crashKind, m.armed = idx, kind, true
}

// hook is the store's beforeStatement seam: fires the armed crash once.
func (m *inv2Machine) hook(conn *sqlite.Conn, path string, idx int, name string) error {
	if !m.armed || idx != m.crashAt {
		return nil
	}
	m.armed = false
	switch m.crashKind {
	case crashError:
		return errInjectedCrash
	case crashPanic:
		panic(errInjectedCrash)
	case crashRollbackUnderneath:
		// The process "dies": SQLite discards the uncommitted WAL frames and
		// nothing after this point runs. Emulated by rolling back on the
		// holder's own connection AND aborting the holder (a first version
		// let the remaining statements run in autocommit — which is not a
		// crash, and did produce an orphan payload row).
		_ = sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
		return errInjectedCrash
	}
	return nil
}

// disarm clears a crash that did not fire (fewer statements than crashAt).
func (m *inv2Machine) disarm() { m.armed = false }

func (m *inv2Machine) checkINV2() {
	m.env.withConn(func(conn *sqlite.Conn) {
		keys := allCPKeys(m.env.t, conn)
		for _, k := range keys {
			assertINV2(m.env.t, conn, k)
		}
		// model: every live key is present, nothing else is
		present := map[string]bool{}
		for _, k := range keys {
			present[k] = true
		}
		for k := range m.live {
			if !present[k] {
				m.t.Fatalf("model says %s is live but the store has no row", k)
			}
		}
		for _, k := range keys {
			if _, ok := m.live[k]; !ok {
				m.t.Fatalf("store has %s but the model says it was never created or was deleted", k)
			}
		}
		// rv agrees with the last observed RV
		for k, rv := range m.live {
			row := inspectRow(m.env.t, conn, k)
			if row.jsonRV != rv {
				m.t.Fatalf("%s: model rv %s, store rv %s", k, rv, row.jsonRV)
			}
		}
	})
}

func (m *inv2Machine) maybeCrash(maxIdx int) bool {
	if rapid.Bool().Draw(m.t, "crash") {
		idx := rapid.IntRange(1, maxIdx).Draw(m.t, "crashAt")
		kind := crashKind(rapid.IntRange(int(crashError), int(crashRollbackUnderneath)).Draw(m.t, "crashKind"))
		m.armCrash(idx, kind)
		return true
	}
	return false
}

func (m *inv2Machine) pickName() string {
	return m.names[rapid.IntRange(0, len(m.names)-1).Draw(m.t, "name")]
}

// ---- actions ----

func (m *inv2Machine) Create(t *rapid.T) {
	name := m.pickName()
	key := m.env.key(name)
	crashed := m.maybeCrash(4)
	p := m.env.plain(name)
	out := &softwarecomposition.ContainerProfile{}
	err := m.env.store.Create(m.env.ctx, key, p, out, 0)
	m.disarm()
	_, existed := m.live[key]
	switch {
	case crashed && errors.Is(err, errInjectedCrash):
		// interrupted: nothing changed
	case existed:
		if !storage.IsExist(err) {
			t.Fatalf("create of existing %s: want KeyExists, got %v", key, err)
		}
	default:
		if err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		m.live[key] = out.ResourceVersion
	}
	m.checkINV2()
}

func (m *inv2Machine) Update(t *rapid.T) {
	name := m.pickName()
	key := m.env.key(name)
	crashed := m.maybeCrash(3)
	out := &softwarecomposition.ContainerProfile{}
	label := fmt.Sprintf("v%d", rapid.IntRange(0, 1000).Draw(t, "label"))
	err := m.env.store.GuaranteedUpdate(m.env.ctx, key, out, false, nil, setLabel("l", label), nil)
	m.disarm()
	_, existed := m.live[key]
	switch {
	case crashed && errors.Is(err, errInjectedCrash):
		// interrupted: rv unchanged
	case !existed:
		if !storage.IsNotFound(err) {
			t.Fatalf("update of absent %s: want NotFound, got %v", key, err)
		}
	default:
		if err != nil {
			t.Fatalf("update %s: %v", key, err)
		}
		m.live[key] = out.ResourceVersion
	}
	m.checkINV2()
}

func (m *inv2Machine) Delete(t *rapid.T) {
	name := m.pickName()
	key := m.env.key(name)
	crashed := m.maybeCrash(4)
	err := m.env.store.Delete(m.env.ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
	m.disarm()
	_, existed := m.live[key]
	switch {
	case crashed && errors.Is(err, errInjectedCrash):
	case !existed:
		if !storage.IsNotFound(err) {
			t.Fatalf("delete of absent %s: want NotFound, got %v", key, err)
		}
	default:
		if err != nil {
			t.Fatalf("delete %s: %v", key, err)
		}
		delete(m.live, key)
	}
	m.checkINV2()
}

// CreateTS creates a time-series profile of the base (a chained report), so
// consolidation has something to merge; its row joins the transaction.
func (m *inv2Machine) CreateTS(t *rapid.T) {
	n := len(m.tsSuffixes) + 1
	suffix := fmt.Sprintf("r%d", n)
	key := m.env.tsKey(suffix)
	crashed := m.maybeCrash(5)
	out := &softwarecomposition.ContainerProfile{}
	err := m.env.store.Create(m.env.ctx, key, m.env.ts(suffix, n, helpersv1.Learning, helpersv1.Partial), out, 0)
	m.disarm()
	switch {
	case errors.Is(err, ObjectCompletedError):
		// the base is Completed/Full: refused, nothing written
	case crashed && errors.Is(err, errInjectedCrash):
	default:
		if err != nil {
			t.Fatalf("create ts %s: %v", key, err)
		}
		m.live[key] = out.ResourceVersion
		m.tsSuffixes = append(m.tsSuffixes, suffix)
	}
	m.checkINV2()
}

// Tick runs one consolidation pass: the write set (base save + time_series
// rewrite + processed TS deletes) commits atomically or not at all.
func (m *inv2Machine) Tick(t *rapid.T) {
	crashed := m.maybeCrash(4)
	err := m.env.processor.ConsolidateTimeSeries(m.env.ctx)
	m.disarm()
	if err != nil && !(crashed && errors.Is(err, errInjectedCrash)) {
		t.Fatalf("tick: %v", err)
	}
	// Re-derive the model from the store: a committed tick deleted the
	// processed TS objects and created/updated the base; an interrupted one
	// changed nothing. Which happened is decided by whether the TS keys are
	// still there — but INV-2 and "pre-tick or post-tick, nothing between" are
	// what we assert: either ALL processed TS keys are gone and the base
	// exists, or NONE are gone.
	m.env.withConn(func(conn *sqlite.Conn) {
		present := map[string]bool{}
		for _, k := range allCPKeys(m.env.t, conn) {
			present[k] = true
		}
		gone, kept := 0, 0
		for _, sfx := range m.tsSuffixes {
			k := m.env.tsKey(sfx)
			if _, live := m.live[k]; !live {
				continue
			}
			if present[k] {
				kept++
			} else {
				gone++
			}
		}
		if gone > 0 && kept > 0 {
			t.Fatalf("tick left a partial state: %d TS objects deleted, %d kept (crashed=%v err=%v)", gone, kept, crashed, err)
		}
		if gone > 0 {
			if !present[m.env.baseKey] {
				t.Fatalf("tick deleted TS objects but the base is absent")
			}
			for _, sfx := range m.tsSuffixes {
				delete(m.live, m.env.tsKey(sfx))
			}
			m.tsSuffixes = nil
			m.live[m.env.baseKey] = inspectRow(m.env.t, conn, m.env.baseKey).jsonRV
		} else if present[m.env.baseKey] {
			m.live[m.env.baseKey] = inspectRow(m.env.t, conn, m.env.baseKey).jsonRV
		}
	})
	m.checkINV2()
}

func (m *inv2Machine) Check(t *rapid.T) {
	m.checkINV2()
}

func TestINV2_RapidStateMachineWithCrashInjection(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		e := newObjectStoreEnv(t)
		m := &inv2Machine{t: rt, env: e, live: map[string]string{}, names: []string{"a", "b", "c"}}
		e.store.hooks.beforeStatement = m.hook
		// the base key must be part of the model once consolidation creates it
		defer func() { e.store.hooks.beforeStatement = nil }()
		rt.Repeat(rapid.StateMachineActions(m))
	})
}

// TestINV2_CrashAtEveryBoundaryDeterministic walks every statement boundary
// of the three transactions with each crash kind, deterministically, so the
// property has an exhaustive floor under the randomised machine.
func TestINV2_CrashAtEveryBoundaryDeterministic(t *testing.T) {
	kinds := []crashKind{crashError, crashPanic, crashRollbackUnderneath}
	for _, kind := range kinds {
		for idx := 1; idx <= 6; idx++ {
			t.Run(fmt.Sprintf("kind=%d/idx=%d", kind, idx), func(t *testing.T) {
				e := newObjectStoreEnv(t)
				m := &inv2Machine{env: e, live: map[string]string{}}
				e.store.hooks.beforeStatement = m.hook
				seedTS := e.ts("r1", 1, helpersv1.Learning, helpersv1.Partial)

				// Create (TS admission, insert-metadata, insert-payload,
				// insert-time-series, commit = 5 boundaries; idx 6 never fires)
				m.armCrash(idx, kind)
				err := e.store.Create(e.ctx, e.tsKey("r1"), seedTS, nil, 0)
				fired := !m.armed
				m.disarm()
				e.withConn(func(c *sqlite.Conn) {
					for _, k := range allCPKeys(t, c) {
						assertINV2(t, c, k)
					}
				})
				row := e.inspect(e.tsKey("r1"))
				base := e.inspect(e.baseKey)
				if fired {
					require.ErrorIs(t, err, errInjectedCrash)
					require.False(t, row.metaExists || row.payloadExists, "interrupted create left rows")
					require.Equal(t, 0, base.tsRows, "interrupted create left a time_series row")
					// re-create cleanly for the next transactions
					require.NoError(t, e.store.Create(e.ctx, e.tsKey("r1"), e.ts("r1", 1, helpersv1.Learning, helpersv1.Partial), nil, 0))
				} else {
					require.NoError(t, err)
					require.True(t, row.metaExists && row.payloadExists)
					require.Equal(t, 1, base.tsRows)
				}

				// Update (update-metadata, update-payload, commit = 3 boundaries)
				m.armCrash(idx, kind)
				err = e.store.GuaranteedUpdate(e.ctx, e.tsKey("r1"), &softwarecomposition.ContainerProfile{}, false, nil, setLabel("x", "y"), nil)
				fired = !m.armed
				m.disarm()
				e.withConn(func(c *sqlite.Conn) { assertINV2(t, c, e.tsKey("r1")) })
				got := e.mustGet(e.tsKey("r1"))
				if fired {
					require.ErrorIs(t, err, errInjectedCrash)
					require.Equal(t, "1", got.ResourceVersion, "interrupted update must leave rv 1")
					require.Empty(t, got.Labels["x"])
				} else {
					require.NoError(t, err)
					require.Equal(t, "2", got.ResourceVersion)
				}

				// Consolidation tick: replace-time-series, save-base, delete-ts,
				// commit = 4 boundaries
				m.armCrash(idx, kind)
				err = e.processor.ConsolidateTimeSeries(e.ctx)
				fired = !m.armed
				m.disarm()
				e.withConn(func(c *sqlite.Conn) {
					for _, k := range allCPKeys(t, c) {
						assertINV2(t, c, k)
					}
				})
				tsRow := e.inspect(e.tsKey("r1"))
				base = e.inspect(e.baseKey)
				if fired {
					require.ErrorIs(t, err, errInjectedCrash, "interrupted tick must report the error")
					require.True(t, tsRow.metaExists, "pre-tick state: TS object still there")
					require.False(t, base.metaExists, "pre-tick state: no base")
					require.Equal(t, 1, base.tsRows, "pre-tick state: time_series row still there")
				} else {
					require.NoError(t, err)
					require.False(t, tsRow.metaExists, "post-tick state: TS object deleted")
					require.True(t, base.metaExists && base.payloadExists, "post-tick state: base created")
				}

				// Delete of the base (delete-metadata, delete-payload, delete-time-series, commit)
				if base.metaExists {
					m.armCrash(idx, kind)
					err = e.store.Delete(e.ctx, e.baseKey, nil, nil, nil, nil, storage.DeleteOptions{})
					fired = !m.armed
					m.disarm()
					e.withConn(func(c *sqlite.Conn) { assertINV2(t, c, e.baseKey) })
					after := e.inspect(e.baseKey)
					if fired {
						require.ErrorIs(t, err, errInjectedCrash)
						require.True(t, after.metaExists && after.payloadExists, "interrupted delete must leave both rows")
					} else {
						require.NoError(t, err)
						require.False(t, after.metaExists || after.payloadExists)
					}
				}
				require.True(t, e.store.gate.conn.AutocommitEnabled(), "gate connection left dirty")
			})
		}
	}
}

// sortedKeys is a test helper for readable failures.
func sortedKeys(m map[string]string) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
