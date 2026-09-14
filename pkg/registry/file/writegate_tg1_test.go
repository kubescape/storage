package file

// T-G1 of .omc/plans/write-gate-sharing.md: one table-driven test per
// enumerated write site outside the gate (§1, W1–W9b), run under the flag-on
// topology with the statement recorder on every pool connection and state
// seeded only through the fixture handle. Each site asserts (i) zero
// INSERT/UPDATE/DELETE on any connection the gate never owned, (ii) the
// site's own statement on a gate-owned connection, and (iii) — once, for the
// statements themselves — that the delete/read use the primary key.
//
// On the prototype topology before gate sharing (R0) every site is red at
// (i): that failing run is the evidence the bug class exists there. After R1
// every site is green.

import (
	"context"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

type tg1Write struct {
	op    sqlite.OpType
	table string
}

type tg1Site struct {
	name  string
	state string // an acg2States name, "" for nothing seeded
	run   func(e *acg2Env, key string) error
	// want is the statement the site must run on a gate-owned connection.
	want tg1Write
}

func acg2StateByName(t *testing.T, name string) acg2KeyState {
	t.Helper()
	for _, st := range acg2States {
		if st.name == name {
			return st
		}
	}
	t.Fatalf("no key state %q", name)
	return acg2KeyState{}
}

func tg1Update(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
	m := input.(metav1.Object)
	labels := m.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["tg1"] = "updated"
	m.SetLabels(labels)
	return input, nil, nil
}

var tg1Sites = []tg1Site{
	{name: "W1-create", run: func(e *acg2Env, key string) error {
		obj := newSBOM().(*softwarecomposition.SBOMSyft)
		_, _, _, _, _, obj.Name = K8sPathToKeys(key)
		return e.legacy.Create(e.ctx, key, obj, nil, 0)
	}, want: tg1Write{sqlite.OpInsert, "metadata"}},
	{name: "W1-update", state: "present", run: func(e *acg2Env, key string) error {
		return e.legacy.GuaranteedUpdate(e.ctx, key, &softwarecomposition.SBOMSyft{}, false, nil, tg1Update, nil)
	}, want: tg1Write{sqlite.OpInsert, "metadata"}},
	{name: "W3-Delete", state: "present", run: func(e *acg2Env, key string) error {
		return e.legacy.Delete(e.ctx, key, &softwarecomposition.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{})
	}, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W3-DeleteWithConn", state: "present", run: func(e *acg2Env, key string) error {
		conn, err := e.pool.Take(e.ctx)
		if err != nil {
			return err
		}
		defer e.pool.Put(conn)
		return e.legacy.DeleteWithConn(e.ctx, conn, key, &softwarecomposition.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{})
	}, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W4-orphan-prune", state: "orphan", run: func(e *acg2Env, key string) error {
		return e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	}, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W5-corrupt-delete", state: "corrupt", run: func(e *acg2Env, key string) error {
		return e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	}, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W6a-migrate-toolfails", state: "wrongtype-toolfails", run: tg1GetHoldingWriteLock, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W6b-migrate-toolsucceeds", state: "wrongtype-toolsucceeds", run: tg1GetHoldingWriteLock, want: tg1Write{sqlite.OpInsert, "metadata"}},
	{name: "W7a-migrateUnlocked-toolfails", state: "wrongtype-toolfails", run: func(e *acg2Env, key string) error {
		return e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	}, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W7b-migrateUnlocked-toolsucceeds", state: "wrongtype-toolsucceeds", run: func(e *acg2Env, key string) error {
		return e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	}, want: tg1Write{sqlite.OpInsert, "metadata"}},
	{name: "W8-list-rewrite-toolsucceeds", state: "wrongtype-toolsucceeds", run: func(e *acg2Env, key string) error {
		return e.legacy.GetByNamespace(e.ctx, acg2Group, acg2Kind, acg2DefaultNS, &softwarecomposition.SBOMSyftList{})
	}, want: tg1Write{sqlite.OpInsert, "metadata"}},
	{name: "W9a-cleanup-delete", state: "present-unreferenced", run: tg1CleanupTick, want: tg1Write{sqlite.OpDelete, "metadata"}},
	{name: "W9b-cleanup-migrate", state: "file-without-row", run: tg1CleanupTick, want: tg1Write{sqlite.OpInsert, "metadata"}},
}

// tg1GetHoldingWriteLock reaches get()'s hasWriteLock state (migrateObject,
// W6) the way GuaranteedUpdateWithConn does: under Lock(key).
func tg1GetHoldingWriteLock(e *acg2Env, key string) error {
	conn, err := e.pool.Take(e.ctx)
	if err != nil {
		return err
	}
	defer e.pool.Put(conn)
	if err := e.legacy.locks.Lock(e.ctx, key); err != nil {
		return err
	}
	defer e.legacy.locks.Unlock(key)
	return e.legacy.get(e.ctx, conn, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{}, hasWriteLock)
}

func tg1CleanupTick(e *acg2Env, _ string) error {
	return e.cleanup.CleanupTask(e.ctx, map[string][]TypeCleanupHandlerFunc{acg2Kind: {deleteByImageId}})
}

func TestTG1_EveryLegacyWriteSiteIsGated(t *testing.T) {
	installACG2MigrationTool(t)
	for _, site := range tg1Sites {
		site := site
		t.Run(site.name, func(t *testing.T) {
			t.Parallel()
			e := newACG2OnEnv(t)
			key := sbomKey(e, strings.ToLower(site.name))
			if site.state != "" {
				obj := newSBOM()
				_, _, _, _, _, name := K8sPathToKeys(key)
				obj.(metav1.Object).SetName(name)
				acg2StateByName(t, site.state).seed(e, key, obj)
			}
			mark := e.rec.mark()
			err := site.run(e, key)
			actions := e.rec.since(mark)
			t.Logf("%s: err=%v", site.name, err)

			now := writeStmtSeq.Load()
			var ungated []string
			var gated []tg1Write
			for _, a := range actions {
				if !isWriteOp(a.op) || a.table == "" {
					continue
				}
				if e.gate.owns(a.conn, now) {
					gated = append(gated, tg1Write{a.op, a.table})
				} else {
					ungated = append(ungated, writeOpLabel(a.op)+" "+a.table)
				}
			}
			assert.Empty(t, ungated, "%s: write statements prepared on a connection the gate does not own (the bug class)", site.name)
			assert.Contains(t, gated, site.want, "%s: expected %s on %s on the gate's connection; gated writes: %v", site.name, writeOpLabel(site.want.op), site.want.table, gated)
		})
	}
}

// TestTG1_WriteStatementsUsePrimaryKey (iii): the legacy delete and the
// CAS read both resolve the row through the metadata primary key, so a hold
// is one index seek (PM-G1's index case).
func TestTG1_WriteStatementsUsePrimaryKey(t *testing.T) {
	e := newACG2OnEnv(t)
	plan := func(sql string) string {
		var details []string
		require.NoError(t, sqlitex.ExecuteTransient(e.fixture, "EXPLAIN QUERY PLAN "+sql, &sqlitex.ExecOptions{
			Args: []any{"k", "n", "x"},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				details = append(details, stmt.ColumnText(3))
				return nil
			},
		}))
		return strings.Join(details, "; ")
	}
	for _, sql := range []string{
		`DELETE FROM metadata WHERE kind = ? AND namespace = ? AND name = ? RETURNING metadata`,
		`SELECT metadata FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`,
	} {
		p := plan(sql)
		assert.True(t, strings.Contains(p, "sqlite_autoindex_metadata_1") || strings.Contains(p, "PRIMARY KEY"), "%s: plan %q does not use the primary key", sql, p)
	}
}

var _ = context.Background
