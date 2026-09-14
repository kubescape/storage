package file

// Tests for US-002 of .omc/plans/rollback-safety-guard.md, Part 2: the
// read-time fallback at get()'s missing-payload-file branch, and its exact
// 4-condition predicate --
//
//	(1) a metadata row exists, (2) rv IS NOT NULL, (3) is_time_series = 0,
//	(4) a payloads row exists
//
// checked in that order, short-circuiting on the first failure. Every
// negative case below must land on today's existing self-repair path,
// unchanged.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
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

const (
	testFallbackRV  = int64(4242)
	testFallbackUID = "11111111-2222-3333-4444-555555555555"
)

// seedMetadataRow inserts a metadata row for key carrying the exact rv / uid /
// is_time_series column values the predicate reads. rvNull seeds the shape
// every legacy write leaves behind (WriteJSON's INSERT OR REPLACE omits the
// rv column, so the row's rv is NULL).
func seedMetadataRow(t *testing.T, conn *sqlite.Conn, key string, obj runtime.Object, rv int64, rvNull bool, uid string, isTimeSeries bool) {
	t.Helper()
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	metadataJSON, err := json.Marshal(obj)
	require.NoError(t, err)
	var rvArg any
	if !rvNull {
		rvArg = rv
	}
	require.NoError(t, sqlitex.Execute(conn,
		`INSERT OR REPLACE INTO metadata (kind, namespace, name, metadata, rv, uid, is_time_series)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{kind, namespace, name, string(metadataJSON), rvArg, uid, isTimeSeries}}))
}

// seedDecodablePayloadsRow inserts a payloads row whose body is exactly what
// an ObjectStore write would have stored for obj (encodePayloadBody, at the
// json/v1beta1 encoding decodePayloadBody requires).
func seedDecodablePayloadsRow(t *testing.T, conn *sqlite.Conn, s *StorageImpl, key string, obj runtime.Object) {
	t.Helper()
	body, err := encodePayloadBody(s.scheme, obj)
	require.NoError(t, err)
	seedRawPayloadsRow(t, conn, key, PayloadEncodingJSONV1Beta1, body)
}

func seedRawPayloadsRow(t *testing.T, conn *sqlite.Conn, key, encoding string, body []byte) {
	t.Helper()
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	require.NoError(t, sqlitex.Execute(conn,
		`INSERT OR REPLACE INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{kind, namespace, name, encoding, body}}))
}

func metadataRowExistsForTest(t *testing.T, conn *sqlite.Conn, key string) bool {
	t.Helper()
	_, err := ReadMetadata(conn, key)
	if err == nil {
		return true
	}
	require.ErrorIs(t, err, ErrMetadataNotFound)
	return false
}

// fallbackTestObject is the object the fallback-eligible payloads body holds:
// content distinctive enough that serving it is unmistakable in an assertion.
func fallbackTestObject(name string) *v1beta1.SBOMSyft {
	return &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name:        name,
			Namespace:   "kubescape",
			Annotations: map[string]string{"kubescape.io/status": "completed", "kubescape.io/completion": "full"},
		},
		Spec: v1beta1.SBOMSyftSpec{
			Metadata: v1beta1.SPDXMeta{Tool: v1beta1.ToolMeta{Name: "payloads-body", Version: "v1"}},
		},
	}
}

// TestGet_FallsBackToPayloadsWithCorrectResourceVersion is Part 2's positive
// case: all four conditions hold, so GET succeeds from the payloads body,
// stamped with the metadata row's rv and uid, and with no side effects --
// neither row is deleted.
func TestGet_FallsBackToPayloadsWithCorrectResourceVersion(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/fallback-ok"
	obj := fallbackTestObject("fallback-ok")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)
	// No .g file on disk: this is the missing-file branch.

	out := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, out))

	assert.Equal(t, "4242", out.ResourceVersion, "ResourceVersion must be stamped from the metadata row's rv column")
	assert.Equal(t, testFallbackUID, string(out.UID), "UID must be stamped from the metadata row's uid column")
	assert.Equal(t, "fallback-ok", out.Name)
	assert.Equal(t, "payloads-body", out.Spec.Metadata.Tool.Name, "the served content must be the payloads body")

	// No side effects: no repairDelete, so both rows survive.
	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.True(t, metadataRowExistsForTest(t, conn, key), "the metadata row must not be deleted by a successful fallback read")
	assert.True(t, payloadsRowExistsForTest(t, conn, key), "the payloads row must not be deleted by a successful fallback read")
	pool.Put(conn)

	// Repeatable: a second GET serves the same object, still not NotFound.
	out2 := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, out2))
	assert.Equal(t, "4242", out2.ResourceVersion)
}

// TestGet_DeletedKeyStaysNotFoundAfterDelete is the C1 regression: a key
// deleted through the real delete path (which removes the payloads row too,
// US-001) must stay NotFound. The fallback must never resurrect it.
func TestGet_DeletedKeyStaysNotFoundAfterDelete(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/deleted"
	obj := fallbackTestObject("deleted")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)

	// Before the delete the key IS fallback-eligible -- otherwise this test
	// would pass vacuously.
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{}))

	require.NoError(t, s.Delete(ctx, key, &v1beta1.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{}))

	getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	assert.True(t, storage.IsNotFound(getErr), "a deleted key must stay NotFound, not be resurrected from payloads; got %v", getErr)

	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, metadataRowExistsForTest(t, conn, key))
	assert.False(t, payloadsRowExistsForTest(t, conn, key))
	pool.Put(conn)
}

// TestGet_PreExistingOrphanPayloadsRowNeverServed is the specific C1 gap the
// metadata-row condition closes: a payloads row with NO metadata row (a
// pre-existing orphan that predates this fix) must never be served.
func TestGet_PreExistingOrphanPayloadsRowNeverServed(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/orphan"
	obj := fallbackTestObject("orphan")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)
	// Deliberately NO metadata row: condition 1 fails.

	out := &v1beta1.SBOMSyft{}
	getErr := s.Get(ctx, key, storage.GetOptions{}, out)
	assert.True(t, storage.IsNotFound(getErr), "an orphan payloads row must never be served; got %v", getErr)
	assert.Empty(t, out.Spec.Metadata.Tool.Name, "nothing must have been decoded into the output object")

	// The absent-key read must also not have issued a repair write: the row
	// is inert, not reclaimed (the plan's named residual).
	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.True(t, payloadsRowExistsForTest(t, conn, key), "the orphan payloads row is left inert, untouched")
	pool.Put(conn)
}

// TestGet_LegacyTouchedRowWithLostRenameNeverServesStalePayload is the C2
// regression, and the reason condition 2 exists: a legacy write's row commit
// survived a crash that lost its .g file rename. The row has been legally
// touched by a legacy write (which nulls rv), so the payloads body is STALE
// ObjectStore-era content. Serving it would be a silent content rollback that
// a downstream consolidation pass would then re-persist as authoritative.
func TestGet_LegacyTouchedRowWithLostRenameNeverServesStalePayload(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/lost-rename"

	// The row the legacy write committed: Completed/Full, rv NULL.
	rowObj := fallbackTestObject("lost-rename")
	// The stale ObjectStore-era body that survived in payloads.
	staleObj := fallbackTestObject("lost-rename")
	staleObj.Spec.Metadata.Tool.Name = "stale-objectstore-era-body"

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, rowObj, 0, true /* rv IS NULL */, testFallbackUID, false)
	seedDecodablePayloadsRow(t, conn, s, key, staleObj)
	pool.Put(conn)
	// No .g file: the rename was lost by the crash.

	out := &v1beta1.SBOMSyft{}
	getErr := s.Get(ctx, key, storage.GetOptions{}, out)
	assert.True(t, storage.IsNotFound(getErr), "an rv-NULL row must fall through to self-repair, not serve the stale payload; got %v", getErr)
	assert.NotEqual(t, "stale-objectstore-era-body", out.Spec.Metadata.Tool.Name, "the stale payloads body must never be served")

	// Today's existing self-repair ran, exactly as before this change.
	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have removed the metadata row")
	assert.False(t, payloadsRowExistsForTest(t, conn, key), "self-repair must have removed the payloads row (US-001)")
	pool.Put(conn)
}

// TestGet_TimeSeriesRowNeverFallsBack is the condition-3 regression: a
// time-series row satisfies every OTHER condition (rv non-NULL, a decodable
// payloads row, no .g file), and must still take today's existing self-repair
// path. Time-series rows are explicitly deferred out of the fallback.
func TestGet_TimeSeriesRowNeverFallsBack(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/timeseries"
	obj := fallbackTestObject("timeseries")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, true /* is_time_series = 1 */)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)

	out := &v1beta1.SBOMSyft{}
	getErr := s.Get(ctx, key, storage.GetOptions{}, out)
	assert.True(t, storage.IsNotFound(getErr), "a time-series row must not reach the fallback; got %v", getErr)
	assert.Empty(t, out.Spec.Metadata.Tool.Name, "nothing must have been decoded into the output object")

	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run, as it does today")
	pool.Put(conn)
}

// TestGet_UndecodablePayloadsFallsThroughToExistingSelfRepair: all four
// conditions hold, but the body does not decode. That is a fall-through to
// today's self-repair, never a silent wrong-data success.
func TestGet_UndecodablePayloadsFallsThroughToExistingSelfRepair(t *testing.T) {
	t.Run("body is not valid JSON", func(t *testing.T) {
		s, pool, _ := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/undecodable-body"
		obj := fallbackTestObject("undecodable-body")

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
		seedRawPayloadsRow(t, conn, key, PayloadEncodingJSONV1Beta1, []byte("this is not json"))
		pool.Put(conn)

		getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

		conn, err = pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run")
		assert.False(t, payloadsRowExistsForTest(t, conn, key))
		pool.Put(conn)
	})

	t.Run("unsupported encoding", func(t *testing.T) {
		s, pool, _ := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/bad-encoding"
		obj := fallbackTestObject("bad-encoding")

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
		body, err := encodePayloadBody(s.scheme, obj)
		require.NoError(t, err)
		seedRawPayloadsRow(t, conn, key, "gob/v0", body)
		pool.Put(conn)

		getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

		conn, err = pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run")
		pool.Put(conn)
	})
}

// TestGet_LegacyOnlyRowStillSelfRepairs is the pure regression guard: a
// genuine legacy-only key (a metadata row, no payloads row, no .g file)
// behaves exactly as it did before this change -- condition 4 fails.
func TestGet_LegacyOnlyRowStillSelfRepairs(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/legacy-only"
	obj := fallbackTestObject("legacy-only")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	require.NoError(t, writeMetadata(conn, key, obj))
	pool.Put(conn)

	getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have removed the orphaned metadata row, as today")
	pool.Put(conn)

	// IgnoreNotFound keeps its existing contract too.
	out := &v1beta1.SBOMSyft{}
	assert.NoError(t, s.Get(ctx, key, storage.GetOptions{IgnoreNotFound: true}, out))
	assert.Empty(t, out.Name)
}

// TestGet_RowWithRVButNoPayloadsRowStillSelfRepairs covers condition 4 on its
// own: conditions 1-3 all hold (a metadata row, rv non-NULL, not a
// time-series row) and only the payloads row is missing. Without this case,
// condition 4's false branch is never reached -- the legacy-only guard above
// short-circuits at condition 2 first.
func TestGet_RowWithRVButNoPayloadsRowStillSelfRepairs(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/rv-no-payloads"
	obj := fallbackTestObject("rv-no-payloads")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
	pool.Put(conn)
	// Deliberately NO payloads row: condition 4 fails.

	getErr := s.Get(ctx, key, storage.GetOptions{}, &v1beta1.SBOMSyft{})
	assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)

	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run, as it does today")
	pool.Put(conn)
}

// TestGet_UndecodableLegacyFileSitesStillDoNotServeFallback pins the scope of
// Part 2: the fallback lives at the missing-file branch ONLY. The three
// undecodable-.g-file self-repair sites (gob EOF, and the migration-tool
// failure for both the noLock and hasWriteLock caller states) behave exactly
// as they did before this change -- self-repair, never serve-from-payloads --
// even with all four fallback conditions satisfied for the key.
func TestGet_UndecodableLegacyFileSitesStillDoNotServeFallback(t *testing.T) {
	// seedFallbackEligible seeds a key that WOULD satisfy all four
	// conditions, so only the presence of the undecodable file separates
	// these sites from the fallback.
	seedFallbackEligible := func(t *testing.T, s *StorageImpl, pool *sqlitemigration.Pool, key, name string) {
		t.Helper()
		obj := fallbackTestObject(name)
		obj.Spec.Metadata.Tool.Name = "payloads-body"
		conn, err := pool.Take(context.Background())
		require.NoError(t, err)
		seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
		seedDecodablePayloadsRow(t, conn, s, key, obj)
		pool.Put(conn)
	}

	t.Run("gob EOF on decode", func(t *testing.T) {
		s, pool, fs := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/badfile1"
		seedFallbackEligible(t, s, pool, key, "badfile1")
		// An empty payload file: gob.Decode returns io.EOF immediately.
		require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), []byte{}, 0644))

		out := &v1beta1.SBOMSyft{}
		getErr := s.Get(ctx, key, storage.GetOptions{}, out)
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)
		assert.NotEqual(t, "payloads-body", out.Spec.Metadata.Tool.Name, "the payloads body must not be served at this site")

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run, exactly as before this change")
		pool.Put(conn)
	})

	t.Run("migration tool failure, noLock caller", func(t *testing.T) {
		installFakeMigrationTool(t)
		t.Setenv("MIGRATION_FAKE_FAIL", "1")

		s, pool, fs := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/badfile2"
		seedFallbackEligible(t, s, pool, key, "badfile2")
		require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), gobPayloadNeedingMigration(t), 0644))

		out := &v1beta1.SBOMSyft{}
		getErr := s.Get(ctx, key, storage.GetOptions{}, out)
		assert.True(t, storage.IsNotFound(getErr), "got %v", getErr)
		assert.NotEqual(t, "payloads-body", out.Spec.Metadata.Tool.Name, "the payloads body must not be served at this site")

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run, exactly as before this change")
		pool.Put(conn)
	})

	t.Run("migration tool failure, hasWriteLock caller", func(t *testing.T) {
		installFakeMigrationTool(t)
		t.Setenv("MIGRATION_FAKE_FAIL", "1")

		// The hasWriteLock get() call site is only reachable with the single
		// writer off (see the US-001 test's equivalent case).
		old := singleWriterEnabled
		singleWriterEnabled = false
		t.Cleanup(func() { singleWriterEnabled = old })

		s, pool, fs := newRollbackSafetyTestStorage(t)
		ctx := context.Background()
		key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/badfile3"
		seedFallbackEligible(t, s, pool, key, "badfile3")
		require.NoError(t, afero.WriteFile(fs, getStoredPayloadFilepath(DefaultStorageRoot, key), gobPayloadNeedingMigration(t), 0644))

		tryUpdate := func(_ runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			t.Fatal("tryUpdate must not run: getCurrentState must fail with NotFound before reaching it")
			return nil, nil, nil
		}
		updErr := s.GuaranteedUpdate(ctx, key, &v1beta1.SBOMSyft{}, false, nil, tryUpdate, nil)
		assert.True(t, storage.IsNotFound(updErr), "got %v", updErr)

		conn, err := pool.Take(ctx)
		require.NoError(t, err)
		assert.False(t, metadataRowExistsForTest(t, conn, key), "self-repair must have run, exactly as before this change")
		pool.Put(conn)
	})
}
