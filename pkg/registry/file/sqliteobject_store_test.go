package file

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func identityTryUpdate(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
	return input, nil, nil
}

func setLabel(k, v string) storage.UpdateFunc {
	return func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		cp := input.(*softwarecomposition.ContainerProfile)
		if cp.Labels == nil {
			cp.Labels = map[string]string{}
		}
		cp.Labels[k] = v
		return cp, nil, nil
	}
}

func TestObjectStore_CreateGetUpdateDeleteRoundTrip(t *testing.T) {
	e := newObjectStoreEnv(t)
	p := e.plain("rt")
	p.TypeMeta = metav1.TypeMeta{APIVersion: StorageV1Beta1ApiVersion, Kind: "ContainerProfile"}
	key := e.key("rt")

	created := e.create(p)
	assert.Equal(t, "1", created.ResourceVersion)
	assert.NotEmpty(t, created.Annotations[helpersv1.SyncChecksumMetadataKey])
	e.withConn(func(c *sqlite.Conn) { assertINV2(t, c, key) })

	got := e.mustGet(key)
	assert.Equal(t, created.ResourceVersion, got.ResourceVersion)
	assert.Equal(t, created.UID, got.UID)
	assert.Equal(t, p.TypeMeta, got.TypeMeta, "TypeMeta must survive the JSON body")
	assert.Equal(t, canonicalCP(created).Spec, canonicalCP(got).Spec)
	assert.Equal(t, created.Annotations, got.Annotations)

	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.GuaranteedUpdate(e.ctx, key, out, false, nil, setLabel("a", "b"), nil))
	assert.Equal(t, "2", out.ResourceVersion)
	assert.Equal(t, "b", e.mustGet(key).Labels["a"])
	e.withConn(func(c *sqlite.Conn) { assertINV2(t, c, key) })

	// no-op update: no write, no RV bump (#315)
	require.NoError(t, e.store.GuaranteedUpdate(e.ctx, key, out, false, nil, identityTryUpdate, nil))
	assert.Equal(t, "2", out.ResourceVersion)
	assert.Equal(t, "2", e.mustGet(key).ResourceVersion)

	deleted := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.Delete(e.ctx, key, deleted, nil, nil, nil, storage.DeleteOptions{}))
	assert.Equal(t, "2", deleted.ResourceVersion)
	_, err := e.get(key)
	assert.True(t, storage.IsNotFound(err), "%v", err)
	row := e.inspect(key)
	assert.False(t, row.metaExists)
	assert.False(t, row.payloadExists)

	err = e.store.Delete(e.ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
	assert.True(t, storage.IsNotFound(err), "delete of an absent key: %v", err)
}

// TestObjectStore_CreateStates covers Create over (absent, present,
// present-being-recreated): the second create of a key is KeyExists; a
// delete+create restarts the key at rv=1 with a fresh UID.
func TestObjectStore_CreateStates(t *testing.T) {
	e := newObjectStoreEnv(t)
	key := e.key("cs")
	first := e.create(e.plain("cs"))
	assert.Equal(t, "1", first.ResourceVersion)

	err := e.store.Create(e.ctx, key, e.plain("cs"), nil, 0)
	assert.True(t, storage.IsExist(err), "%v", err)
	assert.Equal(t, first.UID, e.mustGet(key).UID, "a refused create must not touch the row")

	require.NoError(t, e.store.Delete(e.ctx, key, nil, nil, nil, nil, storage.DeleteOptions{}))
	second := e.create(e.plain("cs"))
	assert.Equal(t, "1", second.ResourceVersion)
	assert.NotEqual(t, first.UID, second.UID)

	err = e.store.Create(e.ctx, key, &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "cs", ResourceVersion: "7"}}, nil, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resourceVersion should not be set")
}

// TestObjectStore_CASMatrix drives the compare-and-swap statement directly over
// (row state) × (expectation): the row is absent, present at (rv 1, uid A), or
// recreated at (rv 1, uid B) after a delete; the expectation matches, is
// stale, or names a different uid. Exactly the matching cases commit.
func TestObjectStore_CASMatrix(t *testing.T) {
	e := newObjectStoreEnv(t)
	uidA, uidB := "uid-a", "uid-b"
	body, err := e.store.encodeBody(e.plain("cas"))
	require.NoError(t, err)

	type expectation struct {
		name      string
		insert    bool
		expectRV  int64
		expectUID string
	}
	type rowState struct {
		name string
		seed func(conn *sqlite.Conn)
	}
	seedRow := func(uid string) func(conn *sqlite.Conn) {
		return func(conn *sqlite.Conn) {
			meta := fmt.Sprintf(`{"name":"cas","namespace":%q,"uid":%q,"resourceVersion":"1"}`, e.ns, uid)
			require.NoError(t, sqlitex.Execute(conn, `INSERT INTO metadata (kind,namespace,name,metadata,rv,uid) VALUES ('containerprofile',?, 'cas', ?, 1, ?)`,
				&sqlitex.ExecOptions{Args: []any{e.ns, meta, uid}}))
			require.NoError(t, sqlitex.Execute(conn, `INSERT INTO payloads (kind,namespace,name,encoding,body) VALUES ('containerprofile',?, 'cas', ?, ?)`,
				&sqlitex.ExecOptions{Args: []any{e.ns, PayloadEncodingJSONV1Beta1, body}}))
		}
	}
	states := []rowState{
		{"absent", func(*sqlite.Conn) {}},
		{"present-rv1-uidA", seedRow(uidA)},
		{"recreated-rv1-uidB", func(conn *sqlite.Conn) { seedRow(uidA)(conn); wipe(t, conn, e.ns, "cas"); seedRow(uidB)(conn) }},
	}
	expectations := []expectation{
		{"insert-if-absent", true, 0, ""},
		{"rv1-uidA", false, 1, uidA},
		{"rv0-uidA (stale)", false, 0, uidA},
		{"rv1-uidB", false, 1, uidB},
	}
	// which (state, expectation) pairs commit
	commits := map[string]bool{
		"absent/insert-if-absent":      true,
		"present-rv1-uidA/rv1-uidA":    true,
		"recreated-rv1-uidB/rv1-uidB":  true,
	}
	for _, st := range states {
		for _, ex := range expectations {
			name := st.name + "/" + ex.name
			t.Run(name, func(t *testing.T) {
				e.withFixture(func(conn *sqlite.Conn) {
					wipe(t, conn, e.ns, "cas")
					st.seed(conn)
					pw := &preparedWrite{key: e.key("cas"), kind: "containerprofile", namespace: e.ns, name: "cas",
						metadataJSON: []byte(fmt.Sprintf(`{"name":"cas","namespace":%q,"uid":%q,"resourceVersion":"%d"}`, e.ns, "uid-new", ex.expectRV+1)),
						body: body, rv: ex.expectRV + 1, uid: "uid-new", insert: ex.insert, expectRV: ex.expectRV, expectUID: ex.expectUID}
					if ex.insert {
						pw.rv, pw.uid = 1, "uid-new"
						pw.metadataJSON = []byte(fmt.Sprintf(`{"name":"cas","namespace":%q,"uid":"uid-new","resourceVersion":"1"}`, e.ns))
					}
					endFn, err := sqlitex.ImmediateTransaction(conn)
					require.NoError(t, err)
					err = e.store.execUpdate(conn, pw, noHook)
					endFn(&err)
					if commits[name] {
						require.NoError(t, err)
						row := inspectRow(t, conn, e.key("cas"))
						require.NotNil(t, row.rv)
						assert.Equal(t, pw.rv, *row.rv)
						assert.Equal(t, "uid-new", *row.uid)
					} else {
						assert.True(t, errors.Is(err, errWriteConflict), "expected conflict, got %v", err)
					}
					assertINV2(t, conn, e.key("cas"))
				})
			})
		}
	}
}

func wipe(t *testing.T, conn *sqlite.Conn, ns, name string) {
	t.Helper()
	require.NoError(t, sqlitex.Execute(conn, `DELETE FROM metadata WHERE kind='containerprofile' AND namespace=? AND name=?`, &sqlitex.ExecOptions{Args: []any{ns, name}}))
	require.NoError(t, sqlitex.Execute(conn, `DELETE FROM payloads WHERE kind='containerprofile' AND namespace=? AND name=?`, &sqlitex.ExecOptions{Args: []any{ns, name}}))
}

// TestObjectStore_TSAdmissionInsideTransaction: the base's four states. The
// authoritative check is the in-transaction SELECT, so the base is flipped
// AFTER the prepare phase's PreSave passed (design "gap 1") and the Create
// must still fail — with no TS object and no time_series row left behind.
func TestObjectStore_TSAdmissionInsideTransaction(t *testing.T) {
	cases := []struct {
		name          string
		baseStatus    string
		baseComplete  string
		incoming      string
		wantErr       error
		wantAdmitted  bool
	}{
		{"absent", "", "", helpersv1.Partial, nil, true},
		{"learning", helpersv1.Learning, helpersv1.Partial, helpersv1.Partial, nil, true},
		{"completed-partial+full-incoming", helpersv1.Completed, helpersv1.Partial, helpersv1.Full, nil, true},
		{"completed-partial+partial-incoming", helpersv1.Completed, helpersv1.Partial, helpersv1.Partial, ObjectCompletedError, false},
		{"completed-full", helpersv1.Completed, helpersv1.Full, helpersv1.Full, ObjectCompletedError, false},
		{"too-large", helpersv1.TooLarge, helpersv1.Partial, helpersv1.Partial, ObjectTooLargeError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newObjectStoreEnv(t)
			if tc.baseStatus != "" {
				// Flip the base between prepare and commit: PreSave sees no base
				// (or a Learning one) and admits; the transaction must not.
				e.store.hooks.afterPrepare = func(path, key string) {
					if path != holdPathCreate {
						return
					}
					e.store.hooks.afterPrepare = nil
					base := e.plain(e.baseNm)
					base.Annotations[helpersv1.StatusMetadataKey] = tc.baseStatus
					base.Annotations[helpersv1.CompletionMetadataKey] = tc.baseComplete
					e.create(base)
				}
			}
			ts := e.ts("r1", 1, helpersv1.Learning, tc.incoming)
			err := e.store.Create(e.ctx, e.tsKey("r1"), ts, nil, 0)
			row := e.inspect(e.tsKey("r1"))
			base := e.inspect(e.baseKey)
			if tc.wantAdmitted {
				require.NoError(t, err)
				assert.True(t, row.metaExists && row.payloadExists)
				assert.Equal(t, 1, base.tsRows, "the time_series row joins the create")
			} else {
				require.ErrorIs(t, err, tc.wantErr)
				assert.False(t, row.metaExists, "refused create must leave no metadata row")
				assert.False(t, row.payloadExists, "refused create must leave no payload")
				assert.Equal(t, 0, base.tsRows, "refused create must leave no time_series row")
			}
		})
	}
}

// TestObjectStore_K6_PayloadRowMissingFailsLoudly: a metadata row without its
// payloads row (INV-2 already violated by something else) makes the payloads
// UPDATE change 0 rows; the transaction rolls back and the caller gets an
// InternalError, instead of a metadata row silently advancing alone.
func TestObjectStore_K6_PayloadRowMissingFailsLoudly(t *testing.T) {
	e := newObjectStoreEnv(t)
	key := e.key("k6")
	e.create(e.plain("k6"))
	e.withFixture(func(conn *sqlite.Conn) {
		require.NoError(t, sqlitex.Execute(conn, `DELETE FROM payloads WHERE name='k6'`, nil))
	})
	// The read needs a payload; feed the update the cached object so the
	// prepare phase does not fail first.
	cached := e.plain("k6")
	cached.ResourceVersion = "1"
	cached.UID = types.UID(e.inspect(key).uidOrEmpty())
	err := e.store.GuaranteedUpdate(e.ctx, key, &softwarecomposition.ContainerProfile{}, false, nil, setLabel("x", "y"), cached)
	require.Error(t, err)
	assert.True(t, apierrors.IsInternalError(err), "%v", err)
	assert.Contains(t, err.Error(), "INV-2")
	row := e.inspect(key)
	require.NotNil(t, row.rv)
	assert.Equal(t, int64(1), *row.rv, "metadata UPDATE must have been rolled back")
	assert.True(t, e.store.gate.conn.AutocommitEnabled(), "gate connection left in a transaction")
}

func (r dbRow) uidOrEmpty() string {
	if r.uid == nil {
		return ""
	}
	return *r.uid
}

// TestObjectStore_K3_AutocheckpointOffOnEveryConnection: wal_autocheckpoint is
// a per-connection setting; PoolOptions.DisableAutoCheckpoint must reach every
// connection of the pool, not only the gate's.
func TestObjectStore_K3_AutocheckpointOffOnEveryConnection(t *testing.T) {
	check := func(t *testing.T, disable bool, want int64) {
		pool := NewPoolWithOptions(t.TempDir()+"/k3.sq3", PoolOptions{Size: 4, DisableAutoCheckpoint: disable})
		var conns []*sqlite.Conn
		defer func() {
			for _, c := range conns {
				pool.Put(c)
			}
			_ = pool.Close()
		}()
		for i := 0; i < 4; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			c, err := pool.Take(ctx)
			require.NoError(t, err)
			c.SetInterrupt(nil)
			cancel()
			conns = append(conns, c)
			var v int64 = -1
			require.NoError(t, sqlitex.ExecuteTransient(c, `PRAGMA wal_autocheckpoint`, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error { v = stmt.ColumnInt64(0); return nil }}))
			assert.Equal(t, want, v, "connection %d", i)
		}
	}
	t.Run("disabled", func(t *testing.T) { check(t, true, 0) })
	t.Run("default", func(t *testing.T) { check(t, false, 1000) })
}

// TestObjectStore_K5_CloseReturnsGateConnection: the gate's connection is back
// in the pool after Close, so every one of the pool's connections can be taken
// and Pool.Close does not block. (The env's cleanup asserts Close returns.)
func TestObjectStore_K5_CloseReturnsGateConnection(t *testing.T) {
	e := newObjectStoreEnv(t, withPoolSize(3))
	countTakeable := func() int {
		var conns []*sqlite.Conn
		defer func() {
			for _, c := range conns {
				e.pool.Put(c)
			}
		}()
		for len(conns) < 10 {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			c, err := e.pool.Take(ctx)
			cancel()
			if err != nil {
				break
			}
			conns = append(conns, c)
		}
		return len(conns)
	}
	assert.Equal(t, 2, countTakeable(), "one of three connections is the gate's while the gate is open")
	// The shared gate is closed by its owner after every store's Close (the
	// apiserver's pre-shutdown hook); the store's own Close returns nothing.
	require.NoError(t, e.store.Close())
	assert.Equal(t, 2, countTakeable(), "the store does not own the gate's connection")
	require.NoError(t, e.gate.Close())
	assert.Equal(t, 3, countTakeable(), "the gate's Close must return its connection")
	require.NoError(t, e.store.Close(), "Close is idempotent")
	require.NoError(t, e.gate.Close(), "Close is idempotent")
	err := e.store.Create(e.ctx, e.key("after-close"), e.plain("after-close"), nil, 0)
	assert.ErrorIs(t, err, errGateClosed)
}

// TestObjectStore_CodecFidelity round-trips a user-authored multi-container
// profile (the subtype-groups regression) and pins the ONE fidelity delta the
// design lists: creationTimestamp is truncated to whole seconds.
func TestObjectStore_CodecFidelity(t *testing.T) {
	e := newObjectStoreEnv(t)
	p := groupedUserCP(e.ns, "grouped")
	p.UID = uuid.NewUUID()
	stamp := metav1.NewTime(time.Date(2026, 9, 8, 12, 0, 0, 123456789, time.UTC))
	p.CreationTimestamp = stamp
	key := e.key("grouped")
	created := e.create(p)
	got := e.mustGet(key)

	assert.Equal(t, created.Spec.Containers, got.Spec.Containers)
	assert.Equal(t, created.Spec.InitContainers, got.Spec.InitContainers)
	assert.Equal(t, created.Spec.EphemeralContainers, got.Spec.EphemeralContainers)
	assert.True(t, got.CreationTimestamp.Time.Equal(stamp.Time.Truncate(time.Second)),
		"creationTimestamp truncated to seconds (intended divergence): got %s", got.CreationTimestamp.Time)
	assert.False(t, got.CreationTimestamp.Time.Equal(stamp.Time), "the sub-second part is gone")
	// What the legacy store's GET returns is the gob round trip of the same
	// persisted object (empty collections come back nil on both codecs).
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(created))
	legacyView := &softwarecomposition.ContainerProfile{}
	require.NoError(t, gob.NewDecoder(&buf).Decode(legacyView))
	assert.True(t, legacyView.CreationTimestamp.Time.Equal(stamp.Time), "gob keeps the nanoseconds")
	// Second codec delta, NOT in the design's list (reported): v1beta1 spec
	// collections without omitempty come back as empty slices from JSON and
	// as nil from gob (REST renders them as [] vs null).
	assert.Nil(t, legacyView.Spec.Execs, "gob: empty Execs decodes to nil")
	assert.NotNil(t, got.Spec.Execs, "json: empty Execs decodes to an empty slice")
	assert.Empty(t, got.Spec.Execs)
	assert.Equal(t, canonicalCP(legacyView), canonicalCP(got), "no field beyond creationTimestamp precision and nil-vs-empty collections may differ from the legacy GET")

	// v1beta1 round trip of the body itself
	var v1 map[string]any
	e.withConn(func(conn *sqlite.Conn) {
		require.NoError(t, sqlitex.Execute(conn, `SELECT body, encoding FROM payloads WHERE name='grouped'`, &sqlitex.ExecOptions{ResultFunc: func(stmt *sqlite.Stmt) error {
			assert.Equal(t, PayloadEncodingJSONV1Beta1, stmt.ColumnText(1))
			return json.Unmarshal([]byte(stmt.ColumnText(0)), &v1)
		}}))
	})
	assert.NotNil(t, v1["spec"], "body is the v1beta1 wire form (lower-case json tags)")
}

// TestObjectStore_ListPagination: metadata LIST is the legacy statement; the
// fullSpec LIST is a paginated join; an UPDATE mid-list keeps the rowid so a
// paginating client sees each object exactly once.
func TestObjectStore_ListPagination(t *testing.T) {
	e := newObjectStoreEnv(t)
	for _, n := range []string{"a", "b", "c"} {
		e.create(e.plain("pg-" + n))
	}
	listKey := testCPPrefix + e.ns
	page := func(rv, cont string, limit int64) *softwarecomposition.ContainerProfileList {
		out := &softwarecomposition.ContainerProfileList{}
		opts := storage.ListOptions{ResourceVersion: rv, Predicate: storage.SelectionPredicate{Limit: limit, Continue: cont}, Recursive: true}
		require.NoError(t, e.store.GetList(e.ctx, listKey, opts, out))
		return out
	}
	names := func(l *softwarecomposition.ContainerProfileList) []string {
		var out []string
		for _, it := range l.Items {
			out = append(out, it.Name)
		}
		return out
	}
	for _, rv := range []string{softwarecomposition.ResourceVersionMetadata, softwarecomposition.ResourceVersionFullSpec} {
		p1 := page(rv, "", 2)
		assert.Equal(t, []string{"pg-a", "pg-b"}, names(p1), rv)
		require.NotEmpty(t, p1.Continue)
		require.NoError(t, e.store.GuaranteedUpdate(e.ctx, e.key("pg-a"), &softwarecomposition.ContainerProfile{}, false, nil, setLabel("k", rv), nil))
		p2 := page(rv, p1.Continue, 2)
		assert.Equal(t, []string{"pg-c"}, names(p2), "%s: updated object must not reappear (rowid stable)", rv)
		assert.Empty(t, p2.Continue)
	}
	full := page(softwarecomposition.ResourceVersionFullSpec, "", 0)
	require.Len(t, full.Items, 3)
	assert.NotEmpty(t, full.Items[0].Spec.Architectures, "fullSpec list carries the body")
	meta := page(softwarecomposition.ResourceVersionMetadata, "", 0)
	assert.Empty(t, meta.Items[0].Spec.Architectures, "metadata list carries no body")
}
