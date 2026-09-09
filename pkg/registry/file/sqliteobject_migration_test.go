package file

// Tests of the startup ContainerProfile data migration (§8.2, K-1, K-2, R3,
// PM-2, the §9 "integration additions from Part C").
//
// The "old binary" is a real legacy StorageImpl (no guard, no gate) over a
// second pool on the same database file and the same afero.Fs: it writes the
// exact row + gob file a pre-flag process writes, and the AC-G1 ledger is
// vacuous for its pool. The "new binary" is the gated pool (AC-G1 armed),
// the migration and an ObjectStore built on it; startNew/stopNew are one boot.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

type migrationEnv struct {
	t      *testing.T
	ctx    context.Context
	dir    string
	dbPath string
	fs     afero.Fs
	scheme *runtime.Scheme
	// pool is the new binary's pool: AC-G1 armed for the whole test.
	pool *sqlitemigration.Pool
	// legacyPool/legacy are the old binary: an unguarded, ungated
	// StorageImpl with the CP processor, writing rows and gob files.
	legacyPool *sqlitemigration.Pool
	legacy     *StorageImpl
	fixture    *sqlite.Conn
	tpl        softwarecomposition.ContainerProfile
	ns         string

	gate  *writeGate
	store *ObjectStore
}

// newMigrationEnv builds the environment over fs (a MemMapFs unless the
// test needs real files for a child process).
func newMigrationEnv(t *testing.T, fs afero.Fs) *migrationEnv {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metadata.sq3")
	pool := NewPoolWithOptions(dbPath, PoolOptions{Size: 4, BusyTimeout: 5 * time.Second, DisableAutoCheckpoint: true})
	armUngatedWriteCheck(t, pool)
	sch := runtime.NewScheme()
	install.Install(sch)
	// The old binary: SQLite defaults (autocheckpoint on), no gate, no guard.
	legacyPool := NewPoolWithOptions(dbPath, PoolOptions{Size: 4, BusyTimeout: 5 * time.Second})
	legacyProcessor := NewContainerProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxContainerProfileSize: 40000}, nil)
	legacyProcessor.Interval = 0
	legacy := NewStorageImplWithCollector(fs, DefaultStorageRoot, legacyPool, NewWatchDispatcher(), sch, legacyProcessor).(*StorageImpl)

	content, err := os.ReadFile("testdata/p1.json")
	require.NoError(t, err)
	var tpl softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(content, &tpl))

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	e := &migrationEnv{
		t: t, ctx: ctx, dir: dir, dbPath: dbPath, fs: fs, scheme: sch,
		pool: pool, legacyPool: legacyPool, legacy: legacy,
		tpl: tpl, ns: tpl.Namespace,
	}
	e.fixture = openFixtureConn(t, pool, dbPath, 5*time.Second)
	t.Cleanup(func() {
		e.stopNew()
		for _, p := range []*sqlitemigration.Pool{e.pool, e.legacyPool} {
			if p == nil {
				continue
			}
			closed := make(chan error, 1)
			go func() { closed <- p.Close() }()
			select {
			case err := <-closed:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Errorf("pool.Close did not return")
			}
		}
	})
	return e
}

func (e *migrationEnv) key(name string) string { return testCPPrefix + e.ns + "/" + name }

func (e *migrationEnv) filePath(name string) string {
	return filepath.Join(DefaultStorageRoot, e.key(name)) + GobExt
}

// plain returns a non-TS profile named name, with the UID the REST layer
// assigns before storage sees an object.
func (e *migrationEnv) plain(name string) *softwarecomposition.ContainerProfile {
	p := e.tpl.DeepCopy()
	p.Name = name
	p.ResourceVersion = ""
	p.UID = uuid.NewUUID()
	delete(p.Annotations, helpersv1.ReportSeriesIdMetadataKey)
	return p
}

// ts returns a TS profile of series suffix under base name base.
func (e *migrationEnv) ts(base, suffix string) *softwarecomposition.ContainerProfile {
	p := e.tpl.DeepCopy()
	p.Name = base + "-" + suffix
	p.ResourceVersion = ""
	p.UID = uuid.NewUUID()
	return p
}

// legacyCreate writes p through the old binary: row (rv NULL) + gob file.
func (e *migrationEnv) legacyCreate(p *softwarecomposition.ContainerProfile) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(e.t, e.legacy.Create(e.ctx, e.key(p.Name), p, out, 0))
	return out
}

// legacyUpdate mutates key through the old binary's GuaranteedUpdate: the
// file is rewritten at RV+1, then the row is INSERT OR REPLACEd (rv NULL).
func (e *migrationEnv) legacyUpdate(key string, mutate func(*softwarecomposition.ContainerProfile)) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(e.t, e.legacy.GuaranteedUpdate(e.ctx, key, out, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cp := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			mutate(cp)
			return cp, nil, nil
		}, nil))
	return out
}

func (e *migrationEnv) legacyGet(key string) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(e.t, e.legacy.Get(e.ctx, key, storage.GetOptions{}, out))
	return out
}

// startNew boots the new binary: the gate and an ObjectStore on the armed pool.
func (e *migrationEnv) startNew() {
	e.t.Helper()
	require.Nil(e.t, e.gate, "startNew called twice")
	gateCtx, cancel := context.WithTimeout(e.ctx, 30*time.Second)
	defer cancel()
	gate, err := newWriteGate(gateCtx, e.pool)
	require.NoError(e.t, err)
	e.gate = gate
	processor := NewContainerProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxContainerProfileSize: 40000}, nil)
	processor.Interval = 0
	guarded := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, e.pool, nil, e.scheme).(*StorageImpl)
	guarded.SetForeignKinds(IsContainerProfileKind)
	guarded.SetWriteGate(gate)
	store, err := NewObjectStore(e.pool, e.dbPath, nil, e.scheme, processor, guarded, gate, ObjectStoreOptions{CheckpointInterval: time.Hour})
	require.NoError(e.t, err)
	e.store = store
}

// stopNew shuts the new binary down (a rollback, or a crash-free restart).
func (e *migrationEnv) stopNew() {
	e.t.Helper()
	if e.store != nil {
		require.NoError(e.t, e.store.Close())
		e.store = nil
	}
	if e.gate != nil {
		require.NoError(e.t, e.gate.Close())
		e.gate = nil
	}
}

func (e *migrationEnv) migrate(opts ContainerProfileMigrationOptions) (*ContainerProfileMigrationReport, error) {
	e.t.Helper()
	return MigrateContainerProfiles(e.ctx, e.pool, e.gate, e.fs, DefaultStorageRoot, e.scheme, opts)
}

func (e *migrationEnv) mustMigrate(opts ContainerProfileMigrationOptions) *ContainerProfileMigrationReport {
	e.t.Helper()
	report, err := e.migrate(opts)
	require.NoError(e.t, err)
	return report
}

func (e *migrationEnv) storeGet(key string) (*softwarecomposition.ContainerProfile, error) {
	out := &softwarecomposition.ContainerProfile{}
	err := e.store.Get(e.ctx, key, storage.GetOptions{}, out)
	return out, err
}

func (e *migrationEnv) mustStoreGet(key string) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out, err := e.storeGet(key)
	require.NoError(e.t, err)
	return out
}

// storeUpdate bumps an annotation through the ObjectStore's CAS with a
// bounded ctx: a row the CAS can never match (rv NULL) times out.
func (e *migrationEnv) storeUpdate(ctx context.Context, key string) error {
	out := &softwarecomposition.ContainerProfile{}
	return e.store.GuaranteedUpdate(ctx, key, out, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cp := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			cp.Annotations["migration-test/touched"] = strconv.FormatInt(time.Now().UnixNano(), 10)
			return cp, nil, nil
		}, nil)
}

func (e *migrationEnv) inspect(key string) dbRow {
	e.t.Helper()
	return inspectRow(e.t, e.fixture, key)
}

func (e *migrationEnv) assertINV2(keys ...string) {
	e.t.Helper()
	for _, k := range keys {
		assertINV2(e.t, e.fixture, k)
	}
}

func (e *migrationEnv) migrationDone() bool {
	e.t.Helper()
	var state string
	require.NoError(e.t, sqlitex.Execute(e.fixture, `SELECT state FROM migration_state WHERE name = ?`, &sqlitex.ExecOptions{
		Args: []any{migrationStateName}, ResultFunc: func(stmt *sqlite.Stmt) error { state = stmt.ColumnText(0); return nil },
	}))
	return state == migrationStateDone
}

// seed writes n plain profiles and one two-report TS series through the old
// binary and returns every key with the object the old binary served for it.
func (e *migrationEnv) seed(n int) map[string]*softwarecomposition.ContainerProfile {
	e.t.Helper()
	before := map[string]*softwarecomposition.ContainerProfile{}
	for i := 0; i < n; i++ {
		p := e.legacyCreate(e.plain(fmt.Sprintf("plain-%02d", i)))
		before[e.key(p.Name)] = p
	}
	base := "replicaset-x-y-1111-2222"
	for _, suffix := range []string{"0001", "0002"} {
		p := e.legacyCreate(e.ts(base, suffix))
		before[e.key(p.Name)] = p
	}
	// One key at RV 2, so the migration meets a non-initial version.
	first := e.key("plain-00")
	before[first] = e.legacyUpdate(first, func(cp *softwarecomposition.ContainerProfile) {
		cp.Annotations["migration-test/updated"] = "1"
	})
	for k := range before {
		before[k] = e.legacyGet(k)
	}
	return before
}

func TestMigration_LegacyRowsBecomeObjectStoreRows(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	before := e.seed(5)
	for k := range before {
		row := e.inspect(k)
		require.True(t, row.metaExists)
		require.Nil(t, row.rv, "a legacy row has rv NULL")
		require.False(t, row.payloadExists, "a legacy row has no payloads row")
	}
	tsRowsBefore := e.inspect(e.key("replicaset-x-y-1111-2222")).tsRows
	require.Equal(t, 2, tsRowsBefore, "the old binary wrote the series' time_series rows")

	e.startNew()
	report := e.mustMigrate(ContainerProfileMigrationOptions{BatchSize: 3})
	require.Equal(t, len(before), report.Count(MigrationShapeMigrated), "%v", report.Counts)
	require.Equal(t, len(before), report.Work())
	require.Equal(t, 4, report.Batches, "7 rows in batches of 3, plus the done-flag write: %v", report.Counts)
	require.True(t, report.SweepsRun)
	require.True(t, e.migrationDone())

	keys := make([]string, 0, len(before))
	for k, legacyObj := range before {
		keys = append(keys, k)
		got := e.mustStoreGet(k)
		require.Equal(t, canonicalCP(legacyObj), canonicalCP(got), "the ObjectStore serves what the old binary served for %s", k)
		require.Equal(t, legacyObj.ResourceVersion, got.ResourceVersion, "resourceVersion preserved for %s", k)
		require.Equal(t, legacyObj.UID, got.UID)
		row := e.inspect(k)
		require.Equal(t, legacyObj.ResourceVersion, strconv.FormatInt(*row.rv, 10))
		exists, err := afero.Exists(e.fs, filepath.Join(DefaultStorageRoot, k)+GobExt)
		require.NoError(t, err)
		require.True(t, exists, "legacy files are left in place for the export tool (§8.4)")
	}
	e.assertINV2(keys...)
	require.Equal(t, tsRowsBefore, e.inspect(e.key("replicaset-x-y-1111-2222")).tsRows, "time_series rows untouched")

	// The migrated rows are live: CAS updates and a fresh Create work.
	for _, k := range keys {
		require.NoError(t, e.storeUpdate(e.ctx, k), "CAS update of migrated key %s", k)
		got := e.mustStoreGet(k)
		require.Equal(t, strconv.FormatInt(parseRV(before[k].ResourceVersion)+1, 10), got.ResourceVersion)
	}
	created := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.Create(e.ctx, e.key("fresh"), e.plain("fresh"), created, 0))
	e.assertINV2(append(keys, e.key("fresh"))...)

	// Idempotent: the second start reconciles nothing and skips the sweeps.
	again := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 0, again.Work(), "%v", again.Counts)
	require.Equal(t, 0, again.Batches)
	require.False(t, again.SweepsRun, "the done-flag gates the file sweeps")
}

func TestMigration_ReconcileShapes(t *testing.T) {
	t.Run("diverged: row ahead of file gets max+1", func(t *testing.T) {
		e := newMigrationEnv(t, afero.NewMemMapFs())
		p := e.legacyCreate(e.plain("diverged"))
		key := e.key(p.Name)
		// B16's shape: the row says 5, the file says 1.
		setRowJSON(t, e.fixture, key, `$.resourceVersion`, "5")
		e.startNew()
		report := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.Equal(t, 1, report.Count(MigrationShapeDiverged), "%v", report.Counts)
		require.Equal(t, 0, report.Count(MigrationShapeMigrated))
		got := e.mustStoreGet(key)
		require.Equal(t, "6", got.ResourceVersion, "max(rowRV, payloadRV)+1")
		e.assertINV2(key)
		require.NoError(t, e.storeUpdate(e.ctx, key))
		require.Equal(t, "7", e.mustStoreGet(key).ResourceVersion)
	})

	t.Run("completed row beats learning file (PreSave's non-TS revert)", func(t *testing.T) {
		e := newMigrationEnv(t, afero.NewMemMapFs())
		p := e.legacyCreate(e.plain("revert"))
		key := e.key(p.Name)
		require.Equal(t, helpersv1.Learning, p.Annotations[helpersv1.StatusMetadataKey])
		setRowJSON(t, e.fixture, key, fmt.Sprintf(`$.annotations."%s"`, helpersv1.StatusMetadataKey), helpersv1.Completed)
		e.startNew()
		report := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.Equal(t, 1, report.Count(MigrationShapeMigrated), "%v", report.Counts)
		require.Equal(t, helpersv1.Completed, e.mustStoreGet(key).Annotations[helpersv1.StatusMetadataKey])
		e.assertINV2(key)
	})

	t.Run("row without file is deleted", func(t *testing.T) {
		e := newMigrationEnv(t, afero.NewMemMapFs())
		p := e.legacyCreate(e.plain("nofile"))
		key := e.key(p.Name)
		require.NoError(t, e.fs.Remove(e.filePath(p.Name)))
		e.startNew()
		report := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.Equal(t, 1, report.Count(MigrationShapeRowWithoutFile), "%v", report.Counts)
		row := e.inspect(key)
		require.False(t, row.metaExists)
		require.False(t, row.payloadExists)
		_, err := e.storeGet(key)
		require.True(t, storage.IsNotFound(err))
	})

	t.Run("file without row is imported by the sweep", func(t *testing.T) {
		e := newMigrationEnv(t, afero.NewMemMapFs())
		p := e.legacyCreate(e.plain("norow"))
		key := e.key(p.Name)
		e.legacyUpdate(key, func(cp *softwarecomposition.ContainerProfile) { cp.Annotations["x"] = "y" })
		fileObj := e.legacyGet(key)
		require.Equal(t, "2", fileObj.ResourceVersion)
		_, _, kind, _, ns, name := K8sPathToKeys(key)
		require.NoError(t, sqlitex.Execute(e.fixture, `DELETE FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`, &sqlitex.ExecOptions{Args: []any{kind, ns, name}}))
		e.startNew()
		report := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.Equal(t, 1, report.Count(MigrationShapeFileWithoutRow), "%v", report.Counts)
		got := e.mustStoreGet(key)
		require.Equal(t, canonicalCP(fileObj), canonicalCP(got))
		require.Equal(t, "2", got.ResourceVersion, "imported at the file's resourceVersion")
		require.Equal(t, fileObj.UID, got.UID, "imported at the file's UID")
		e.assertINV2(key)
		require.True(t, e.migrationDone())
	})

	t.Run("staging files are removed", func(t *testing.T) {
		e := newMigrationEnv(t, afero.NewMemMapFs())
		e.legacyCreate(e.plain("keep"))
		dir := filepath.Dir(e.filePath("keep"))
		for _, n := range []string{"a.g.t", "b.g.t.1234.5"} {
			require.NoError(t, afero.WriteFile(e.fs, filepath.Join(dir, n), []byte("staged"), 0644))
		}
		e.startNew()
		report := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.Equal(t, 2, report.Count(MigrationShapeTempFile), "%v", report.Counts)
		for _, n := range []string{"a.g.t", "b.g.t.1234.5"} {
			exists, err := afero.Exists(e.fs, filepath.Join(dir, n))
			require.NoError(t, err)
			require.False(t, exists)
		}
		exists, err := afero.Exists(e.fs, e.filePath("keep"))
		require.NoError(t, err)
		require.True(t, exists)
	})

	t.Run("undecodable payloads are skipped and counted, never deleted", func(t *testing.T) {
		e := newMigrationEnv(t, afero.NewMemMapFs())
		p := e.legacyCreate(e.plain("garbage"))
		key := e.key(p.Name)
		require.NoError(t, afero.WriteFile(e.fs, e.filePath(p.Name), []byte("not a gob stream"), 0644))
		// A garbage file with no row at all.
		require.NoError(t, afero.WriteFile(e.fs, e.filePath("garbage-norow"), []byte("not a gob stream"), 0644))
		e.startNew()
		report := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.Equal(t, 2, report.Count(MigrationShapeUndecodable), "%v", report.Counts)
		require.Equal(t, 0, report.Count(MigrationShapeRowWithoutFile))
		row := e.inspect(key)
		require.True(t, row.metaExists, "the row is left as is")
		require.Nil(t, row.rv)
		require.False(t, row.payloadExists)
		for _, n := range []string{"garbage", "garbage-norow"} {
			exists, err := afero.Exists(e.fs, e.filePath(n))
			require.NoError(t, err)
			require.True(t, exists, "an undecodable file is never deleted (PM-2)")
		}
		require.False(t, e.migrationDone(), "an undecodable file without a row keeps the sweep armed for the next start")
		again := e.mustMigrate(ContainerProfileMigrationOptions{})
		require.True(t, again.SweepsRun)
		require.Equal(t, 2, again.Count(MigrationShapeUndecodable), "still reported on every start: %v", again.Counts)
	})
}

// R3 / K-1: after a rollback, the old binary's GuaranteedUpdate rewrites the
// .g file at RV+1 and INSERT OR REPLACEs the row (rv NULL). Before the
// reconcile the ObjectStore serves the stale payloads body and can never
// update the key; the next start repairs it from the FILE.
func TestMigration_R3_LegacyRewriteRepairsFromTheFile(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	before := e.seed(2)
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	key := e.key("plain-01")
	e.assertINV2(key)
	migrated := e.mustStoreGet(key)
	require.Equal(t, "1", migrated.ResourceVersion)

	// Rollback: the old binary runs and updates the key.
	e.stopNew()
	e.legacyUpdate(key, func(cp *softwarecomposition.ContainerProfile) {
		cp.Annotations["migration-test/legacy-write"] = "after-rollback"
	})
	legacyView := e.legacyGet(key)
	require.Equal(t, "2", legacyView.ResourceVersion)
	row := e.inspect(key)
	require.Nil(t, row.rv, "INSERT OR REPLACE nulled rv")
	require.Nil(t, row.uid)
	require.True(t, row.payloadExists, "the payloads row (now stale) survived the legacy write")

	// Re-enable WITHOUT the reconcile: the shape's two symptoms.
	e.startNew()
	stale := e.mustStoreGet(key)
	require.Equal(t, "1", stale.ResourceVersion, "the payloads body is the stale side")
	require.Empty(t, stale.Annotations["migration-test/legacy-write"], "the legacy write is invisible through the stale body")
	shortCtx, cancel := context.WithTimeout(e.ctx, 700*time.Millisecond)
	err := e.storeUpdate(shortCtx, key)
	cancel()
	require.Error(t, err, "rv NULL never matches the CAS: every update conflicts until the ctx expires")

	// The every-start reconcile repairs it from the file.
	report := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 1, report.Counts[MigrationShapeLegacyRewrite+"/"+MigrationSourceFile], "%v", report.Counts)
	require.Equal(t, 0, report.Counts[MigrationShapeLegacyRewrite+"/"+MigrationSourcePayloads])
	require.False(t, report.SweepsRun)
	repaired := e.mustStoreGet(key)
	require.Equal(t, canonicalCP(legacyView), canonicalCP(repaired), "the body after repair is the FILE's content, not the pre-rollback payload")
	require.Equal(t, "after-rollback", repaired.Annotations["migration-test/legacy-write"])
	require.Equal(t, "2", repaired.ResourceVersion, "rv := the JSON's resourceVersion, no +1")
	row = e.inspect(key)
	require.Equal(t, "2", row.jsonRV)
	e.assertINV2(key)
	require.NoError(t, e.storeUpdate(e.ctx, key), "the next CAS succeeds")
	require.Equal(t, "3", e.mustStoreGet(key).ResourceVersion)
	// The other key was never touched by the old binary and is not rewritten.
	other := e.key("plain-00")
	e.assertINV2(other)
	require.Equal(t, before[other].ResourceVersion, e.mustStoreGet(other).ResourceVersion)
}

// K-1's row-only variant: a legacy row write with no file (readMetadata's
// sidecar branch) keeps the payloads body as the source.
func TestMigration_R3_LegacyRewriteRowOnlyKeepsThePayloadsBody(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(1)
	key := e.key("plain-00")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	migrated := e.mustStoreGet(key)
	require.Equal(t, "2", migrated.ResourceVersion)
	e.stopNew()

	require.NoError(t, e.fs.Remove(e.filePath("plain-00")))
	_, _, kind, _, ns, name := K8sPathToKeys(key)
	require.NoError(t, sqlitex.Execute(e.fixture,
		`INSERT OR REPLACE INTO metadata (kind, namespace, name, metadata) SELECT kind, namespace, name, metadata FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{kind, ns, name}}))
	row := e.inspect(key)
	require.Nil(t, row.rv)
	require.True(t, row.payloadExists)

	e.startNew()
	report := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 1, report.Counts[MigrationShapeLegacyRewrite+"/"+MigrationSourcePayloads], "%v", report.Counts)
	repaired := e.mustStoreGet(key)
	require.Equal(t, canonicalCP(migrated), canonicalCP(repaired), "the payloads body is kept")
	require.Equal(t, "2", repaired.ResourceVersion)
	e.assertINV2(key)
	require.NoError(t, e.storeUpdate(e.ctx, key))
}

// K-2: a legacy delete after a rollback removes the row and the file and
// leaves the payloads row; every Create of the key then fails on the UNIQUE
// constraint until the mirror predicate deletes the orphan.
func TestMigration_K2_OrphanPayloadAfterLegacyDelete(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(2)
	key := e.key("plain-01")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	e.stopNew()

	require.NoError(t, e.legacy.Delete(e.ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))
	row := e.inspect(key)
	require.False(t, row.metaExists)
	require.True(t, row.payloadExists, "the old binary's delete never touches payloads")
	exists, err := afero.Exists(e.fs, e.filePath("plain-01"))
	require.NoError(t, err)
	require.False(t, exists)

	e.startNew()
	created := &softwarecomposition.ContainerProfile{}
	err = e.store.Create(e.ctx, key, e.plain("plain-01"), created, 0)
	require.Error(t, err, "Create fails on the orphan payloads row")
	require.Contains(t, err.Error(), "insert payload")

	report := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 1, report.Count(MigrationShapeOrphanPayload), "%v", report.Counts)
	require.False(t, e.inspect(key).payloadExists)
	require.NoError(t, e.store.Create(e.ctx, key, e.plain("plain-01"), created, 0), "Create succeeds after the orphan is gone")
	e.assertINV2(key, e.key("plain-00"))
}


// A crash in the middle of a batch rolls that batch back; the next start
// completes the migration from the predicate with nothing lost or duplicated.
func TestMigration_ResumesAfterCrashMidBatch(t *testing.T) {
	for _, mode := range []string{"error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			e := newMigrationEnv(t, afero.NewMemMapFs())
			before := e.seed(10) // 12 rows: batches of 5 → 5, 5, 2
			e.startNew()
			var crashed bool
			opts := ContainerProfileMigrationOptions{BatchSize: 5}
			opts.hooks.beforeStatement = func(batch, idx int, name string) error {
				if batch == 2 && idx == 3 && !crashed {
					crashed = true
					if mode == "panic" {
						panic(errInjectedCrash)
					}
					return errInjectedCrash
				}
				return nil
			}
			report, err := e.migrate(opts)
			require.Error(t, err)
			require.True(t, crashed)
			require.Contains(t, err.Error(), "batch 2 rolled back")
			require.Equal(t, 2, report.Batches)

			// Exactly the first batch is durable; the second rolled back whole.
			var done, pending int
			for k := range before {
				row := e.inspect(k)
				require.True(t, row.metaExists, "no row is lost by a crash")
				if row.rv != nil {
					require.True(t, row.payloadExists, "a committed row has its payload")
					done++
				} else {
					require.False(t, row.payloadExists, "a rolled-back row has no half-written payload")
					pending++
				}
			}
			require.Equal(t, 5, done)
			require.Equal(t, 7, pending)
			require.False(t, e.migrationDone())

			// Restart.
			e.stopNew()
			e.startNew()
			report = e.mustMigrate(ContainerProfileMigrationOptions{BatchSize: 5})
			require.Equal(t, 7, report.Count(MigrationShapeMigrated), "only the pending rows are reconciled: %v", report.Counts)
			require.Equal(t, 0, report.Count(MigrationShapeDiverged))
			require.True(t, e.migrationDone())
			keys := make([]string, 0, len(before))
			for k, legacyObj := range before {
				keys = append(keys, k)
				got := e.mustStoreGet(k)
				require.Equal(t, canonicalCP(legacyObj), canonicalCP(got))
				require.Equal(t, legacyObj.ResourceVersion, got.ResourceVersion)
			}
			e.assertINV2(keys...)
			require.Equal(t, 0, e.mustMigrate(ContainerProfileMigrationOptions{}).Work())
		})
	}
}

// The re-exec variant: the child process is killed (os.Exit inside the gated
// transaction) with real files on disk; the parent resumes.
func TestMigration_ResumesAfterProcessKill(t *testing.T) {
	if os.Getenv("CP_MIGRATION_CRASH_DIR") != "" {
		t.Skip("child helper only")
	}
	base := t.TempDir()
	fs := afero.NewBasePathFs(afero.NewOsFs(), base)
	e := newMigrationEnv(t, fs)
	before := e.seed(10)
	// Hand the database to the child: close our pools' hold on it first.
	require.NoError(t, e.legacyPool.Close())
	require.NoError(t, e.pool.Close())
	e.pool, e.legacyPool = nil, nil

	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(), "CP_MIGRATION_CRASH_DIR="+base, "CP_MIGRATION_CRASH_DB="+e.dbPath)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "the child must die: %v\n%s", err, out)
	require.Equal(t, 3, exitErr.ExitCode(), "the child exits from inside the batch\n%s", out)
	require.Contains(t, string(out), "child: crashing inside batch 2")

	// The parent boots a new binary on the same files and database.
	pool := NewPoolWithOptions(e.dbPath, PoolOptions{Size: 4, BusyTimeout: 5 * time.Second, DisableAutoCheckpoint: true})
	armUngatedWriteCheck(t, pool)
	gate, err := newWriteGate(e.ctx, pool)
	require.NoError(t, err)
	processor := NewContainerProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxContainerProfileSize: 40000}, nil)
	processor.Interval = 0
	guarded := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, e.scheme).(*StorageImpl)
	guarded.SetForeignKinds(IsContainerProfileKind)
	guarded.SetWriteGate(gate)
	store, err := NewObjectStore(pool, e.dbPath, nil, e.scheme, processor, guarded, gate, ObjectStoreOptions{CheckpointInterval: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
		require.NoError(t, gate.Close())
		require.NoError(t, pool.Close())
	})
	fixture := openFixtureConn(t, pool, e.dbPath, 5*time.Second)

	done := 0
	for k := range before {
		row := inspectRow(t, fixture, k)
		require.True(t, row.metaExists)
		require.Equal(t, row.rv != nil, row.payloadExists, "no half-written key after the kill")
		if row.rv != nil {
			done++
		}
	}
	require.Equal(t, 5, done, "exactly the child's first batch is durable")

	report, err := MigrateContainerProfiles(e.ctx, pool, gate, fs, DefaultStorageRoot, e.scheme, ContainerProfileMigrationOptions{BatchSize: 5})
	require.NoError(t, err)
	require.Equal(t, 7, report.Count(MigrationShapeMigrated), "%v", report.Counts)
	for k, legacyObj := range before {
		assertINV2(t, fixture, k)
		got := &softwarecomposition.ContainerProfile{}
		require.NoError(t, store.Get(e.ctx, k, storage.GetOptions{}, got))
		require.Equal(t, canonicalCP(legacyObj), canonicalCP(got))
		require.Equal(t, legacyObj.ResourceVersion, got.ResourceVersion)
	}
}

// TestMigrationCrashChild is TestMigration_ResumesAfterProcessKill's child:
// it runs the migration on the parent's files and exits from inside the
// second batch's transaction.
func TestMigrationCrashChild(t *testing.T) {
	base := os.Getenv("CP_MIGRATION_CRASH_DIR")
	if base == "" {
		t.Skip("child helper only")
	}
	dbPath := os.Getenv("CP_MIGRATION_CRASH_DB")
	fs := afero.NewBasePathFs(afero.NewOsFs(), base)
	pool := NewPoolWithOptions(dbPath, PoolOptions{Size: 4, BusyTimeout: 5 * time.Second, DisableAutoCheckpoint: true})
	sch := runtime.NewScheme()
	install.Install(sch)
	gate, err := newWriteGate(context.Background(), pool)
	require.NoError(t, err)
	opts := ContainerProfileMigrationOptions{BatchSize: 5}
	opts.hooks.beforeStatement = func(batch, idx int, name string) error {
		if batch == 2 && idx == 3 {
			fmt.Printf("child: crashing inside batch 2 before %s\n", name)
			os.Exit(3)
		}
		return nil
	}
	_, err = MigrateContainerProfiles(context.Background(), pool, gate, fs, DefaultStorageRoot, sch, opts)
	require.NoError(t, err)
	t.Fatal("the child must not reach the end of the migration")
}

// Dry-run reconciles and counts the same shapes and writes nothing.
func TestMigration_DryRunWritesNothing(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	before := e.seed(3)
	// An orphan payloads row and a staging file, seeded through the fixture.
	require.NoError(t, sqlitex.Execute(e.fixture,
		`INSERT INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{ContainerProfileKind, e.ns, "orphan", PayloadEncodingJSONV1Beta1, []byte("{}")}}))
	staged := filepath.Join(filepath.Dir(e.filePath("plain-00")), "staged.g.t")
	require.NoError(t, afero.WriteFile(e.fs, staged, []byte("staged"), 0644))

	// No gate exists in a flag-off dry run.
	dry, err := MigrateContainerProfiles(e.ctx, e.pool, nil, e.fs, DefaultStorageRoot, e.scheme, ContainerProfileMigrationOptions{DryRun: true})
	require.NoError(t, err)
	require.True(t, dry.DryRun)
	require.Equal(t, len(before), dry.Count(MigrationShapeMigrated), "%v", dry.Counts)
	require.Equal(t, 1, dry.Count(MigrationShapeOrphanPayload))
	require.Equal(t, 1, dry.Count(MigrationShapeTempFile))
	require.True(t, dry.SweepsRun)
	for k := range before {
		row := e.inspect(k)
		require.Nil(t, row.rv, "dry-run wrote nothing")
		require.False(t, row.payloadExists)
	}
	require.True(t, e.inspect(e.key("orphan")).payloadExists)
	exists, err := afero.Exists(e.fs, staged)
	require.NoError(t, err)
	require.True(t, exists)
	require.False(t, e.migrationDone())

	_, err = MigrateContainerProfiles(e.ctx, e.pool, nil, e.fs, DefaultStorageRoot, e.scheme, ContainerProfileMigrationOptions{})
	require.Error(t, err, "a real run needs the gate")

	e.startNew()
	real := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, dry.Counts, real.Counts, "the dry run predicted the real run")
	require.True(t, e.migrationDone())
}

// The migration's batches are gated writes: with the gate held by another
// writer the migration waits its turn rather than busy-waiting on SQLite's
// lock (AC-G1 is armed on the env's pool and would fail an ungated batch).
func TestMigration_BatchesAreGatedWrites(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(2)
	e.startNew()
	holderIn := make(chan struct{})
	release := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- e.gate.run(e.ctx, priorityHigh, "test-holder", "test", func(_ context.Context, _ *sqlite.Conn) error {
			close(holderIn)
			<-release
			return nil
		})
	}()
	<-holderIn
	migrated := make(chan *ContainerProfileMigrationReport, 1)
	go func() { migrated <- e.mustMigrate(ContainerProfileMigrationOptions{}) }()
	select {
	case <-migrated:
		t.Fatal("the migration committed while another writer held the gate")
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-holderDone)
	select {
	case report := <-migrated:
		require.Equal(t, 4, report.Count(MigrationShapeMigrated), "%v", report.Counts)
	case <-time.After(10 * time.Second):
		t.Fatal("the migration did not proceed after the gate was released")
	}
}

// setRowJSON sets one json path of key's metadata JSON through the fixture
// (a B16-style edit of the row only).
func setRowJSON(t *testing.T, conn *sqlite.Conn, key, path, value string) {
	t.Helper()
	_, _, kind, _, ns, name := K8sPathToKeys(key)
	require.NoError(t, sqlitex.Execute(conn,
		`UPDATE metadata SET metadata = json_set(CAST(metadata AS TEXT), ?, ?) WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{path, value, kind, ns, name}}))
	require.Equal(t, int64(1), int64(conn.Changes()))
}
