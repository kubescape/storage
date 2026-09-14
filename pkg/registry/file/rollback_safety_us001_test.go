package file

// Tests for US-001 of .omc/plans/rollback-safety-guard.md, Part 1: the
// shared DeletePayloads helper wired into both delete paths --
// deleteLocked's non-gated arm (crash-safety ordering: payloads before
// metadata) and repairDelete (the single function shared by all 4 of get()'s
// self-repair call sites).

import (
	"context"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// insertPayloadsRowForTest seeds a payloads row for key directly, mirroring
// what an ObjectStore-era write (or a pre-existing orphan) would have left
// behind. The encoding/body content is irrelevant to these tests -- only the
// row's presence/absence is asserted.
func insertPayloadsRowForTest(t *testing.T, conn *sqlite.Conn, key string) {
	t.Helper()
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	require.NoError(t, sqlitex.Execute(conn,
		`INSERT INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{kind, namespace, name, "json", []byte("{}")}}))
}

func payloadsRowExistsForTest(t *testing.T, conn *sqlite.Conn, key string) bool {
	t.Helper()
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	exists := false
	require.NoError(t, sqlitex.Execute(conn,
		`SELECT 1 FROM payloads WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{
			Args:       []any{kind, namespace, name},
			ResultFunc: func(*sqlite.Stmt) error { exists = true; return nil },
		}))
	return exists
}

func newRollbackSafetyTestStorage(t *testing.T) (*StorageImpl, *sqlitemigration.Pool, afero.Fs) {
	t.Helper()
	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, sch).(*StorageImpl)
	return s, pool, fs
}

// TestDelete_RemovesPayloadsRowBeforeMetadataRow is Part 1's ordering
// guarantee, exercised directly: a crash-simulated partial delete (the
// payloads row removed, the metadata row not yet -- deleteLocked's fixed
// ordering, storage.go's non-gated arm) must leave the key in today's
// existing safe self-repair shape. A subsequent GET on that key must behave
// exactly like a legacy row with no file and no payloads: it self-repairs
// (NotFound), and is never resurrected or corrupted.
func TestDelete_RemovesPayloadsRowBeforeMetadataRow(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	obj := &v1beta1.SBOMSyft{ObjectMeta: v1.ObjectMeta{Name: "toto"}}

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	// Seed a metadata row and a payloads row with no payload file on disk --
	// the shape of an ObjectStore-era row under flag-off, the one live path
	// deleteLocked's non-gated arm actually deletes.
	require.NoError(t, writeMetadata(conn, key, obj))
	insertPayloadsRowForTest(t, conn, key)

	// Simulate the crash: only the first of deleteLocked's two SQLite
	// deletes (payloads, in the fixed order) has committed; the process
	// dies before the metadata delete runs.
	require.NoError(t, DeletePayloads(conn, key))
	pool.Put(conn)

	// Surviving state: metadata row present, no payload file, no payloads
	// row -- exactly today's existing, already-correct self-repair case.
	out := &v1beta1.SBOMSyft{}
	getErr := s.Get(ctx, key, storage.GetOptions{}, out)
	assert.True(t, storage.IsNotFound(getErr), "expected NotFound after crash-simulated partial delete, got %v", getErr)

	// Confirm self-repair actually ran and did not resurrect or corrupt the
	// key: the orphaned metadata row must be gone too, and no payloads row
	// left behind.
	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	_, mErr := ReadMetadata(conn, key)
	assert.ErrorIs(t, mErr, ErrMetadataNotFound, "metadata row must have been cleaned up by self-repair")
	assert.False(t, payloadsRowExistsForTest(t, conn, key), "no payloads row must remain")
	pool.Put(conn)

	// A second GET must behave identically: still NotFound, not resurrected.
	getErr2 := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	assert.True(t, storage.IsNotFound(getErr2))
}

// TestRepairDelete_RemovesPayloadsRowAtAllFourCallSites is table-driven
// across the 4 sites in storage.go's get() that call repairDelete
// (~1017/1092/1153/1315 in the plan's line numbering): the missing-payload-
// file branch, the gob-EOF ("irrecoverable" decode error) branch, and the
// two migration-tool-failure branches (migrateObject for the hasWriteLock
// caller state, migrateObjectUnlocked for the noLock/hasReadLock states).
// Each must leave no orphaned payloads row behind once repairDelete fires.
func TestRepairDelete_RemovesPayloadsRowAtAllFourCallSites(t *testing.T) {
	t.Run("missing payload file (get() afero.ErrFileNotFound branch)", func(t *testing.T) {
		s, pool, _ := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/site1"
		obj := &v1beta1.SBOMSyft{ObjectMeta: v1.ObjectMeta{Name: "site1"}}

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		require.NoError(t, writeMetadata(conn, key, obj))
		insertPayloadsRowForTest(t, conn, key)
		pool.Put(conn)
		// No payload file written: this is the missing-file branch.

		getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

		conn, err = pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, payloadsRowExistsForTest(t, conn, key), "payloads row must be gone after repairDelete")
		pool.Put(conn)
	})

	t.Run("gob EOF on decode (get() irrecoverable-error branch)", func(t *testing.T) {
		s, pool, fs := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/site2"
		obj := &v1beta1.SBOMSyft{ObjectMeta: v1.ObjectMeta{Name: "site2"}}

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		require.NoError(t, writeMetadata(conn, key, obj))
		insertPayloadsRowForTest(t, conn, key)
		pool.Put(conn)
		// An empty payload file: gob.Decode returns io.EOF immediately,
		// matching get()'s io.ErrUnexpectedEOF/io.EOF branch.
		require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), []byte{}, 0644))

		getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

		conn, err = pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, payloadsRowExistsForTest(t, conn, key), "payloads row must be gone after repairDelete")
		pool.Put(conn)
	})

	t.Run("migration tool failure, hasWriteLock caller (migrateObject branch)", func(t *testing.T) {
		installFakeMigrationTool(t)
		t.Setenv("MIGRATION_FAKE_FAIL", "1")

		// This call site (storage.go's GuaranteedUpdateWithConn -> s.get
		// with hasWriteLock) is only reachable when singleWriterEnabled is
		// off; guaranteedUpdateSingleWriter never calls get() with
		// hasWriteLock.
		old := singleWriterEnabled
		singleWriterEnabled = false
		t.Cleanup(func() { singleWriterEnabled = old })

		s, pool, fs := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/site3"

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		insertPayloadsRowForTest(t, conn, key)
		pool.Put(conn)
		require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), gobPayloadNeedingMigration(t), 0644))

		tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
			t.Fatal("tryUpdate must not run: getCurrentState should fail with NotFound before reaching it")
			return nil, nil, nil
		}
		updErr := s.GuaranteedUpdate(ctx, key, &v1beta1.SBOMSyft{}, false, nil, tryUpdate, nil)
		assert.True(t, storage.IsNotFound(updErr), "got %v", updErr)

		conn, err = pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, payloadsRowExistsForTest(t, conn, key), "payloads row must be gone after repairDelete")
		pool.Put(conn)
	})

	t.Run("migration tool failure, noLock caller (migrateObjectUnlocked branch)", func(t *testing.T) {
		installFakeMigrationTool(t)
		t.Setenv("MIGRATION_FAKE_FAIL", "1")

		s, pool, fs := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/site4"

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		insertPayloadsRowForTest(t, conn, key)
		pool.Put(conn)
		require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), gobPayloadNeedingMigration(t), 0644))

		getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

		conn, err = pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, payloadsRowExistsForTest(t, conn, key), "payloads row must be gone after repairDelete")
		pool.Put(conn)
	})
}
