package file

import (
	"context"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// guardFs counts every payload-file operation the legacy store issues.
type guardFs struct {
	afero.Fs
	ops atomic.Int64
}

func (g *guardFs) Open(name string) (afero.File, error) { g.ops.Add(1); return g.Fs.Open(name) }
func (g *guardFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	g.ops.Add(1)
	return g.Fs.OpenFile(name, flag, perm)
}
func (g *guardFs) Rename(o, n string) error { g.ops.Add(1); return g.Fs.Rename(o, n) }
func (g *guardFs) Remove(name string) error { g.ops.Add(1); return g.Fs.Remove(name) }
func (g *guardFs) Stat(name string) (os.FileInfo, error) {
	g.ops.Add(1)
	return g.Fs.Stat(name)
}

// TestINV4_LegacyStoreRefusesContainerProfileKeys runs every full-object
// operation of the legacy StorageImpl (the guarded paths of design §5.6) on a
// containerprofile key while the ObjectStore owns the kind, and asserts: the
// ownership refusal (InternalError), ZERO statements on metadata / payloads /
// time_series, ZERO payload-file operations, the rows untouched — and that the
// metadata-only reads, which both backends agree on, are NOT refused.
func TestINV4_LegacyStoreRefusesContainerProfileKeys(t *testing.T) {
	e := newObjectStoreEnv(t)
	fs := &guardFs{Fs: e.legacyFs}
	e.legacy.appFs = fs

	// Seed through the owner.
	e.create(e.ts("r1", 1, helpersv1.Learning, helpersv1.Partial))
	e.tick()
	base := e.mustGet(e.baseKey)
	before := e.inspect(e.baseKey)
	listKey := testCPPrefix + e.ns
	cp := func() *softwarecomposition.ContainerProfile { return &softwarecomposition.ContainerProfile{} }
	identity := identityTryUpdate

	type op struct {
		name string
		run  func() error
	}
	ops := []op{
		{"Get(full)", func() error { return e.legacy.Get(e.ctx, e.baseKey, storage.GetOptions{}, cp()) }},
		{"GetList(fullSpec)", func() error {
			return e.legacy.GetList(e.ctx, listKey, storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec, Recursive: true}, &softwarecomposition.ContainerProfileList{})
		}},
		{"Create", func() error { return e.legacy.Create(e.ctx, e.key("guard-new"), e.plain("guard-new"), nil, 0) }},
		{"GuaranteedUpdate", func() error { return e.legacy.GuaranteedUpdate(e.ctx, e.baseKey, cp(), false, nil, identity, nil) }},
		{"Delete", func() error { return e.legacy.Delete(e.ctx, e.baseKey, cp(), nil, nil, nil, storage.DeleteOptions{}) }},
		{"GetByNamespace", func() error {
			return e.legacy.GetByNamespace(e.ctx, "spdx.softwarecomposition.kubescape.io", ContainerProfileKind, e.ns, &softwarecomposition.ContainerProfileList{})
		}},
		{"GetByCluster", func() error {
			return e.legacy.GetByCluster(e.ctx, "spdx.softwarecomposition.kubescape.io", ContainerProfileKind, &softwarecomposition.ContainerProfileList{})
		}},
		{"appendGobObjectFromFile", func() error {
			list := &softwarecomposition.ContainerProfileList{}
			v := reflect.ValueOf(&list.Items).Elem()
			return e.legacy.appendGobObjectFromFile(e.ctx, DefaultStorageRoot+e.baseKey+GobExt, v)
		}},
		{"CreateWithConn", func() error {
			return e.withLegacyConn(func(conn *sqlite.Conn) error {
				return e.legacy.CreateWithConn(e.ctx, conn, e.key("guard-new2"), e.plain("guard-new2"), nil, 0)
			})
		}},
		{"GuaranteedUpdateWithConn", func() error {
			return e.withLegacyConn(func(conn *sqlite.Conn) error {
				return e.legacy.GuaranteedUpdateWithConn(e.ctx, conn, e.baseKey, cp(), false, nil, identity, nil, "")
			})
		}},
	}
	// The flag-off write path (singleWriterEnabled=false) reaches
	// CreateWithConn/GuaranteedUpdateWithConn through Create/GuaranteedUpdate.
	ops = append(ops,
		op{"Create(singleWriter=false)", func() error {
			old := singleWriterEnabled
			singleWriterEnabled = false
			defer func() { singleWriterEnabled = old }()
			return e.legacy.Create(e.ctx, e.key("guard-new3"), e.plain("guard-new3"), nil, 0)
		}},
		op{"GuaranteedUpdate(singleWriter=false)", func() error {
			old := singleWriterEnabled
			singleWriterEnabled = false
			defer func() { singleWriterEnabled = old }()
			return e.legacy.GuaranteedUpdate(e.ctx, e.baseKey, cp(), false, nil, identity, nil)
		}},
	)

	// An observer connection: PRAGMA data_version changes whenever ANY other
	// connection commits a change to the database — a write detector that does
	// not depend on statement caching (the authorizer fires at prepare time).
	observer, err := e.pool.Take(e.ctx)
	require.NoError(t, err)
	defer e.pool.Put(observer)
	dataVersion := func() int64 {
		var v int64
		require.NoError(t, sqlitex.ExecuteTransient(observer, `PRAGMA data_version`, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { v = stmt.ColumnInt64(0); return nil }}))
		return v
	}

	// Step 1-L's statement observer: every sqlitex.Execute call site in this
	// package reports its name at execution time (the authorizer only sees
	// prepares). The design's INV-4 instrument.
	var stmts []string
	var stmtsMu sync.Mutex
	setStmtObserver(func(site string) { stmtsMu.Lock(); stmts = append(stmts, site); stmtsMu.Unlock() })
	defer setStmtObserver(nil)

	for _, o := range ops {
		t.Run(o.name, func(t *testing.T) {
			fs.ops.Store(0)
			dv := dataVersion()
			stmtsMu.Lock()
			stmts = nil
			stmtsMu.Unlock()
			mark := e.rec.mark()
			err := o.run()
			actions := e.rec.since(mark)
			stmtsMu.Lock()
			executed := append([]string(nil), stmts...)
			stmtsMu.Unlock()
			assert.Empty(t, executed, "%s: legacy call sites executed statements after refusal", o.name)
			require.Error(t, err, "must refuse")
			assert.True(t, apierrors.IsInternalError(err), "refusal must be an InternalError, got %T: %v", err, err)
			assert.Contains(t, err.Error(), "owned by the ContainerProfile SQLite backend")
			for _, a := range actions {
				assert.Empty(t, a.table, "%s: statement on %s (%v) after refusal", o.name, a.table, a.op)
			}
			assert.Equal(t, dv, dataVersion(), "%s: the database was written after refusal", o.name)
			assert.Equal(t, int64(0), fs.ops.Load(), "%s: payload-file operation after refusal", o.name)
			assert.Equal(t, before, e.inspect(e.baseKey), "%s: rows changed", o.name)
		})
	}

	t.Run("metadata-only reads are not refused", func(t *testing.T) {
		out := cp()
		require.NoError(t, e.legacy.Get(e.ctx, e.baseKey, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, out))
		assert.Equal(t, base.ResourceVersion, out.ResourceVersion)
		assert.Equal(t, base.UID, out.UID)
		list := &softwarecomposition.ContainerProfileList{}
		require.NoError(t, e.legacy.GetList(e.ctx, listKey, storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata, Recursive: true}, list))
		require.Len(t, list.Items, 1)
		assert.Equal(t, base.Name, list.Items[0].Name)
		n, err := e.legacy.Count(listKey)
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
	})

	t.Run("other kinds are served", func(t *testing.T) {
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyft/" + e.ns + "/img"
		require.NoError(t, e.legacy.Create(e.ctx, key, &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: "img", Namespace: e.ns}}, nil, 0))
		require.NoError(t, e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{}))
		require.NoError(t, e.legacy.Delete(e.ctx, key, &softwarecomposition.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{}))
	})

	t.Run("guard removed serves CP again", func(t *testing.T) {
		e.legacy.SetForeignKinds(nil)
		defer e.legacy.SetForeignKinds(IsContainerProfileKind)
		// The legacy full read of an ObjectStore row finds no gob file and,
		// exactly as the design warns (PM-4), would delete the shared row —
		// which is why the guard exists. Only the metadata read is safe here.
		require.NoError(t, e.legacy.Get(e.ctx, e.baseKey, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, cp()))
	})
}

func (e *objectStoreEnv) withLegacyConn(fn func(conn *sqlite.Conn) error) error {
	conn, err := e.pool.Take(context.Background())
	if err != nil {
		return err
	}
	defer e.pool.Put(conn)
	return fn(conn)
}

// TestINV4_UnguardedLegacyFullReadDeletesOwnedRow documents the hazard the
// guard closes (PM-4): without it, a legacy full GET on an ObjectStore key
// deletes the shared metadata row because the gob file is missing.
func TestINV4_UnguardedLegacyFullReadDeletesOwnedRow(t *testing.T) {
	e := newObjectStoreEnv(t)
	e.create(e.plain("pm4"))
	e.legacy.SetForeignKinds(nil)
	err := e.legacy.Get(e.ctx, e.key("pm4"), storage.GetOptions{}, &softwarecomposition.ContainerProfile{})
	assert.True(t, storage.IsNotFound(err))
	row := e.inspect(e.key("pm4"))
	assert.False(t, row.metaExists, "the unguarded legacy read deleted the row (the PM-4 hazard)")
	assert.True(t, row.payloadExists, "leaving an orphan payload: INV-2 violated")
	e.legacy.SetForeignKinds(IsContainerProfileKind)
}
