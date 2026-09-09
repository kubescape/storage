package file

// Shared test environment for the ObjectStore (SQLite-native ContainerProfile
// backend) tests: one pool, the ObjectStore, a legacy StorageImpl over the SAME
// pool carrying the kind-ownership guard (the production wiring under
// config.ContainerProfileSqliteBackend), a real ContainerProfileProcessor, and
// a per-connection statement authorizer that records every table an
// operation touched — the instrument INV-1 and INV-4 assert on.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

const testCPPrefix = "/spdx.softwarecomposition.kubescape.io/containerprofile/"

// tableAction is one authorizer observation: an SQLite action type on a table
// (SQLITE_READ / INSERT / UPDATE / DELETE), recorded per connection.
type tableAction struct {
	op    sqlite.OpType
	table string
	conn  *sqlite.Conn
	// txn is set for BEGIN/COMMIT/ROLLBACK observations (Operation()).
	txn string
}

// tableRecorder is installed through PoolOptions.Authorizer on every
// connection; it records every table-touching action from connection
// creation on and is never windowed off: a statement first prepared outside
// a recording window re-executes later through the connection's statement
// cache with no authorizer call, so a windowed recorder would miss exactly
// the statements prepared during setup. Tests take a mark and read what came
// after it. Safe from any goroutine.
type tableRecorder struct {
	mu      sync.Mutex
	actions []tableAction
}

func (r *tableRecorder) authorizer(conn *sqlite.Conn) sqlite.Authorizer {
	return sqlite.AuthorizeFunc(func(a sqlite.Action) sqlite.AuthResult {
		switch a.Type() {
		case sqlite.OpRead, sqlite.OpInsert, sqlite.OpUpdate, sqlite.OpDelete:
			if t := a.Table(); t != "" {
				r.mu.Lock()
				r.actions = append(r.actions, tableAction{op: a.Type(), table: t, conn: conn})
				r.mu.Unlock()
			}
		case sqlite.OpTransaction:
			r.mu.Lock()
			r.actions = append(r.actions, tableAction{op: a.Type(), conn: conn, txn: a.Operation()})
			r.mu.Unlock()
		}
		return sqlite.AuthResultOK
	})
}

// mark returns the current position; since returns everything recorded
// after a mark.
func (r *tableRecorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.actions)
}

func (r *tableRecorder) since(mark int) []tableAction {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tableAction(nil), r.actions[mark:]...)
}

func isWriteOp(op sqlite.OpType) bool {
	return op == sqlite.OpInsert || op == sqlite.OpUpdate || op == sqlite.OpDelete
}

// touched returns the set of tables among the recorded actions, optionally
// restricted to writes.
func touchedTables(actions []tableAction, writesOnly bool) map[string]bool {
	out := map[string]bool{}
	for _, a := range actions {
		if a.table == "" || (writesOnly && a.op == sqlite.OpRead) {
			continue
		}
		out[a.table] = true
	}
	return out
}

type objectStoreEnv struct {
	t         *testing.T
	ctx       context.Context
	dir       string
	dbPath    string
	pool      *sqlitemigration.Pool
	store     *ObjectStore
	cp        *objectStoreCPStorage
	legacy    *StorageImpl
	legacyFs  afero.Fs
	processor *ContainerProfileProcessor
	wd        *WatchDispatcher
	scheme    *runtime.Scheme
	rec       *tableRecorder
	// fixture is the non-pool handle tests seed state through (CR-2b).
	fixture   *sqlite.Conn
	tpl       softwarecomposition.ContainerProfile
	baseNm    string
	ns        string
	baseKey   string
	now       time.Time
}

type envOption func(*envConfig)

type envConfig struct {
	poolSize        int
	checkpointBytes int64
	checkpointEvery time.Duration
	autoCheckpoint  bool
}

func withPoolSize(n int) envOption { return func(c *envConfig) { c.poolSize = n } }
func withCheckpoint(bytes int64, every time.Duration) envOption {
	return func(c *envConfig) { c.checkpointBytes = bytes; c.checkpointEvery = every }
}

// newObjectStoreEnv builds the environment. singleWriterEnabled stays in its
// default (true) position: the legacy instance is the flag-default reference.
func newObjectStoreEnv(t *testing.T, opts ...envOption) *objectStoreEnv {
	t.Helper()
	cfg := envConfig{poolSize: DefaultPoolSize, checkpointEvery: time.Hour}
	for _, o := range opts {
		o(&cfg)
	}
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "metadata.sq3")
	rec := &tableRecorder{}
	pool := NewPoolWithOptions(dbPath, PoolOptions{
		Size:                  cfg.poolSize,
		BusyTimeout:           5 * time.Second,
		DisableAutoCheckpoint: !cfg.autoCheckpoint,
		Authorizer:            rec.authorizer,
	})
	// AC-G1, registered first so it runs after the store and pool closed.
	armUngatedWriteCheck(t, pool)
	sch := runtime.NewScheme()
	install.Install(sch)
	wd := NewWatchDispatcher()

	legacyFs := afero.NewMemMapFs()
	legacy := NewStorageImpl(legacyFs, DefaultStorageRoot, pool, wd, sch).(*StorageImpl)
	legacy.SetForeignKinds(IsContainerProfileKind)

	processor := NewContainerProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxContainerProfileSize: 40000}, nil)
	processor.Interval = 0
	processor.Workers = 1
	processor.DeleteThreshold = 24 * time.Hour

	store, err := NewObjectStore(pool, dbPath, wd, sch, processor, legacy, ObjectStoreOptions{
		CheckpointThresholdBytes: cfg.checkpointBytes,
		CheckpointInterval:       cfg.checkpointEvery,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
		closed := make(chan error, 1)
		go func() { closed <- pool.Close() }()
		select {
		case err := <-closed:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Errorf("pool.Close did not return: a connection was never returned (K-5)")
		}
	})

	content, err := os.ReadFile("testdata/p1.json")
	require.NoError(t, err)
	var tpl softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(content, &tpl))
	baseNm, _ := SplitProfileName(tpl.Name)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return &objectStoreEnv{
		t: t, ctx: ctx, dir: dir, dbPath: dbPath, pool: pool, store: store,
		cp:        processor.ContainerProfileStorage.(*objectStoreCPStorage),
		legacy:    legacy, legacyFs: legacyFs, processor: processor, wd: wd, scheme: sch, rec: rec,
		fixture:   openFixtureConn(t, pool, dbPath, 5*time.Second),
		tpl: tpl, baseNm: baseNm, ns: tpl.Namespace,
		baseKey: testCPPrefix + tpl.Namespace + "/" + baseNm,
		now:     time.Now().Round(0),
	}
}

func (e *objectStoreEnv) tsKey(suffix string) string { return e.baseKey + "-" + suffix }

func (e *objectStoreEnv) reportTimestamp(n int) string {
	return e.now.Add(time.Duration(n-10) * time.Minute).String()
}

// ts clones the template as report n of its series under suffix, chained to
// report n-1 so consolidation sees one continuous series.
func (e *objectStoreEnv) ts(suffix string, n int, status, completion string) *softwarecomposition.ContainerProfile {
	p := e.tpl.DeepCopy()
	p.Name = e.baseNm + "-" + suffix
	p.ResourceVersion = ""
	p.UID = uuid.NewUUID()
	prev := "0001-01-01 00:00:00 +0000 UTC"
	if n > 1 {
		prev = e.reportTimestamp(n - 1)
	}
	p.Annotations[helpersv1.ReportTimestampMetadataKey] = e.reportTimestamp(n)
	p.Annotations[helpersv1.PreviousReportTimestampMetadataKey] = prev
	p.Annotations[helpersv1.StatusMetadataKey] = status
	p.Annotations[helpersv1.CompletionMetadataKey] = completion
	return p
}

// plain returns a non-TS profile named name in the template's namespace.
func (e *objectStoreEnv) plain(name string) *softwarecomposition.ContainerProfile {
	p := e.tpl.DeepCopy()
	p.Name = name
	p.ResourceVersion = ""
	p.UID = uuid.NewUUID()
	delete(p.Annotations, helpersv1.ReportSeriesIdMetadataKey)
	return p
}

func (e *objectStoreEnv) key(name string) string { return testCPPrefix + e.ns + "/" + name }

func (e *objectStoreEnv) create(p *softwarecomposition.ContainerProfile) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(e.t, e.store.Create(e.ctx, e.key(p.Name), p, out, 0))
	return out
}

func (e *objectStoreEnv) get(key string) (*softwarecomposition.ContainerProfile, error) {
	out := &softwarecomposition.ContainerProfile{}
	err := e.store.Get(e.ctx, key, storage.GetOptions{}, out)
	return out, err
}

func (e *objectStoreEnv) mustGet(key string) *softwarecomposition.ContainerProfile {
	e.t.Helper()
	out, err := e.get(key)
	require.NoError(e.t, err)
	return out
}

func (e *objectStoreEnv) tick() {
	e.t.Helper()
	require.NoError(e.t, e.processor.ConsolidateTimeSeries(e.ctx))
}

// withConn runs fn on a pool connection: reads only. A write here would be
// an ungated write under AC-G1; seed state through withFixture instead.
func (e *objectStoreEnv) withConn(fn func(conn *sqlite.Conn)) {
	e.t.Helper()
	conn, err := e.pool.Take(e.ctx)
	require.NoError(e.t, err)
	defer e.pool.Put(conn)
	fn(conn)
}

// withFixture runs fn on the non-pool fixture handle (CR-2b).
func (e *objectStoreEnv) withFixture(fn func(conn *sqlite.Conn)) {
	e.t.Helper()
	fn(e.fixture)
}

// dbRow is what the tables hold for one key, as seen from a fresh connection.
type dbRow struct {
	metaExists    bool
	payloadExists bool
	rv            *int64
	uid           *string
	jsonRV        string
	jsonUID       string
	tsRows        int
}

// inspect reads the INV-2 view of key from its own connection (a snapshot no
// in-flight transaction can influence).
func (e *objectStoreEnv) inspect(key string) dbRow {
	e.t.Helper()
	var row dbRow
	e.withConn(func(conn *sqlite.Conn) {
		row = inspectRow(e.t, conn, key)
	})
	return row
}

func inspectRow(t *testing.T, conn *sqlite.Conn, key string) dbRow {
	t.Helper()
	_, _, kind, _, ns, name := K8sPathToKeys(key)
	var row dbRow
	require.NoError(t, sqlitex.Execute(conn,
		`SELECT rv, uid, json_extract(CAST(metadata AS TEXT), '$.resourceVersion'), json_extract(CAST(metadata AS TEXT), '$.uid')
			FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{kind, ns, name}, ResultFunc: func(stmt *sqlite.Stmt) error {
			row.metaExists = true
			if stmt.ColumnType(0) != sqlite.TypeNull {
				v := stmt.ColumnInt64(0)
				row.rv = &v
			}
			if stmt.ColumnType(1) != sqlite.TypeNull {
				v := stmt.ColumnText(1)
				row.uid = &v
			}
			row.jsonRV = stmt.ColumnText(2)
			row.jsonUID = stmt.ColumnText(3)
			return nil
		}}))
	require.NoError(t, sqlitex.Execute(conn,
		`SELECT 1 FROM payloads WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{kind, ns, name}, ResultFunc: func(*sqlite.Stmt) error { row.payloadExists = true; return nil }}))
	require.NoError(t, sqlitex.Execute(conn,
		`SELECT count(*) FROM time_series WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{NormalizeContainerProfileKind(kind), ns, name}, ResultFunc: func(stmt *sqlite.Stmt) error { row.tsRows = int(stmt.ColumnInt64(0)); return nil }}))
	return row
}

// assertINV2 asserts the design's INV-2 for key on a fresh connection:
// metadata row ⇔ payloads row; rv == json resourceVersion; uid == json uid.
func assertINV2(t *testing.T, conn *sqlite.Conn, key string) {
	t.Helper()
	row := inspectRow(t, conn, key)
	require.Equal(t, row.metaExists, row.payloadExists, "INV-2: metadata row (%v) must exist iff payloads row (%v) for %s", row.metaExists, row.payloadExists, key)
	if !row.metaExists {
		return
	}
	require.NotNil(t, row.rv, "INV-2: rv column NULL for %s", key)
	require.NotNil(t, row.uid, "INV-2: uid column NULL for %s", key)
	require.Equal(t, strings.TrimSpace(row.jsonRV), strconv.FormatInt(*row.rv, 10), "INV-2: rv column != json resourceVersion for %s", key)
	require.Equal(t, row.jsonUID, *row.uid, "INV-2: uid column != json uid for %s", key)
}

// allCPKeys lists every containerprofile key present in metadata or payloads.
func allCPKeys(t *testing.T, conn *sqlite.Conn) []string {
	t.Helper()
	seen := map[string]bool{}
	var keys []string
	add := func(stmt *sqlite.Stmt) error {
		k := testCPPrefix + stmt.ColumnText(0) + "/" + stmt.ColumnText(1)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
		return nil
	}
	require.NoError(t, sqlitex.Execute(conn, `SELECT namespace, name FROM metadata WHERE kind = 'containerprofile'`, &sqlitex.ExecOptions{ResultFunc: add}))
	require.NoError(t, sqlitex.Execute(conn, `SELECT namespace, name FROM payloads WHERE kind = 'containerprofile'`, &sqlitex.ExecOptions{ResultFunc: add}))
	return keys
}

// canonicalCP returns a copy of cp normalised for cross-codec comparison: the
// two documented codec deltas are removed (creationTimestamp truncated to
// seconds in UTC; empty collections nilified) plus raw header JSON compacted
// (gob keeps the bytes verbatim, encoding/json compacts RawMessage on output).
// Everything else must match exactly.
func canonicalCP(cp *softwarecomposition.ContainerProfile) *softwarecomposition.ContainerProfile {
	out := cp.DeepCopy()
	if !out.CreationTimestamp.IsZero() {
		out.CreationTimestamp = metav1.NewTime(out.CreationTimestamp.Time.Truncate(time.Second).UTC())
	}
	// Header values are emitted by the endpoint analyzer in map-iteration
	// order (nondeterministic on either backend), so canonicalise them: parse,
	// sort each header's values, re-marshal (sorted keys, compact).
	compactHeaders := func(eps []softwarecomposition.HTTPEndpoint) {
		for i := range eps {
			if len(eps[i].Headers) == 0 {
				continue
			}
			var hdr map[string][]string
			if err := json.Unmarshal(eps[i].Headers, &hdr); err != nil {
				var buf bytes.Buffer
				if err := json.Compact(&buf, eps[i].Headers); err == nil {
					eps[i].Headers = buf.Bytes()
				}
				continue
			}
			for k := range hdr {
				sort.Strings(hdr[k])
			}
			if b, err := json.Marshal(hdr); err == nil {
				eps[i].Headers = b
			}
		}
	}
	compactHeaders(out.Spec.Endpoints)
	for i := range out.Spec.Containers {
		compactHeaders(out.Spec.Containers[i].Endpoints)
	}
	for i := range out.Spec.InitContainers {
		compactHeaders(out.Spec.InitContainers[i].Endpoints)
	}
	for i := range out.Spec.EphemeralContainers {
		compactHeaders(out.Spec.EphemeralContainers[i].Endpoints)
	}
	nilifyEmpty(reflect.ValueOf(out).Elem())
	return out
}

// nilifyEmpty sets every zero-length slice or map reachable from v to nil.
func nilifyEmpty(v reflect.Value) {
	switch v.Kind() {
	case reflect.Ptr, reflect.Interface:
		if !v.IsNil() {
			nilifyEmpty(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				nilifyEmpty(v.Field(i))
			}
		}
	case reflect.Slice:
		if v.Len() == 0 {
			if v.CanSet() {
				v.Set(reflect.Zero(v.Type()))
			}
			return
		}
		for i := 0; i < v.Len(); i++ {
			nilifyEmpty(v.Index(i))
		}
	case reflect.Map:
		if v.Len() == 0 && v.CanSet() {
			v.Set(reflect.Zero(v.Type()))
			return
		}
		for _, k := range v.MapKeys() {
			mv := v.MapIndex(k)
			if mv.Kind() == reflect.Struct || mv.Kind() == reflect.Ptr {
				cp := reflect.New(mv.Type()).Elem()
				cp.Set(mv)
				nilifyEmpty(cp)
				v.SetMapIndex(k, cp)
			}
		}
	}
}
