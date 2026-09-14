package file

// Tests for the fifth review round on .omc/plans/rollback-safety-guard.md:
//
//   - Part 3 (previously deferred on the incorrect assumption that flag-off +
//     singleWriterEnabled=false is unreachable -- main.go only Fatals on
//     flag-ON + singleWriterEnabled=false, so it is a supported
//     configuration): Create's existence check must also see an object that
//     is visible only through the rollback read fallback, not just the legacy
//     .g file on disk.
//   - Part 1's error handling: a payloads-row delete failure inside the real
//     StorageImpl.Delete must reach the caller instead of being logged and
//     swallowed; the remaining object must survive for a retry.

import (
	"context"
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// denyPayloadsDeleteAuthorizer fails exactly one statement shape -- a DELETE
// against the payloads table -- and authorizes everything else, so a test can
// drive the real Delete path with DeletePayloads (and only DeletePayloads)
// failing.
type denyPayloadsDeleteAuthorizer struct{}

func (denyPayloadsDeleteAuthorizer) Authorize(action sqlite.Action) sqlite.AuthResult {
	if action.Type() == sqlite.OpDelete && action.Table() == "payloads" {
		return sqlite.AuthResultDeny
	}
	return sqlite.AuthResultOK
}

// newRollbackSafetyTestStorageWithPool is newRollbackSafetyTestStorage with a
// caller-supplied pool, for the fault-injection test below.
func newRollbackSafetyTestStorageWithPool(t *testing.T, opts PoolOptions) (*StorageImpl, *sqlitemigration.Pool) {
	t.Helper()
	fs := afero.NewMemMapFs()
	pool := NewPoolWithOptions(t.TempDir()+"/test.sq3", opts)
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, sch).(*StorageImpl)
	return s, pool
}

// TestDelete_PayloadsDeleteFailureSurfacesToCaller drives the REAL
// StorageImpl.Delete (not DeletePayloads in isolation) with the payloads
// DELETE rejected at the SQLite authorizer. The error must reach the caller
// and leave the object available for a successful retry.
func TestDelete_PayloadsDeleteFailureSurfacesToCaller(t *testing.T) {
	s, pool := newRollbackSafetyTestStorageWithPool(t, PoolOptions{
		Size:       1,
		Authorizer: func(*sqlite.Conn) sqlite.Authorizer { return denyPayloadsDeleteAuthorizer{} },
	})
	ctx := context.Background()
	key := cpTestKey("delete-payloads-fails")
	obj := cpTestObject("delete-payloads-fails")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)

	delErr := s.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
	require.Error(t, delErr, "a Delete that could not remove the payloads row must not report success")
	assert.Contains(t, delErr.Error(), "delete payloads")

	// Failed cleanup preserves the object; removing the injected failure
	// allows the same Delete to succeed on retry.
	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.True(t, payloadsRowExistsForTest(t, conn, key))
	assert.True(t, metadataRowExistsForTest(t, conn, key))
	require.NoError(t, conn.SetAuthorizer(nil))
	pool.Put(conn)
	require.NoError(t, s.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))
	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, payloadsRowExistsForTest(t, conn, key))
	assert.False(t, metadataRowExistsForTest(t, conn, key))
	pool.Put(conn)
}

// TestDelete_SucceedsWhenPayloadsDeleteWorks is the control for the test
// above: with no fault injected, the same Delete reports success and removes
// both rows. Without it, the assertion above could pass for the wrong reason.
func TestDelete_SucceedsWhenPayloadsDeleteWorks(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := cpTestKey("delete-payloads-ok")
	obj := cpTestObject("delete-payloads-ok")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)

	require.NoError(t, s.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))

	conn, err = pool.Take(ctx)
	require.NoError(t, err)
	assert.False(t, payloadsRowExistsForTest(t, conn, key))
	assert.False(t, metadataRowExistsForTest(t, conn, key))
	pool.Put(conn)
}

// TestCreate_RefusesAnObjectVisibleOnlyThroughTheFallback is Part 3. A key
// with a metadata row (rv non-NULL, is_time_series = 0) and a payloads row
// but NO legacy .g file is a live object: a GET serves it from the payloads
// body. Create must therefore report AlreadyExists for it, in BOTH supported
// configurations -- and must not have replaced the row.
func TestCreate_RefusesAnObjectVisibleOnlyThroughTheFallback(t *testing.T) {
	for _, tc := range []struct {
		name         string
		singleWriter bool
	}{
		{"singleWriterEnabled=false (CreateWithConn's own existence check)", false},
		{"singleWriterEnabled=true (the commit-time recheck)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = tc.singleWriter
			t.Cleanup(func() { singleWriterEnabled = old })

			s, pool, fs := newRollbackSafetyTestStorage(t)
			ctx := context.Background()
			key := cpTestKey("create-vs-fallback")
			existing := cpTestObject("create-vs-fallback")
			existing.Spec.Execs = []softwarecomposition.ExecCalls{{Path: "/bin/original"}}

			conn, err := pool.Take(ctx)
			require.NoError(t, err)
			seedMetadataRow(t, conn, key, existing, testFallbackRV, false, testFallbackUID, false)
			seedDecodablePayloadsRow(t, conn, s, key, existing)
			pool.Put(conn)
			// Deliberately no .g file: the only thing CreateWithConn's Stat
			// check can see is absent, which is the whole point.

			// Not vacuous: the key really is served by the fallback today.
			before := &softwarecomposition.ContainerProfile{}
			require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, before))
			require.Equal(t, "/bin/original", before.Spec.Execs[0].Path)

			incoming := cpTestObject("create-vs-fallback")
			incoming.Spec.Execs = []softwarecomposition.ExecCalls{{Path: "/bin/overwriter"}}
			createErr := s.Create(ctx, key, incoming, &softwarecomposition.ContainerProfile{}, 0)
			require.Error(t, createErr, "Create must not silently replace an object the read fallback is serving")
			assert.True(t, storage.IsExist(createErr), "expected AlreadyExists, got %v", createErr)

			// And the refusal was real: the stored object is untouched, and
			// no .g file was left behind by a half-done create.
			after := &softwarecomposition.ContainerProfile{}
			require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, after))
			assert.Equal(t, "/bin/original", after.Spec.Execs[0].Path, "the fallback-visible object must be unchanged")
			_, statErr := fs.Stat(getStoredPayloadFilepath(DefaultStorageRoot, key))
			assert.Error(t, statErr, "the refused Create must not have written a .g file")
		})
	}
}

// TestCreate_StillSucceedsForKeysTheFallbackDoesNotServe is the
// over-refusal control for Part 3: the new existence check must refuse
// exactly what the read fallback serves and nothing more. Each shape below
// fails one of the predicate's conditions, so a GET self-repairs it rather
// than serving it -- and Create must go through.
func TestCreate_StillSucceedsForKeysTheFallbackDoesNotServe(t *testing.T) {
	seed := func(t *testing.T, s *StorageImpl, pool *sqlitemigration.Pool, key string, rvNull, isTimeSeries, withPayloads bool) {
		t.Helper()
		obj := cpTestObject("noop")
		conn, err := pool.Take(context.Background())
		require.NoError(t, err)
		seedMetadataRow(t, conn, key, obj, testFallbackRV, rvNull, testFallbackUID, isTimeSeries)
		if withPayloads {
			seedDecodablePayloadsRow(t, conn, s, key, obj)
		}
		pool.Put(conn)
	}

	for _, tc := range []struct {
		name                             string
		rvNull, isTimeSeries, hasPayload bool
	}{
		{"rv IS NULL (a legacy-owned row)", true, false, true},
		{"a time-series row", false, true, true},
		{"no payloads row", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = false
			t.Cleanup(func() { singleWriterEnabled = old })

			s, pool, _ := newRollbackSafetyTestStorage(t)
			ctx := context.Background()
			key := cpTestKey("create-allowed")
			seed(t, s, pool, key, tc.rvNull, tc.isTimeSeries, tc.hasPayload)

			incoming := cpTestObject("create-allowed")
			incoming.Spec.Execs = []softwarecomposition.ExecCalls{{Path: "/bin/new"}}
			require.NoError(t, s.Create(ctx, key, incoming, &softwarecomposition.ContainerProfile{}, 0),
				"the new existence check must not refuse a key the fallback does not serve")

			out := &softwarecomposition.ContainerProfile{}
			require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, out))
			assert.Equal(t, "/bin/new", out.Spec.Execs[0].Path)
		})
	}
}

// Query failures must preserve live rows and must never look like absence,
// including when Get is called with IgnoreNotFound by an update path.
func TestRollbackSafety_InspectionFailuresPreserveLiveObject(t *testing.T) {
	for _, table := range []string{"payloads", "metadata"} {
		for _, operation := range []string{"get", "get-ignore-not-found", "create"} {
			t.Run(table+"/"+operation, func(t *testing.T) {
				old := singleWriterEnabled
				singleWriterEnabled = false
				t.Cleanup(func() { singleWriterEnabled = old })
				s, pool := newRollbackSafetyTestStorageWithPool(t, PoolOptions{Size: 1})
				ctx := context.Background()
				key := cpTestKey("inspection-failure")
				obj := cpTestObject("inspection-failure")
				obj.Spec.Execs = []softwarecomposition.ExecCalls{{Path: "/bin/original"}}
				conn, err := pool.Take(ctx)
				require.NoError(t, err)
				seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
				seedDecodablePayloadsRow(t, conn, s, key, obj)
				beforeMetadata, err := ReadMetadata(conn, key)
				require.NoError(t, err)
				before, err := readFallbackCandidate(conn, key)
				require.NoError(t, err)
				// Installing an authorizer expires cached statements. Reject reads
				// without dropping tables or destroying the evidence of preservation.
				require.NoError(t, conn.SetAuthorizer(sqlite.AuthorizeFunc(func(a sqlite.Action) sqlite.AuthResult {
					if a.Type() == sqlite.OpRead && a.Table() == table {
						return sqlite.AuthResultDeny
					}
					return sqlite.AuthResultOK
				})))
				pool.Put(conn)

				var operationErr error
				if operation == "create" {
					operationErr = s.Create(ctx, key, cpTestObject("inspection-failure"), &softwarecomposition.ContainerProfile{}, 0)
				} else {
					operationErr = s.Get(ctx, key, storage.GetOptions{IgnoreNotFound: operation == "get-ignore-not-found"}, &softwarecomposition.ContainerProfile{})
				}
				require.Error(t, operationErr)
				assert.False(t, storage.IsNotFound(operationErr))
				assert.False(t, storage.IsExist(operationErr))

				conn, err = pool.Take(ctx)
				require.NoError(t, err)
				require.NoError(t, conn.SetAuthorizer(nil))
				afterMetadata, err := ReadMetadata(conn, key)
				require.NoError(t, err)
				after, err := readFallbackCandidate(conn, key)
				require.NoError(t, err)
				assert.Equal(t, beforeMetadata, afterMetadata)
				assert.Equal(t, before, after, "both rows, including payload bytes, must survive")
				pool.Put(conn)
				exists, err := afero.Exists(s.appFs, getStoredPayloadFilepath(s.root, key))
				require.NoError(t, err)
				assert.False(t, exists)
				out := &softwarecomposition.ContainerProfile{}
				require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, out))
				assert.Equal(t, "/bin/original", out.Spec.Execs[0].Path, "read recovers after the transient failure")
			})
		}
	}
}

func TestRollbackSafety_TimeSeriesPayloadSurvivesWithEitherRVState(t *testing.T) {
	for _, rvNull := range []bool{false, true} {
		t.Run(fmt.Sprintf("rvNull=%t", rvNull), func(t *testing.T) {
			s, pool, _ := newRollbackSafetyTestStorage(t)
			ctx := context.Background()
			key := cpTestKey("time-series")
			obj := cpTestObject("time-series")
			conn, err := pool.Take(ctx)
			require.NoError(t, err)
			seedMetadataRow(t, conn, key, obj, testFallbackRV, rvNull, testFallbackUID, true)
			seedDecodablePayloadsRow(t, conn, s, key, obj)
			before, err := readFallbackCandidate(conn, key)
			require.NoError(t, err)
			pool.Put(conn)
			require.True(t, storage.IsNotFound(s.Get(ctx, key, storage.GetOptions{}, &softwarecomposition.ContainerProfile{})))
			conn, err = pool.Take(ctx)
			require.NoError(t, err)
			assert.False(t, metadataRowExistsForTest(t, conn, key))
			var body []byte
			require.NoError(t, sqlitex.Execute(conn, "SELECT body FROM payloads", &sqlitex.ExecOptions{
				ResultFunc: func(stmt *sqlite.Stmt) error {
					body = make([]byte, stmt.ColumnLen(0))
					stmt.ColumnBytes(0, body)
					return nil
				},
			}))
			assert.Equal(t, before.body, body)
			pool.Put(conn)
		})
	}
}

// An unreadable SQL object must never become an empty update candidate.
func TestRollbackSafety_UpdateDoesNotReplaceUndecodablePayload(t *testing.T) {
	for _, singleWriter := range []bool{false, true} {
		t.Run(fmt.Sprintf("singleWriter=%t", singleWriter), func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = singleWriter
			t.Cleanup(func() { singleWriterEnabled = old })
			s, pool, _ := newRollbackSafetyTestStorage(t)
			ctx := context.Background()
			key := cpTestKey("undecodable-update")
			obj := cpTestObject("undecodable-update")
			conn, err := pool.Take(ctx)
			require.NoError(t, err)
			seedMetadataRow(t, conn, key, obj, testFallbackRV, false, testFallbackUID, false)
			seedRawPayloadsRow(t, conn, key, "future-encoding", []byte("recoverable bytes"))
			before, err := readFallbackCandidate(conn, key)
			require.NoError(t, err)
			pool.Put(conn)
			called := false
			err = s.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, true, nil,
				func(runtime.Object, storage.ResponseMeta) (runtime.Object, *uint64, error) {
					called = true
					return cpTestObject("replacement"), nil, nil
				}, nil)
			require.Error(t, err)
			assert.False(t, storage.IsNotFound(err))
			assert.False(t, called, "inspection failure must prevent the update callback")
			conn, err = pool.Take(ctx)
			require.NoError(t, err)
			after, err := readFallbackCandidate(conn, key)
			require.NoError(t, err)
			assert.Equal(t, before, after)
			pool.Put(conn)
		})
	}
}
