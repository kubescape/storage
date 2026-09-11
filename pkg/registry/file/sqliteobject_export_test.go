package file

// The reverse export (§8.4: export-then-downgrade) and the danger it closes.

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitex"
)

func (e *migrationEnv) export(opts ContainerProfileExportOptions) *ContainerProfileExportReport {
	e.t.Helper()
	report, err := ExportContainerProfiles(e.ctx, e.pool, e.fs, DefaultStorageRoot, e.scheme, opts)
	require.NoError(e.t, err)
	return report
}

func (e *migrationEnv) storeCreate(name string) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(e.t, e.store.Create(e.ctx, e.key(name), e.plain(name), out, 0))
	return out
}

func (e *migrationEnv) readFile(name string) []byte {
	e.t.Helper()
	b, err := afero.ReadFile(e.fs, e.filePath(name))
	require.NoError(e.t, err)
	return b
}

func decodeGob(t *testing.T, b []byte) *softwarecomposition.ContainerProfile {
	t.Helper()
	obj := &softwarecomposition.ContainerProfile{}
	require.NoError(t, gob.NewDecoder(bytes.NewReader(b)).Decode(obj))
	return obj
}

// The danger, on this branch's code, without the export: an older binary
// opening the migrated database destroys the rows the ObjectStore wrote and
// nulls rv/uid on the rows it rewrites.
func TestExport_DangerWithoutExport_OldBinaryDestroysNewStoreRows(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(2)
	updated, created := e.key("plain-00"), e.key("created-under-the-new-store")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	e.storeCreate("created-under-the-new-store")
	require.NoError(t, e.storeUpdate(e.ctx, updated))
	require.Equal(t, "3", e.mustStoreGet(updated).ResourceVersion)
	e.assertINV2(updated, created)
	exists, err := afero.Exists(e.fs, e.filePath("created-under-the-new-store"))
	require.NoError(t, err)
	require.False(t, exists, "a key created under the new store has no file")

	// Downgrade without the export: the old binary runs.
	e.stopNew()

	// (1) A key with no file: get() deletes the metadata row — the object is
	// destroyed, and its payloads row is left orphaned.
	_, err = e.storeGetLegacy(created)
	require.True(t, storage.IsNotFound(err))
	row := e.inspect(created)
	require.False(t, row.metaExists, "the old binary's self-repair deleted the row")
	require.True(t, row.payloadExists, "and left the payload orphaned")

	// (2) A key the new store updated: the old binary serves the STALE file
	// while the row says otherwise — a divergent pair.
	stale := e.legacyGet(updated)
	require.Equal(t, "2", stale.ResourceVersion, "the file is the pre-flip version")
	require.Equal(t, "3", e.inspect(updated).jsonRV, "the row is the post-flip version")

	// (3) The old binary's row write, exactly as its writeMetadata issues it,
	// nulls rv/uid: the CAS can never match again.
	other := e.key("plain-01")
	_, _, kind, _, ns, name := K8sPathToKeys(other)
	require.NoError(t, sqlitex.Execute(e.fixture,
		`INSERT OR REPLACE INTO metadata (kind, namespace, name, metadata) SELECT kind, namespace, name, metadata FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{kind, ns, name}}))
	row = e.inspect(other)
	require.Nil(t, row.rv, "INSERT OR REPLACE nulled rv")
	require.Nil(t, row.uid, "INSERT OR REPLACE nulled uid")
	e.startNew()
	shortCtx, cancel := context.WithTimeout(e.ctx, 700*time.Millisecond)
	err = e.storeUpdate(shortCtx, other)
	cancel()
	require.Error(t, err, "rv NULL never matches the CAS")
}

// storeGetLegacy is the old binary's Get.
func (e *migrationEnv) storeGetLegacy(key string) (*softwarecomposition.ContainerProfile, error) {
	out := &softwarecomposition.ContainerProfile{}
	err := e.legacy.Get(e.ctx, key, storage.GetOptions{}, out)
	return out, err
}

// The round trip: legacy → migrate → export → the old binary serves every
// object exactly as it did before the migration, at the same RV and UID,
// and can keep writing it.
//
// Bytes are compared as decoded objects, not raw: gob encodes maps
// (labels, annotations) in Go's randomised map order, so two encodes of the
// same object differ byte-wise; and the JSON codec keeps creationTimestamp
// to the second and empty collections as empty (canonicalCP), both
// documented codec deltas of the backend. Everything the old binary reads
// back is asserted equal.
func TestExport_RoundTripIsBehaviourallyIdentical(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	before := e.seed(4)
	original := map[string][]byte{}
	for k := range before {
		original[k] = e.readFile(filepath.Base(k))
	}

	e.startNew()
	report := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, len(before), report.Count(MigrationShapeMigrated))
	exported := e.export(ContainerProfileExportOptions{BatchSize: 3})
	require.Equal(t, len(before), exported.Exported)
	require.Equal(t, 0, exported.LegacySkipped)
	require.Equal(t, 0, exported.Undecodable)
	e.stopNew()

	for k, legacyBefore := range before {
		name := filepath.Base(k)
		fileNow := e.readFile(name)
		require.Equal(t, canonicalCP(decodeGob(t, original[k])), canonicalCP(decodeGob(t, fileNow)), "the exported file decodes to the original for %s", k)
		got := e.legacyGet(k)
		require.Equal(t, canonicalCP(legacyBefore), canonicalCP(got), "the old binary serves the pre-migration object for %s", k)
		require.Equal(t, legacyBefore.ResourceVersion, got.ResourceVersion)
		require.Equal(t, legacyBefore.UID, got.UID)
		staged, err := afero.Exists(e.fs, e.filePath(name)+".t")
		require.NoError(t, err)
		require.False(t, staged, "no staging file left behind")
	}
	// The old binary keeps working on the exported files.
	k := e.key("plain-01")
	after := e.legacyUpdate(k, func(cp *softwarecomposition.ContainerProfile) { cp.Annotations["export-test"] = "old-binary-write" })
	require.Equal(t, "2", after.ResourceVersion)
	require.Equal(t, "old-binary-write", e.legacyGet(k).Annotations["export-test"])
}

// The full rollback cycle in the design's order: export, downgrade, the old
// binary reads and writes, re-enable — the reconcile repairs what the old
// binary rewrote and nothing is lost.
func TestExport_ThenDowngradeThenReEnable(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(2)
	updated, created, untouched := e.key("plain-00"), e.key("created-under-the-new-store"), e.key("plain-01")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	e.storeCreate("created-under-the-new-store")
	require.NoError(t, e.storeUpdate(e.ctx, updated))
	newView := map[string]*softwarecomposition.ContainerProfile{
		updated: e.mustStoreGet(updated), created: e.mustStoreGet(created), untouched: e.mustStoreGet(untouched),
	}

	// Export, then downgrade.
	report := e.export(ContainerProfileExportOptions{})
	require.Equal(t, 5, report.Exported, "2 plain + 2 TS seeded, 1 created")
	e.stopNew()

	// The old binary sees every object at its post-flip state.
	for k, want := range newView {
		got := e.legacyGet(k)
		require.Equal(t, canonicalCP(want), canonicalCP(got), "%s", k)
		require.Equal(t, want.ResourceVersion, got.ResourceVersion)
		require.Equal(t, want.UID, got.UID)
	}
	require.True(t, e.inspect(created).metaExists, "nothing was deleted")

	// The old binary writes one key and deletes another.
	e.legacyUpdate(created, func(cp *softwarecomposition.ContainerProfile) { cp.Annotations["export-test"] = "old-binary-write" })
	require.Nil(t, e.inspect(created).rv)
	require.NoError(t, e.legacy.Delete(e.ctx, untouched, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))
	require.True(t, e.inspect(untouched).payloadExists, "the old binary's delete leaves the payloads row")

	// Re-enable: the every-start reconcile repairs both shapes.
	e.startNew()
	again := e.mustMigrate(ContainerProfileMigrationOptions{})
	require.Equal(t, 1, again.Counts[MigrationShapeLegacyRewrite+"/"+MigrationSourceFile], "%v", again.Counts)
	require.Equal(t, 1, again.Count(MigrationShapeOrphanPayload), "%v", again.Counts)
	require.Equal(t, 0, again.Count(MigrationShapeRowWithoutFile))
	e.assertINV2(updated, created, untouched)
	got := e.mustStoreGet(created)
	require.Equal(t, "old-binary-write", got.Annotations["export-test"], "the old binary's write survives the re-enable")
	require.Equal(t, "2", got.ResourceVersion)
	require.Equal(t, canonicalCP(newView[updated]), canonicalCP(e.mustStoreGet(updated)), "an untouched key is exactly as the new store left it")
	_, err := e.storeGet(untouched)
	require.True(t, storage.IsNotFound(err), "the old binary's delete holds")
	require.NoError(t, e.storeUpdate(e.ctx, created), "the CAS is live again")
	require.NoError(t, e.storeUpdate(e.ctx, updated))
}

func TestExport_SkipsLegacyRowsAndDryRunWritesNothing(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(1)
	migratedKey := e.key("plain-00")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	e.storeCreate("created-under-the-new-store")
	// A row a legacy writer already rewrote (rv NULL): its file is the legacy
	// writer's, and the export must not overwrite it with the stale body.
	e.stopNew()
	e.legacyUpdate(migratedKey, func(cp *softwarecomposition.ContainerProfile) { cp.Annotations["export-test"] = "legacy" })
	legacyFile := e.readFile("plain-00")

	dry := e.export(ContainerProfileExportOptions{DryRun: true})
	require.Equal(t, 3, dry.Exported, "1 plain + 2 TS seeded, 1 created, 1 legacy-rewritten")
	require.Equal(t, 1, dry.LegacySkipped)
	exists, err := afero.Exists(e.fs, e.filePath("created-under-the-new-store"))
	require.NoError(t, err)
	require.False(t, exists, "dry-run wrote nothing")

	real := e.export(ContainerProfileExportOptions{})
	require.Equal(t, 3, real.Exported)
	require.Equal(t, 1, real.LegacySkipped)
	require.Equal(t, legacyFile, e.readFile("plain-00"), "the legacy writer's file is untouched")

	// An rv NULL row with NO file (no known producer): the body is the only
	// copy, exported at the JSON's resourceVersion rather than skipped.
	require.NoError(t, e.fs.Remove(e.filePath("plain-00")))
	filled := e.export(ContainerProfileExportOptions{})
	require.Equal(t, 4, filled.Exported)
	require.Equal(t, 0, filled.LegacySkipped)
	filledObj := e.legacyGet(migratedKey)
	require.Equal(t, "3", filledObj.ResourceVersion, "the row JSON's version (seed updated it to 2, the legacy write to 3)")
	require.Empty(t, filledObj.Annotations["export-test"], "the payloads body (pre-legacy-write) is what was left")
	got := e.legacyGet(e.key("created-under-the-new-store"))
	require.Equal(t, "1", got.ResourceVersion)
	require.Equal(t, helpersv1.Learning, got.Annotations[helpersv1.StatusMetadataKey])
}

// TestExport_RemovesStaleFileOfKeyDeletedUnderTheNewStore reproduces the
// rollback-resurrection sequence: migrate A, delete A under the ObjectStore
// backend (which removes only the database rows and leaves A's pre-flip .g
// file on disk), export, downgrade. Without reconcileStaleExportedFiles, the
// old binary's get() finds the stale file and A is readable again after
// being deleted.
func TestExport_RemovesStaleFileOfKeyDeletedUnderTheNewStore(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(1)
	key := e.key("plain-00")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})

	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.Delete(e.ctx, key, out, nil, nil, nil, storage.DeleteOptions{}))
	row := e.inspect(key)
	require.False(t, row.metaExists, "the delete removed the metadata row")
	require.False(t, row.payloadExists, "and the payloads row")
	exists, err := afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.True(t, exists, "the pre-flip legacy file survives an ObjectStore delete")

	report := e.export(ContainerProfileExportOptions{})
	require.Equal(t, 1, report.StaleFilesRemoved)
	exists, err = afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.False(t, exists, "the export removed the stale file")

	e.stopNew()
	getOut := &softwarecomposition.ContainerProfile{}
	err = e.legacy.Get(e.ctx, key, storage.GetOptions{}, getOut)
	require.True(t, storage.IsNotFound(err), "the old binary must not resurrect the deleted object")
}

// TestExport_DryRunLeavesStaleFiles confirms the dry-run count matches what
// a real run would remove, without touching the filesystem.
func TestExport_DryRunLeavesStaleFiles(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(1)
	key := e.key("plain-00")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.Delete(e.ctx, key, out, nil, nil, nil, storage.DeleteOptions{}))

	dry := e.export(ContainerProfileExportOptions{DryRun: true})
	require.Equal(t, 1, dry.StaleFilesRemoved)
	exists, err := afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.True(t, exists, "dry-run removed nothing")
}

// TestExport_TrailingSlashRootDoesNotDeleteLiveFiles: reconcileStaleExportedFiles
// derives a key by slicing a Walk()-reported path (always cleaned, via
// filepath.Join) at len(root). An uncleaned root with a trailing slash (e.g.
// the CLI's -root /data/) makes that length one too many, silently dropping
// the key's required leading '/' -- ReadMetadata then misses the live row
// for a key that was NEVER deleted, and the stale-file pass wrongly deletes
// its just-exported file. Both a live object and a genuinely deleted one are
// present so the fix is proven both ways: the live file must survive, the
// deleted one's stale file must still be removed.
func TestExport_TrailingSlashRootDoesNotDeleteLiveFiles(t *testing.T) {
	e := newMigrationEnv(t, afero.NewMemMapFs())
	e.seed(2)
	liveKey, deletedKey := e.key("plain-00"), e.key("plain-01")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})

	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.Delete(e.ctx, deletedKey, out, nil, nil, nil, storage.DeleteOptions{}))
	liveExists, err := afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.True(t, liveExists, "plain-00's row is live, its pre-flip file still on disk")

	report, err := ExportContainerProfiles(e.ctx, e.pool, e.fs, DefaultStorageRoot+"/", e.scheme, ContainerProfileExportOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, report.StaleFilesRemoved, "only plain-01's stale file, not plain-00's live one")

	liveExists, err = afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.True(t, liveExists, "the trailing slash must not make the live file look stale")

	deletedExists, err := afero.Exists(e.fs, e.filePath("plain-01"))
	require.NoError(t, err)
	require.False(t, deletedExists, "the genuinely deleted key's stale file is still removed")

	e.stopNew()
	getOut := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.legacy.Get(e.ctx, liveKey, storage.GetOptions{}, getOut), "the live object must still be readable by an old binary")
}

// failOpenFs fails Open for one target path while armed, otherwise
// delegates -- reproduces a permission-denied/I/O error reading a specific
// file's content (what decodeLegacyFileAt hits), distinct from toggleFailFs
// (migration test helper, same package) which fails Stat for directory/walk
// -level errors. afero.Walk itself never calls Open, only Stat/ReadDir, so
// arming this on a file already discovered by Walk reproduces the failure
// happening exactly where reconcileStaleExportedFiles decodes it.
type failOpenFs struct {
	afero.Fs
	target string
	fail   atomic.Bool
}

func (f *failOpenFs) Open(name string) (afero.File, error) {
	if f.fail.Load() && name == f.target {
		return nil, fmt.Errorf("failOpenFs: simulated open failure for %s", name)
	}
	return f.Fs.Open(name)
}

// TestExport_ReconcileTopLevelStatErrorFailsExport: the same class of bug as
// TestMigration_SweepTopLevelStatErrorDoesNotMarkDone, on the export side --
// afero.DirExists returns exists=false on ANY stat error, not just "does
// not exist", and reconcileStaleExportedFiles used to read that as "nothing
// to reconcile" and report success. A stat error there must instead fail
// the export, since a directory that could not be checked might hold a
// stale, resurrection-capable file the export never got to look at.
func TestExport_ReconcileTopLevelStatErrorFailsExport(t *testing.T) {
	failing := &toggleFailFs{Fs: afero.NewMemMapFs()}
	e := newMigrationEnv(t, failing)
	e.seed(1)
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})

	failing.target = filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, ContainerProfileKind)
	failing.fail.Store(true)
	_, err := ExportContainerProfiles(e.ctx, e.pool, failing, DefaultStorageRoot, e.scheme, ContainerProfileExportOptions{})
	require.Error(t, err, "a stat error checking the reconcile directory must fail the export, not read as \"nothing to reconcile\"")
}

// TestExport_ReconcileFileAccessErrorFailsExport: a permission/I/O error
// opening a stale candidate's file (its metadata row is gone, its file
// still exists) used to be treated identically to the file's content being
// genuinely undecodable garbage -- reconcileStaleExportedFiles left it in
// place and the export still reported success. But an access error means
// the tool could not tell whether that file is content an old binary would
// resurrect; unlike truly undecodable content (safe to leave, an old binary
// can't read it either), it must fail the export instead of silently
// leaving a landmine an operator believes was checked.
func TestExport_ReconcileFileAccessErrorFailsExport(t *testing.T) {
	failing := &failOpenFs{Fs: afero.NewMemMapFs()}
	e := newMigrationEnv(t, failing)
	e.seed(1)
	key := e.key("plain-00")
	e.startNew()
	e.mustMigrate(ContainerProfileMigrationOptions{})

	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.Delete(e.ctx, key, out, nil, nil, nil, storage.DeleteOptions{}))
	exists, err := afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.True(t, exists, "the pre-flip legacy file survives an ObjectStore delete")

	failing.target = e.filePath("plain-00")
	failing.fail.Store(true)
	_, err = ExportContainerProfiles(e.ctx, e.pool, failing, DefaultStorageRoot, e.scheme, ContainerProfileExportOptions{})
	require.Error(t, err, "an access error on a stale candidate must fail the export, not be treated as harmless undecodable content")

	failing.fail.Store(false)
	exists, err = afero.Exists(e.fs, e.filePath("plain-00"))
	require.NoError(t, err)
	require.True(t, exists, "the file was never classified, so it must not have been removed either")
}
