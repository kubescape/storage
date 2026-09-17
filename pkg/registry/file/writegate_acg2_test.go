package file

// AC-G2 of .omc/plans/write-gate-sharing.md: every read entry point returns
// promptly while SQLite's write lock is held — the generalisation of the two
// tests #401 added (TestGet_AbsentKeyDoesNotWaitOnWriter and the collapse
// provider's). A table over (read entry point) × (key state), run once per
// topology:
//
//   - AC-G2(off) — no gate; a pool connection holds BEGIN IMMEDIATE until
//     the reader returns. Repair cells must attempt a write without changing
//     metadata; other synchronous readers must not attempt writes.
//   - AC-G2(on) — see writegate_acg2_on_test.go.
//
// Key states cover both found bugs' statement on every door it has: absent
// (from #401), present, orphan (row, no file: W4), corrupt gob (row,
// truncated file: W5), wrong-type with a failing migration tool (W6a/W7a, no
// write on the list readers), wrong-type with a succeeding tool (the W6b/
// W7b/W8 rewrites); and for the cleanup tick, referenced (pure read),
// unreferenced (W9a) and file-without-row (W9b).

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

const (
	// acg2BusyTimeout lets flag-off repairs finish while the writer stays held.
	acg2BusyTimeout = time.Second
	// acg2Prompt remains the bound used by the independent gate-refusal test.
	acg2Prompt = 500 * time.Millisecond
	// This is a deadlock guard, not a read-latency acceptance threshold.
	acg2Deadline = 30 * time.Second

	acg2Kind      = "sbomsyft"
	acg2Group     = "spdx.softwarecomposition.kubescape.io"
	acg2DefaultNS = "kubescape" // the SBOM namespace (ContainerProfileProcessor.DefaultNamespace)
	acg2LiveImage = "sha256:live"
	acg2DeadImage = "sha256:dead"
)

// acg2Topology is what differs between AC-G2(off) and AC-G2(on).
type acg2Topology struct {
	name string
	on   bool
	// build constructs a fresh environment for one cell.
	build func(t *testing.T) *acg2Env
}

type acg2Env struct {
	t              *testing.T
	ctx            context.Context
	dbPath         string
	fs             afero.Fs
	pool           *sqlitemigration.Pool
	legacy         *StorageImpl
	fixture        *sqlite.Conn
	processor      *ContainerProfileProcessor
	preSaveProfile *softwarecomposition.ContainerProfile
	cleanup        *ResourcesCleanupHandler
	fetcher        *acg2Fetcher
	// hold acquires SQLite's write lock the topology's way and returns the
	// release; flag-on repairs are released after they queue.
	hold func() (release func())
	// closeStore runs before pool.Close (nil under flag-off).
	closeStore func()
	// The statement recorder observes both topologies; store and gate are flag-on only.
	store *ObjectStore
	gate  *writeGate
	rec   *tableRecorder
}

// acg2Fetcher is the cleanup tick's ResourcesFetcher: one namespace that
// lists the live image ids, plus the default namespace the SBOMs live in.
type acg2Fetcher struct {
	live mapset.Set[string]
}

func (f *acg2Fetcher) ListNamespaces(*sqlite.Conn) ([]string, error) {
	return []string{"other", acg2DefaultNS}, nil
}

func (f *acg2Fetcher) FetchResources(string) (ResourceMaps, error) {
	return ResourceMaps{
		RunningContainerImageIds:     f.live,
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}, nil
}

// newACG2Base builds the parts both topologies share: a pool with
// acg2BusyTimeout on every connection, the legacy StorageImpl over an
// in-memory filesystem, the ContainerProfileProcessor (its storage is set by
// the topology), the cleanup handler and the fixture handle.
func newACG2Base(t *testing.T, pool *sqlitemigration.Pool, dbPath string) *acg2Env {
	t.Helper()
	sch := runtime.NewScheme()
	install.Install(sch)
	wd := NewWatchDispatcher()
	fs := afero.NewMemMapFs()
	legacy := NewStorageImpl(fs, DefaultStorageRoot, pool, wd, sch).(*StorageImpl)
	processor := NewContainerProfileProcessor(acg2ProcessorConfig(), nil)
	processor.Interval = 0
	processor.Workers = 1
	fetcher := &acg2Fetcher{live: mapset.NewSet(acg2LiveImage)}
	cleanup := NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, wd, 0, acg2DefaultNS, fetcher, false)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return &acg2Env{
		t: t, ctx: ctx, dbPath: dbPath, fs: fs, pool: pool, legacy: legacy,
		fixture:   openFixtureConn(t, pool, dbPath, acg2BusyTimeout),
		processor: processor, cleanup: cleanup, fetcher: fetcher,
	}
}

// closePool is the env's last cleanup: the store first (K-5), then the pool,
// which must return promptly once every connection is back.
func (e *acg2Env) closePool() {
	if e.closeStore != nil {
		e.closeStore()
	}
	done := make(chan error, 1)
	go func() { done <- e.pool.Close() }()
	select {
	case err := <-done:
		require.NoError(e.t, err)
	case <-time.After(30 * time.Second):
		e.t.Errorf("pool.Close did not return: a connection was never returned")
	}
}

// acg2Off is AC-G2(off): today's production topology, no gate.
var acg2Off = acg2Topology{
	name: "off",
	build: func(t *testing.T) *acg2Env {
		dbPath := filepath.Join(t.TempDir(), "acg2.sq3")
		rec := &tableRecorder{}
		pool := NewPoolWithOptions(dbPath, PoolOptions{Size: 4, BusyTimeout: acg2BusyTimeout, Authorizer: rec.authorizer})
		e := newACG2Base(t, pool, dbPath)
		e.rec = rec
		t.Cleanup(e.closePool)
		e.processor.SetStorage(NewContainerProfileStorageImpl(e.legacy, pool))
		// The holder: a pool connection in an open BEGIN IMMEDIATE, the state
		// a legacy writer is in for the length of its transaction.
		e.hold = func() func() {
			conn, err := pool.Take(e.ctx)
			require.NoError(t, err)
			conn.SetInterrupt(nil)
			endFn, err := sqlitex.ImmediateTransaction(conn)
			require.NoError(t, err)
			return sync.OnceFunc(func() {
				var txErr error
				endFn(&txErr)
				require.NoError(t, txErr)
				pool.Put(conn)
			})
		}
		return e
	},
}

// ---- key states ----

func (e *acg2Env) payloadPath(key string) string {
	return makePayloadPath(filepath.Join(DefaultStorageRoot, key))
}

// seedRow writes the metadata row exactly as writeMetadata does, through the
// fixture handle.
func (e *acg2Env) seedRow(key string, obj runtime.Object) {
	e.t.Helper()
	raw, err := json.Marshal(extractFields(obj, []string{"ObjectMeta", "SchemaVersion"}))
	require.NoError(e.t, err)
	require.NoError(e.t, WriteJSON(e.fixture, key, raw))
}

func gobBytes(t *testing.T, obj runtime.Object) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, gob.NewEncoder(&buf).Encode(obj))
	return buf.Bytes()
}

func (e *acg2Env) seedPayload(key string, payload []byte) {
	e.t.Helper()
	require.NoError(e.t, afero.WriteFile(e.fs, e.payloadPath(key), payload, 0644))
}

func (e *acg2Env) seedPresent(key string, obj runtime.Object) {
	e.seedRow(key, obj)
	e.seedPayload(key, gobBytes(e.t, obj))
}

type acg2KeyState struct {
	name string
	seed func(e *acg2Env, key string, obj runtime.Object)
	// cleanupOnly states exist for the cleanup tick's referencedness axis.
	cleanupOnly bool
}

var acg2States = []acg2KeyState{
	{name: "absent", seed: func(*acg2Env, string, runtime.Object) {}},
	{name: "present", seed: func(e *acg2Env, key string, obj runtime.Object) { e.seedPresent(key, obj) }},
	{name: "orphan", seed: func(e *acg2Env, key string, obj runtime.Object) {
		e.seedPresent(key, obj)
		require.NoError(e.t, e.fs.Remove(e.payloadPath(key)))
	}},
	{name: "corrupt", seed: func(e *acg2Env, key string, obj runtime.Object) {
		// An empty payload (created, never written: a crash before the first
		// O_DIRECT block) decodes to io.EOF, the branch get() treats as
		// corrupt (W5). A partially written gob fails with gob's own "extra
		// data"/"type" errors, which get() returns without repairing.
		e.seedRow(key, obj)
		e.seedPayload(key, nil)
	}},
	{name: "wrongtype-toolfails", seed: func(e *acg2Env, key string, obj runtime.Object) {
		e.seedRow(key, obj)
		e.seedPayload(key, gobPayloadNeedingMigration(e.t))
	}},
	{name: "wrongtype-toolsucceeds", seed: func(e *acg2Env, key string, obj runtime.Object) {
		e.seedRow(key, obj)
		e.seedPayload(key, gobPayloadNeedingMigration(e.t))
	}},
	{name: "present-referenced", cleanupOnly: true, seed: func(e *acg2Env, key string, obj runtime.Object) {
		obj.(metav1.Object).SetAnnotations(map[string]string{helpersv1.ImageIDMetadataKey: acg2LiveImage})
		e.seedPresent(key, obj)
	}},
	{name: "present-unreferenced", cleanupOnly: true, seed: func(e *acg2Env, key string, obj runtime.Object) {
		obj.(metav1.Object).SetAnnotations(map[string]string{helpersv1.ImageIDMetadataKey: acg2DeadImage})
		e.seedPresent(key, obj)
	}},
	{name: "file-without-row", cleanupOnly: true, seed: func(e *acg2Env, key string, obj runtime.Object) {
		obj.(metav1.Object).SetAnnotations(map[string]string{helpersv1.ImageIDMetadataKey: acg2LiveImage})
		e.seedPayload(key, gobBytes(e.t, obj))
		raw, err := json.Marshal(extractFields(obj, []string{"ObjectMeta", "SchemaVersion"}))
		require.NoError(e.t, err)
		sidecar := strings.TrimSuffix(e.payloadPath(key), GobExt) + MetadataExt
		require.NoError(e.t, afero.WriteFile(e.fs, sidecar, raw, 0644))
	}},
}

// installACG2MigrationTool points migrationBinaryPath at one script for the
// whole matrix: it fails for a payload whose path names the tool-fails state
// and prints a valid object otherwise, so cells need no per-cell package
// state and can run in parallel.
func installACG2MigrationTool(t *testing.T) {
	t.Helper()
	scriptPath := filepath.Join(t.TempDir(), "acg2-migration.sh")
	script := "#!/bin/sh\ncase \"$2\" in *toolfails*) echo 'acg2: tool fails' >&2; exit 1;; esac\n" +
		"printf '%s' '{\"metadata\":{\"name\":\"migrated\",\"namespace\":\"" + acg2DefaultNS + "\"}}'\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0755))
	old := migrationBinaryPath
	migrationBinaryPath = scriptPath
	t.Cleanup(func() { migrationBinaryPath = old })
}

// ---- read entry points ----

type acg2ReaderClass int

const (
	// acg2GetReader reaches get(): repairs orphan/corrupt/wrong-type keys.
	acg2GetReader acg2ReaderClass = iota
	// acg2ListReader walks payload files (appendGobObjectFromFile): the only
	// write is the W8 rewrite after a succeeding migration tool.
	acg2ListReader
	// acg2MetaReader reads the metadata row only: never writes.
	acg2MetaReader
	// acg2CleanupReader is one cleanup tick: writes on the unreferenced and
	// file-without-row states.
	acg2CleanupReader
)

type acg2Reader struct {
	name   string
	class  acg2ReaderClass
	onOnly bool
	// key returns the key whose state the cell seeds; obj a fresh object of
	// its kind.
	key func(e *acg2Env, cell string) string
	obj func() runtime.Object
	// prepare, when set, runs before the key is seeded and before the lock is
	// held, and returns the measured read (for readers whose construction
	// itself reads, like the collapse provider's prime).
	prepare func(e *acg2Env, key string) func() error
	run     func(e *acg2Env, key string) error
}

func (r acg2Reader) repairs(st acg2KeyState) bool {
	switch r.class {
	case acg2GetReader:
		return st.name == "orphan" || st.name == "corrupt" || strings.HasPrefix(st.name, "wrongtype")
	case acg2ListReader:
		return st.name == "wrongtype-toolsucceeds"
	case acg2CleanupReader:
		return st.name == "present-unreferenced" || st.name == "file-without-row"
	}
	return false
}

func sbomKey(e *acg2Env, cell string) string {
	return K8sKeysToPath("", acg2Group, acg2Kind, "", acg2DefaultNS, cell)
}

func newSBOM() runtime.Object {
	return &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Namespace: acg2DefaultNS}}
}

func sbomPrefix() string { return "/" + acg2Group + "/" + acg2Kind + "/" + acg2DefaultNS }

// acg2Readers are the entry points common to both topologies; the flag-on
// file appends its own.
var acg2Readers = []acg2Reader{
	{name: "Get", class: acg2GetReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.legacy.Get(e.ctx, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	}},
	{name: "Get(metadata)", class: acg2MetaReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.legacy.Get(e.ctx, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &softwarecomposition.SBOMSyft{})
	}},
	{name: "GetWithConn", class: acg2GetReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		conn, err := e.pool.Take(e.ctx)
		if err != nil {
			return err
		}
		defer e.pool.Put(conn)
		return e.legacy.GetWithConn(e.ctx, conn, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{})
	}},
	{name: "get(noLock)", class: acg2GetReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		conn, err := e.pool.Take(e.ctx)
		if err != nil {
			return err
		}
		defer e.pool.Put(conn)
		return e.legacy.get(e.ctx, conn, key, storage.GetOptions{}, &softwarecomposition.SBOMSyft{}, noLock)
	}},
	{name: "GetList(metadata)", class: acg2MetaReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.legacy.GetList(e.ctx, sbomPrefix(), storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata, Recursive: true}, &softwarecomposition.SBOMSyftList{})
	}},
	{name: "GetList(fullSpec)", class: acg2GetReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.legacy.GetList(e.ctx, sbomPrefix(), storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec, Recursive: true}, &softwarecomposition.SBOMSyftList{})
	}},
	{name: "GetByNamespace", class: acg2ListReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.legacy.GetByNamespace(e.ctx, acg2Group, acg2Kind, acg2DefaultNS, &softwarecomposition.SBOMSyftList{})
	}},
	{name: "GetByCluster", class: acg2ListReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.legacy.GetByCluster(e.ctx, acg2Group, acg2Kind, &softwarecomposition.SBOMSyftList{})
	}},
	{name: "Count", class: acg2MetaReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		_, err := e.legacy.Count(sbomPrefix())
		return err
	}},
	{name: "Watch", class: acg2MetaReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		w, err := e.legacy.Watch(e.ctx, key, storage.ListOptions{})
		if err == nil {
			w.Stop()
		}
		return err
	}},
	{name: "GetSbom", class: acg2GetReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		ctx, cleanup, err := e.processor.ContainerProfileStorage.WithConnection(e.ctx)
		if err != nil {
			return err
		}
		defer cleanup()
		_, err = e.processor.ContainerProfileStorage.GetSbom(ctx, key)
		return err
	}},
	{name: "collapse-provider", class: acg2MetaReader,
		key: func(*acg2Env, string) string { return collapseConfigurationKey(DefaultCollapseConfigurationName) },
		obj: func() runtime.Object {
			return &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{Name: DefaultCollapseConfigurationName}}
		},
		prepare: func(e *acg2Env, key string) func() error {
			// The provider primes synchronously at construction (on the absent
			// key, as in #401's test); the key state is seeded after that, so it
			// is the background refresh that meets it. The refresh is off the
			// caller's goroutine (#401): the closure is prompt in every key
			// state, and its Get is what AC-G1 watches. Both Gets (prime +
			// refresh) are joined after the measurement, before the env closes.
			cs := &countingGetStorage{Interface: e.legacy}
			provider := NewCRDCollapseSettingsProvider(cs)
			e.t.Cleanup(func() { joinRefresh(e.t, cs) })
			return func() error { provider(); return nil }
		}},
	{name: "cleanup-tick", class: acg2CleanupReader, key: sbomKey, obj: newSBOM, run: func(e *acg2Env, key string) error {
		return e.cleanup.CleanupTask(e.ctx, map[string][]TypeCleanupHandlerFunc{acg2Kind: {deleteByImageId}})
	}},
}

func acg2ReaderStates(r acg2Reader) []acg2KeyState {
	var out []acg2KeyState
	for _, st := range acg2States {
		if st.cleanupOnly == (r.class == acg2CleanupReader) {
			out = append(out, st)
		}
	}
	return out
}

// runACG2Matrix runs every (reader × key state) cell of topo, each in a fresh
// environment, in parallel. Package state the cells share (the migration
// tool, the collapse TTL) is set once here and restored after the last cell.
func runACG2Matrix(t *testing.T, topo acg2Topology, readers []acg2Reader) {
	t.Helper()
	installACG2MigrationTool(t)
	oldTTL := collapseSettingsTTL
	collapseSettingsTTL = time.Nanosecond
	t.Cleanup(func() { collapseSettingsTTL = oldTTL })

	for _, r := range readers {
		if r.onOnly && !topo.on {
			continue
		}
		for _, st := range acg2ReaderStates(r) {
			r, st := r, st
			t.Run(r.name+"/"+st.name, func(t *testing.T) {
				t.Parallel()
				e := topo.build(t)
				cell := strings.NewReplacer("(", "-", ")", "", "/", "-").Replace(r.name + "-" + st.name)
				key := r.key(e, cell)
				obj := r.obj()
				if m, ok := obj.(metav1.Object); ok && m.GetName() == "" {
					_, _, _, _, _, name := K8sPathToKeys(key)
					m.SetName(name)
				}
				run := func() error { return r.run(e, key) }
				if r.prepare != nil {
					run = r.prepare(e, key)
				}
				st.seed(e, key, obj)

				repair := r.repairs(st)
				before, beforeErr := ReadMetadata(e.fixture, key)
				if beforeErr != nil {
					require.ErrorIs(t, beforeErr, ErrMetadataNotFound)
				}
				release := e.hold()
				// Release before registered cleanups even when an assertion fails.
				defer release()
				mark := e.rec.mark()
				start := time.Now()
				done := make(chan error, 1)
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					done <- run()
				}()
				defer func() {
					release()
					select {
					case <-stopped:
					case <-time.After(acg2Deadline):
						t.Error("reader did not stop after release")
					}
				}()
				if topo.on && repair {
					require.Eventually(t, func() bool {
						high, low := e.gate.queued()
						if high+low > 0 {
							return true
						}
						select {
						case <-stopped:
							return true
						default:
							return false
						}
					}, acg2Deadline, time.Millisecond, "repair must queue behind the held gate")
					select {
					case err := <-done:
						t.Fatalf("repair returned before the gate was released: %v", err)
					default:
					}
					release()
				}
				var err error
				select {
				case err = <-done:
				case <-time.After(acg2Deadline):
					t.Fatal("reader did not complete")
				}
				elapsed := time.Since(start)
				// The collapse provider returns cached settings and starts a separate
				// refresh, which may repair. Its contract here is caller completion
				// while held; prepare registers a cleanup to join that refresh.
				if r.name != "collapse-provider" {
					wrote := false
					for _, action := range e.rec.since(mark) {
						wrote = wrote || isWriteOp(action.op)
					}
					assert.Equal(t, repair, wrote, "repair cells must attempt writes; pure reads must not")
				}
				if topo.on && repair && (st.name == "orphan" || st.name == "corrupt" || st.name == "wrongtype-toolfails" || st.name == "present-unreferenced") {
					_, afterErr := ReadMetadata(e.fixture, key)
					assert.ErrorIs(t, afterErr, ErrMetadataNotFound, "queued repair must delete the metadata after release")
				}
				if !topo.on && repair {
					after, afterErr := ReadMetadata(e.fixture, key)
					assert.Equal(t, before, after, "repair cannot change metadata while SQLite's writer is held")
					assert.Equal(t, beforeErr, afterErr)
				}
				release()

				kind := "read"
				if repair {
					kind = "repair"
				}
				t.Logf("AC-G2(%s) %s × %s: %s cell, %s (err=%v)", topo.name, r.name, st.name, kind, elapsed, err)
				if st.name == "present" && r.class != acg2MetaReader {
					assert.NoError(t, err, "a present key must read")
				}
			})
		}
	}
}

// TestACG2_FlagOff is AC-G2(off): the legacy topology's residual, pinned.
func TestACG2_FlagOff(t *testing.T) {
	runACG2Matrix(t, acg2Off, acg2Readers)
}

// acg2ProcessorConfig is the processor config both topologies use.
func acg2ProcessorConfig() config.Config {
	return config.Config{DefaultNamespace: acg2DefaultNS, MaxContainerProfileSize: 40000}
}
