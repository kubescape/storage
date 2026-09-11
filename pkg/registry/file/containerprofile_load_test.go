package file

// Mixed-load harness for the ContainerProfile write/read/consolidate paths.
// It reproduces the incident's concurrency shape — node-agent continuously
// writing ContainerProfile time series while background consolidation runs
// and REST clients (CVE scan / network-policy check) read the same keys.
//
// This file holds three tests with DIFFERENT statuses:
//
//   - TestContainerProfileLockFailFast — a committed REGRESSION TEST for the
//     fail-fast lock backstop. Deterministic, ~1s, runs in `go test ./...`.
//
//   - TestPerfABRound — Tier B of the measurement harness (design: A.13.4 of
//     .omc/plans/raw-write-bypass-elimination.md). One round of FIXED WORK
//     with closed-loop clients on the production shape (pool 10, 8 shards,
//     Workers = pool/4, GOMAXPROCS=8), reported as JSON plus benchstat-format
//     lines. It never has a pass/fail threshold of its own: hack/perf-ab.sh
//     runs it interleaved for a merge-base build and a HEAD build on the same
//     machine in the same window and decides RELATIVELY (`make perf-ab`).
//     Gated on PERF_AB_OUT=<file>; skipped otherwise.
//
//   - TestContainerProfileLoad — the original time-boxed diagnostic
//     (LOAD_TEST=1), kept for exploration with the env tunables below. Its
//     absolute numbers are machine-dependent and never a gate; use Tier B
//     for any before/after claim.
//
// Both load tests share runLoadScenario.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	dto "github.com/prometheus/client_model/go"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/component-base/metrics/legacyregistry"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// perfABHarnessVersion is echoed in every round's JSON. hack/perf-ab.sh
// overlays this file onto the base worktree, so both arms must report the
// same value; the driver refuses to compare rounds that do not.
const perfABHarnessVersion = "4"

// ---- tunables (documented defaults; overridable via env for exploration) ----

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// loadBackend selects the storage backend a round exercises: "legacy" (the
// row+gob-file StorageImpl, default) or "objectstore" (the SQLite-native
// ContainerProfile backend, config.ContainerProfileSqliteBackend). It is NOT
// part of the effective-config echo, so hack/perf-ab.sh can run the two arms of
// an A/B from the same commit with PERF_AB_BASE_ENV / PERF_AB_HEAD_ENV.
func loadBackend() string {
	if b := os.Getenv("PERF_AB_BACKEND"); b != "" {
		return b
	}
	return "legacy"
}

func loadPoolSize() int      { return envInt("LOAD_POOL", 6) }
func loadWriters() int       { return envInt("LOAD_WRITERS", 6) }
func loadReaders() int       { return envInt("LOAD_READERS", 25) }
func loadUpdaters() int      { return envInt("LOAD_UPDATERS", 3) }
func loadConsolidators() int { return envInt("LOAD_CONSOLIDATORS", 1) }
func loadLegacyWriters() int { return envInt("LOAD_LEGACY_WRITERS", 2) }
func loadLegacySizeKB() int  { return envInt("LOAD_LEGACY_KB", 1024) }
func loadCleanupRows() int   { return envInt("LOAD_CLEANUP_ROWS", 300) }

// loadHotKeys selects the same-key contention shape: every updater and one
// writer target base key 0 instead of being spread across the 12 templates.
func loadHotKeys() bool { return os.Getenv("LOAD_HOT_KEYS") == "1" }

// loadProcessorWorkers returns the worker bound the processor uses in the
// benchmark. Kept a helper so a baseline without the Workers field can be
// adapted with a single edit.
func loadProcessorWorkers() int {
	return max(1, loadPoolSize()/4)
}

func loadDuration() time.Duration {
	return time.Duration(envInt("LOAD_SECONDS", 6)) * time.Second
}

// loadConfig is one load scenario. Ops fields > 0 select fixed-work mode
// (each client performs exactly that many operations, then the consolidator
// runs ExtraTicks more passes and the run ends); otherwise the run is
// time-boxed by Duration.
type loadConfig struct {
	PoolSize      int
	Workers       int
	Writers       int
	Readers       int
	Updaters      int
	Listers       int
	Consolidators int

	WriterOps  int
	ReaderOps  int
	UpdaterOps int
	ListerOps  int
	ExtraTicks int
	Duration   time.Duration

	// Legacy-kind traffic through the same StorageImpl the CP scenario's
	// GetSbom reads (T-G3 of write-gate-sharing): LegacyWriters clients each
	// run LegacyOps create → update → delete cycles, alternating a sbomsyft
	// object of LegacySizeKB (gob-encoded before the commit; the hold is a
	// row plus a rename regardless of size) and a small vulnerability
	// manifest. CleanupRows unreferenced sbomsyft rows are seeded in their own
	// namespace and one cleanup tick reclaims them concurrently with the run.
	LegacyWriters int
	LegacyOps     int
	LegacySizeKB  int
	CleanupRows   int

	WriterSleep  time.Duration
	ReaderSleep  time.Duration
	UpdaterSleep time.Duration
	ListerSleep  time.Duration
	LegacySleep  time.Duration
	// TickInterval is the gap between consolidation passes; 0 is a zero-gap
	// loop (the harshest diagnostic setting, and a livelock generator).
	TickInterval time.Duration
	BusyTimeout  time.Duration
	// RequestTimeout is each REST-facing call's context deadline.
	RequestTimeout time.Duration
	// HotKeys concentrates every updater and writer 0 on base key 0 (the
	// consolidator's write to that base key then races all the updaters):
	// the deliberate same-key shape for update-p99 and conflict rate. It is
	// part of the effective-config echo, so a hot-keys round never pairs with
	// a spread one.
	HotKeys bool
	// CollapseTTL, when > 0, pins collapseSettingsTTL for the round. The
	// 10 s default makes a consolidation save that has already written refresh
	// the CollapseConfiguration cache on a second connection; the absent CR's
	// DeleteMetadata then waits on the write lock the same goroutine holds
	// until the busy timeout, freezing every shard commit with it. Whether a
	// round crosses a TTL boundary is wall-clock phase, not the change under
	// test, so perf-ab rounds pin it; the stall itself is a bug in its own
	// right, visible in the over-one-sec row of an unpinned run.
	CollapseTTL time.Duration
}

// perfABConfig is Tier B's pinned production shape.
func perfABConfig() loadConfig {
	return loadConfig{
		PoolSize:       DefaultPoolSize,
		Workers:        max(1, DefaultPoolSize/4),
		Writers:        6,
		Readers:        25,
		Updaters:       3,
		Listers:        2,
		Consolidators:  1,
		WriterOps:      2400,
		ReaderOps:      12000,
		UpdaterOps:     1200,
		ListerOps:      300,
		ExtraTicks:     3,
		LegacyWriters:  2,
		LegacyOps:      60,
		LegacySizeKB:   1024,
		CleanupRows:    300,
		TickInterval:   250 * time.Millisecond,
		BusyTimeout:    5 * time.Second,
		RequestTimeout: 15 * time.Second,
		CollapseTTL:    time.Hour,
		HotKeys:        loadHotKeys(),
	}
}

// diagnosticConfig is TestContainerProfileLoad's env-tunable time-boxed shape.
func diagnosticConfig() loadConfig {
	return loadConfig{
		PoolSize:       loadPoolSize(),
		Workers:        loadProcessorWorkers(),
		Writers:        loadWriters(),
		Readers:        loadReaders(),
		Updaters:       loadUpdaters(),
		Consolidators:  loadConsolidators(),
		LegacyWriters:  loadLegacyWriters(),
		LegacySizeKB:   loadLegacySizeKB(),
		CleanupRows:    loadCleanupRows(),
		Duration:       loadDuration(),
		WriterSleep:    time.Duration(envInt("LOAD_WRITER_SLEEP_MS", 10)) * time.Millisecond,
		ReaderSleep:    time.Duration(envInt("LOAD_READER_SLEEP_MS", 3)) * time.Millisecond,
		UpdaterSleep:   time.Duration(envInt("LOAD_UPDATER_SLEEP_MS", 20)) * time.Millisecond,
		LegacySleep:    time.Duration(envInt("LOAD_LEGACY_SLEEP_MS", 50)) * time.Millisecond,
		TickInterval:   time.Duration(envInt("LOAD_CONSOLIDATOR_SLEEP_MS", 0)) * time.Millisecond,
		BusyTimeout:    5 * time.Second,
		RequestTimeout: 15 * time.Second,
		HotKeys:        loadHotKeys(),
	}
}

func (c loadConfig) fixedWork() bool {
	return c.WriterOps > 0 || c.ReaderOps > 0 || c.UpdaterOps > 0 || c.ListerOps > 0 || c.LegacyOps > 0
}

// Legacy-kind fixtures for T-G3.
const (
	loadLegacyNS  = "load-legacy"  // the legacy writers' namespace
	loadCleanupNS = "load-cleanup" // the seeded rows one cleanup tick reclaims
)

func loadLegacyKey(kind, ns, name string) string {
	return K8sKeysToPath("", "spdx.softwarecomposition.kubescape.io", kind, "", ns, name)
}

// loadSizedSBOM builds a sbomsyft object whose gob encoding is roughly sizeKB
// (each catalogued file is ~80 bytes encoded).
func loadSizedSBOM(name, ns string, sizeKB int) *softwarecomposition.SBOMSyft {
	n := max(1, sizeKB*1024/80)
	files := make([]softwarecomposition.SyftFile, n)
	for i := range files {
		files[i] = softwarecomposition.SyftFile{ID: strconv.Itoa(i), Location: softwarecomposition.Coordinates{RealPath: fmt.Sprintf("/usr/lib/x86_64-linux-gnu/lib-%08d.so.1", i), FileSystemID: "sha256:layer"}}
	}
	return &softwarecomposition.SBOMSyft{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Annotations: map[string]string{helpersv1.ImageIDMetadataKey: "sha256:" + name}},
		Spec:       softwarecomposition.SBOMSyftSpec{Syft: softwarecomposition.SyftDocument{Files: files}},
	}
}

// loadCleanupFetcher lists the cleanup namespace with nothing running, so
// every seeded row is reclaimed by the tick.
type loadCleanupFetcher struct{}

func (loadCleanupFetcher) ListNamespaces(*sqlite.Conn) ([]string, error) {
	return []string{loadCleanupNS}, nil
}
func (loadCleanupFetcher) FetchResources(string) (ResourceMaps, error) {
	return ResourceMaps{
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}, nil
}

func loadLabelUpdate(input k8sruntime.Object, _ storage.ResponseMeta) (k8sruntime.Object, *uint64, error) {
	m := input.(metav1.Object)
	labels := m.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels["load"] = strconv.FormatInt(time.Now().UnixNano(), 36)
	m.SetLabels(labels)
	return input, nil, nil
}

// cpTemplate is one testdata TS ContainerProfile plus the derived base
// (consolidated) key that REST readers GET and the consolidator writes.
type cpTemplate struct {
	profile softwarecomposition.ContainerProfile
	baseKey string // /spdx.../containerprofile/<ns>/<baseName>  (no ts suffix)
	baseNm  string // name without the ts suffix
	ns      string
}

func loadTemplates(t *testing.T) []cpTemplate {
	t.Helper()
	var out []cpTemplate
	for i := 1; i <= 12; i++ {
		content, err := os.ReadFile(fmt.Sprintf("testdata/p%d.json", i))
		require.NoError(t, err)
		var p softwarecomposition.ContainerProfile
		require.NoError(t, json.Unmarshal(content, &p))
		baseNm, _ := SplitProfileName(p.Name)
		out = append(out, cpTemplate{
			profile: p,
			baseNm:  baseNm,
			ns:      p.Namespace,
			baseKey: "/spdx.softwarecomposition.kubescape.io/containerprofile/" + p.Namespace + "/" + baseNm,
		})
	}
	return out
}

// latencyRec accumulates per-op latencies and error classes (mutex only on
// append; cheap relative to the storage ops themselves).
type latencyRec struct {
	mu          sync.Mutex
	name        string
	samples     []time.Duration
	errServerTO int64 // fail-fast ServerTimeout, or the request context's own deadline
	errTakeConn int64 // "take connection" pool exhaustion
	errOther    int64
	okCount     int64
	overOneSec  int64
	overFiveSec int64
}

func (r *latencyRec) record(d time.Duration, err error) {
	r.mu.Lock()
	r.samples = append(r.samples, d)
	r.mu.Unlock()
	if d > time.Second {
		atomic.AddInt64(&r.overOneSec, 1)
	}
	if d > 5*time.Second {
		atomic.AddInt64(&r.overFiveSec, 1)
	}
	switch {
	case err == nil:
		atomic.AddInt64(&r.okCount, 1)
	case isServerTimeoutErr(err):
		atomic.AddInt64(&r.errServerTO, 1)
	case isTakeConnErr(err):
		atomic.AddInt64(&r.errTakeConn, 1)
	default:
		atomic.AddInt64(&r.errOther, 1)
	}
}

// latencyStats is one client class's row in a loadReport.
type latencyStats struct {
	Ops           int64   `json:"ops"`
	OK            int64   `json:"ok"`
	ServerTimeout int64   `json:"server_timeout"`
	TakeConn      int64   `json:"take_conn"`
	Other         int64   `json:"other"`
	OverOneSec    int64   `json:"over_one_sec"`
	OverFiveSec   int64   `json:"over_five_sec"`
	P50Ms         float64 `json:"p50_ms"`
	P95Ms         float64 `json:"p95_ms"`
	P99Ms         float64 `json:"p99_ms"`
	MaxMs         float64 `json:"max_ms"`
	TotalS        float64 `json:"total_s"`
}

func (r *latencyRec) stats() latencyStats {
	r.mu.Lock()
	s := append([]time.Duration(nil), r.samples...)
	r.mu.Unlock()
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	pct := func(p float64) float64 {
		if len(s) == 0 {
			return 0
		}
		idx := int(p / 100 * float64(len(s)-1))
		return float64(s[idx]) / float64(time.Millisecond)
	}
	out := latencyStats{
		Ops:           int64(len(s)),
		OK:            atomic.LoadInt64(&r.okCount),
		ServerTimeout: atomic.LoadInt64(&r.errServerTO),
		TakeConn:      atomic.LoadInt64(&r.errTakeConn),
		Other:         atomic.LoadInt64(&r.errOther),
		OverOneSec:    atomic.LoadInt64(&r.overOneSec),
		OverFiveSec:   atomic.LoadInt64(&r.overFiveSec),
		P50Ms:         pct(50),
		P95Ms:         pct(95),
		P99Ms:         pct(99),
	}
	var total time.Duration
	for _, d := range s {
		total += d
	}
	out.TotalS = total.Seconds()
	if len(s) > 0 {
		out.MaxMs = float64(s[len(s)-1]) / float64(time.Millisecond)
	}
	return out
}

func (r *latencyRec) report(t *testing.T) {
	s := r.stats()
	t.Logf("== %s ==", r.name)
	t.Logf("  ops=%d ok=%d | errs: serverTimeout=%d takeConn=%d other=%d",
		s.Ops, s.OK, s.ServerTimeout, s.TakeConn, s.Other)
	if s.Ops == 0 {
		return
	}
	t.Logf("  p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms", s.P50Ms, s.P95Ms, s.P99Ms, s.MaxMs)
	t.Logf("  >1s=%d  >5s=%d", s.OverOneSec, s.OverFiveSec)
}

// isServerTimeoutErr covers the fail-fast contention error and the harness's
// own request-context deadline: a transient stall against reqCtx is a
// timeout, not an "other" error, so it cannot trip the hard rows on its own.
func isServerTimeoutErr(err error) bool {
	return apierrors.IsServerTimeout(err) || errors.Is(err, context.DeadlineExceeded)
}

func isTakeConnErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "take connection")
}

// loadPool builds a temp-dir SQLite pool identical to production NewPool EXCEPT
// for a bounded busy timeout on every connection. Production's NewPool leaves
// connections blocking indefinitely on a held write lock; under a small pool
// that turns ordinary SQLite write contention into a stall that pins every
// connection to the 60s poolContext and drowns the Go-level pool/lock
// behaviour this harness measures. This is a harness isolation choice, not a
// claim about production.
func loadPool(t *testing.T, path string, size int, busyTimeout time.Duration) *sqlitemigration.Pool {
	t.Helper()
	// The production schema (SchemaMigrations, including the ObjectStore's
	// migrations 3-4); wal_autocheckpoint=0 on every connection exactly as
	// main.go does when config.ContainerProfileSqliteBackend is on (K-3).
	return NewPoolWithOptions(path, PoolOptions{
		Size:                  size,
		BusyTimeout:           busyTimeout,
		DisableAutoCheckpoint: loadBackend() == "objectstore",
	})
}

// newLoadStorage builds a real StorageImpl + ContainerProfileProcessor over a
// temp-dir SQLite pool of the given size. Returns storage, processor, pool.
func newLoadStorage(t *testing.T, poolSize int) (*StorageImpl, *ContainerProfileProcessor, *sqlitemigration.Pool) {
	t.Helper()
	s, _, processor, pool, _, _ := newLoadStorageWith(t, poolSize, loadProcessorWorkers(), 5*time.Second)
	return s, processor, pool
}

// loadLegacyKinds is the default StorageImpl of apiserver.go — DefaultProcessor,
// the instance that serves every non-ContainerProfile kind — over the same
// pool, filesystem and (under the flag) gate as the CP instance. The legacy
// writers and the cleanup seed go through it, as REST traffic does.
type loadLegacyKinds struct {
	s       *StorageImpl
	cleanup *ResourcesCleanupHandler
}

// newLoadStorageWith builds the legacy StorageImpl (always: it serves GetSbom
// and is the flag-default reference) and, when PERF_AB_BACKEND=objectstore, the
// ObjectStore over the same pool with the legacy instance carrying the
// kind-ownership guard — the production wiring under
// config.ContainerProfileSqliteBackend. The returned storage.Interface is the
// one the load clients drive; closeStore must run before pool.Close (K-5).
// storeOwnedConns is the number of pool connections the selected backend keeps
// for its own lifetime (the ObjectStore's write gate owns one); probePoolSize
// cannot see them, so the effective pool size adds them back.
var storeOwnedConns int

func newLoadStorageWith(t *testing.T, poolSize, workers int, busyTimeout time.Duration) (*StorageImpl, storage.Interface, *ContainerProfileProcessor, *sqlitemigration.Pool, loadLegacyKinds, func()) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "load.sq3")
	_ = os.Remove(path)
	pool := loadPool(t, path, poolSize, busyTimeout)
	require.NotNil(t, pool)

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	processor := &ContainerProfileProcessor{
		DeleteThreshold:         0, // never expire during the run
		MaxContainerProfileSize: 40000,
		Workers:                 workers,
	}
	wd := NewWatchDispatcher()
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: wd,
	}
	// Exercise the real CollapseConfig provider so PreSave's cached settings
	// lookup is on the hot path.
	processor.CollapseSettings = NewCRDCollapseSettingsProvider(s)
	// The default (every-other-kind) instance and the cleanup handler over the
	// same pool and filesystem (T-G3's legacy traffic and tick).
	kinds := loadLegacyKinds{
		s:       NewStorageImpl(s.appFs, DefaultStorageRoot, pool, wd, sch).(*StorageImpl),
		cleanup: NewResourcesCleanupHandler(s.appFs, DefaultStorageRoot, pool, wd, 0, "kubescape", loadCleanupFetcher{}, false),
	}
	if loadBackend() == "objectstore" {
		s.SetForeignKinds(IsContainerProfileKind)
		kinds.s.SetForeignKinds(IsContainerProfileKind)
		// The process's one write gate, shared by the ObjectStore, both legacy
		// instances and the cleanup handler (main.go's wiring under the flag).
		gateCtx, gateCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer gateCancel()
		gate, err := NewWriteGate(gateCtx, pool)
		require.NoError(t, err)
		if os.Getenv("PERF_AB_GATE_BUSY0") == "1" {
			// AC-G3 in its strong form: with every writer gated nothing else
			// holds the write lock, so the gate's BEGIN IMMEDIATE never needs
			// the busy handler. With the handler off, any hidden contention
			// fails the transaction ("database is locked", counted under
			// err-other) instead of waiting silently — the busy-wait histogram
			// alone cannot tell a lock wait from scheduling jitter.
			gate.conn.SetBusyTimeout(0)
		}
		s.SetWriteGate(gate)
		kinds.s.SetWriteGate(gate)
		kinds.cleanup.SetWriteGate(gate)
		// NewObjectStore hands the processor its ContainerProfileStorage.
		store, err := NewObjectStore(pool, path, wd, sch, processor, s, gate, ObjectStoreOptions{})
		require.NoError(t, err)
		storeOwnedConns = 1
		return s, store, processor, pool, kinds, func() { _ = store.Close(); _ = gate.Close() }
	}
	// Interval 0 => SetStorage does not spawn the maintenance goroutine; the
	// load goroutines drive ConsolidateTimeSeries explicitly.
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))
	storeOwnedConns = 0
	return s, s, processor, pool, kinds, func() {}
}

// effectiveConfig is read back from the constructed runtime objects, not from
// the harness's inputs: hack/perf-ab.sh compares base's block against head's
// and refuses to call a comparison between two different workloads a verdict.
type effectiveConfig struct {
	HarnessVersion      string `json:"harness_version"`
	Mode                string `json:"mode"`
	PoolSize            int    `json:"pool_size"`
	Shards              int    `json:"shards"`
	Workers             int    `json:"workers"`
	GOMAXPROCS          int    `json:"gomaxprocs"`
	SingleWriterEnabled bool   `json:"single_writer_enabled"`
	Writers             int    `json:"writers"`
	Readers             int    `json:"readers"`
	Updaters            int    `json:"updaters"`
	Listers             int    `json:"listers"`
	Consolidators       int    `json:"consolidators"`
	WriterOps           int    `json:"writer_ops"`
	ReaderOps           int    `json:"reader_ops"`
	UpdaterOps          int    `json:"updater_ops"`
	ListerOps           int    `json:"lister_ops"`
	ExtraTicks          int    `json:"extra_ticks"`
	LegacyWriters       int    `json:"legacy_writers"`
	LegacyOps           int    `json:"legacy_ops"`
	LegacySizeKB        int    `json:"legacy_size_kb"`
	CleanupRows         int    `json:"cleanup_rows"`
	WriterSleepMs       int64  `json:"writer_sleep_ms"`
	ReaderSleepMs       int64  `json:"reader_sleep_ms"`
	UpdaterSleepMs      int64  `json:"updater_sleep_ms"`
	ListerSleepMs       int64  `json:"lister_sleep_ms"`
	TickIntervalMs      int64  `json:"tick_interval_ms"`
	BusyTimeoutMs       int64  `json:"busy_timeout_ms"`
	RequestTimeoutMs    int64  `json:"request_timeout_ms"`
	CollapseTTLMs       int64  `json:"collapse_ttl_ms"`
	HotKeys             bool   `json:"hot_keys"`
	BaseKeys            int    `json:"base_keys"`
	TotalClientOps      int64  `json:"total_client_ops"`
}

type histStat struct {
	Count uint64  `json:"count"`
	Sum   float64 `json:"sum"`
	P99   float64 `json:"p99"`
	// MaxBucket is the upper bound of the highest non-empty bucket: an upper
	// bound on the largest observation (AC-G3 reads the busy wait's).
	MaxBucket float64 `json:"max_bucket"`
}

// metricsSnapshot is the per-round delta of the six process-registry series
// R1.6 named (the same vocabulary Tier C scrapes from the pod), keyed by
// label set ("kind=containerprofiles,outcome=acquired").
type metricsSnapshot struct {
	LockWait           map[string]histStat `json:"lock_wait"`
	PoolWait           map[string]histStat `json:"pool_wait"`
	QueueWait          map[string]histStat `json:"queue_wait"`
	CommitTotal        map[string]float64  `json:"commit_total"`
	ConflictRetryTotal map[string]float64  `json:"conflict_retry_total"`
	QueueDepthMax      map[string]float64  `json:"queue_depth_max"`
	// The ObjectStore's own series (zero on the legacy arm): where a write
	// spent its time — queued for the gate ticket, waiting for SQLite's lock
	// at BEGIN IMMEDIATE, or holding the gate through its statements + COMMIT.
	GateWait        map[string]histStat `json:"gate_wait"`
	BusyWait        map[string]histStat `json:"busy_wait"`
	WriteHold       map[string]histStat `json:"write_hold"`
	CheckpointTotal map[string]float64  `json:"checkpoint_total"`
	CASConflict     map[string]float64  `json:"cas_conflict_total"`
	// UngatedWrite is storage_sqlite_ungated_write_total (AC-G1's production
	// counter): non-zero on the objectstore arm is the bug class.
	UngatedWrite map[string]float64 `json:"ungated_write_total"`
}

// loadReport is one round. Series is the flat view the verdict reads:
// headline latencies/throughput, contention counts and the hard rows.
type loadReport struct {
	// Backend is provenance only (legacy | objectstore); it is deliberately
	// not in Effective so an A/B of the two backends is not a CONFIG MISMATCH.
	Backend     string                  `json:"backend"`
	WriteBytes  int64                   `json:"write_bytes"`
	Effective   effectiveConfig         `json:"effective"`
	WallSeconds float64                 `json:"wall_seconds"`
	OpsPerSec   float64                 `json:"ops_per_s"`
	Classes     map[string]latencyStats `json:"classes"`
	Metrics     metricsSnapshot         `json:"metrics"`
	Series      map[string]float64      `json:"series"`
}

// runLoadScenario runs one load round and returns its report.
func runLoadScenario(t *testing.T, cfg loadConfig) loadReport {
	t.Helper()
	if cfg.CollapseTTL > 0 {
		oldTTL := collapseSettingsTTL
		collapseSettingsTTL = cfg.CollapseTTL
		defer func() { collapseSettingsTTL = oldTTL }()
	}
	legacy, s, processor, pool, kinds, closeStore := newLoadStorageWith(t, cfg.PoolSize, cfg.Workers, cfg.BusyTimeout)
	defer func() { closeStore(); _ = pool.Close() }()
	legacyKinds, cleanup := kinds.s, kinds.cleanup

	templates := loadTemplates(t)
	nsListKey := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + templates[0].ns

	// Preseed: create the base testdata TS profiles and consolidate once so
	// the base keys readers GET and updaters update exist from the start. The
	// seed tick runs single-worker: concurrent deferred transactions can lose
	// SQLite's read-to-write upgrade ("database is locked"), which is a
	// measured outcome during the run but must not make the seed partial.
	// Every profile is pinned Learning/Partial: a template that consolidates
	// into a Completed/Full base would make PreSave reject every later Create
	// for that base, and the write load would never reach the commit path.
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 30*time.Second)
	for _, tpl := range templates {
		p := tpl.profile.DeepCopy()
		p.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Learning
		p.Annotations[helpersv1.CompletionMetadataKey] = helpersv1.Partial
		key := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + p.Namespace + "/" + p.Name
		require.NoError(t, s.Create(seedCtx, key, p, nil, 0))
	}
	seedWorkers := processor.Workers
	processor.Workers = 1
	require.NoError(t, processor.ConsolidateTimeSeries(seedCtx))
	processor.Workers = seedWorkers
	// The rows one cleanup tick reclaims during the run: unreferenced sbomsyft
	// objects (small: the tick's hold is one row delete per file).
	for i := 0; i < cfg.CleanupRows; i++ {
		name := fmt.Sprintf("dead-%d", i)
		obj := &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadCleanupNS, Annotations: map[string]string{helpersv1.ImageIDMetadataKey: "sha256:" + name}}}
		require.NoError(t, legacyKinds.Create(seedCtx, loadLegacyKey("sbomsyft", loadCleanupNS, name), obj, nil, 0))
	}
	seedCancel()

	writes := &latencyRec{name: "REST Create (node-agent writers)"}
	reads := &latencyRec{name: "REST Get (CVE/netpol readers)"}
	updates := &latencyRec{name: "REST GuaranteedUpdate"}
	lists := &latencyRec{name: "REST List (metadata / fullSpec)"}
	listsMeta := &latencyRec{name: "REST List metadata"}
	listsFull := &latencyRec{name: "REST List fullSpec page"}
	ticks := &latencyRec{name: "ConsolidateTimeSeries pass"}
	legacyCreates := &latencyRec{name: "legacy Create (sbomsyft / vulnerabilitymanifest)"}
	legacyUpdates := &latencyRec{name: "legacy GuaranteedUpdate"}
	legacyDeletes := &latencyRec{name: "legacy Delete"}
	cleanupTicks := &latencyRec{name: "cleanup tick (one namespace)"}

	before := gatherStorageMetrics(t)
	writeBytesBefore := procWriteBytes()
	depthMax := newGaugeMaxSampler(t, "storage_single_writer_queue_depth", 100*time.Millisecond)

	stop := make(chan struct{})
	var clients sync.WaitGroup
	var suffixCounter int64

	reqCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), cfg.RequestTimeout)
	}
	// keepGoing reports whether a client should run its i-th op.
	keepGoing := func(i, ops int) bool {
		if ops > 0 {
			return i < ops
		}
		select {
		case <-stop:
			return false
		default:
			return true
		}
	}

	// updaterTemplate spreads the updaters over the 12 base keys; in HotKeys
	// mode they all share base key 0, which writer 0 feeds (id%12 == 0) so the
	// consolidator keeps rewriting that base key under the updaters.
	updaterTemplate := func(id int) cpTemplate {
		if cfg.HotKeys {
			return templates[0]
		}
		return templates[id%len(templates)]
	}

	start := time.Now()

	for i := 0; i < cfg.Writers; i++ {
		clients.Add(1)
		go func(id int) {
			defer clients.Done()
			for i := 0; keepGoing(i, cfg.WriterOps); i++ {
				tpl := templates[id%len(templates)]
				p := tpl.profile.DeepCopy()
				n := atomic.AddInt64(&suffixCounter, 1)
				suffix := "ld" + strconv.FormatInt(n, 36)
				p.Name = tpl.baseNm + "-" + suffix
				if p.Annotations == nil {
					p.Annotations = map[string]string{}
				}
				p.Annotations[helpersv1.ReportTimestampMetadataKey] = time.Now().Format(time.RFC3339Nano)
				p.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Learning
				p.Annotations[helpersv1.CompletionMetadataKey] = helpersv1.Partial
				p.ResourceVersion = ""
				key := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + p.Namespace + "/" + p.Name
				ctx, cancel := reqCtx()
				t0 := time.Now()
				err := s.Create(ctx, key, p, nil, 0)
				writes.record(time.Since(t0), err)
				cancel()
				if cfg.WriterSleep > 0 {
					time.Sleep(cfg.WriterSleep)
				}
			}
		}(i)
	}

	for i := 0; i < cfg.Readers; i++ {
		clients.Add(1)
		go func(id int) {
			defer clients.Done()
			for i := 0; keepGoing(i, cfg.ReaderOps); i++ {
				tpl := templates[id%len(templates)]
				ctx, cancel := reqCtx()
				t0 := time.Now()
				out := &softwarecomposition.ContainerProfile{}
				err := s.Get(ctx, tpl.baseKey, storage.GetOptions{IgnoreNotFound: true}, out)
				reads.record(time.Since(t0), err)
				cancel()
				if cfg.ReaderSleep > 0 {
					time.Sleep(cfg.ReaderSleep)
				}
			}
		}(i)
	}

	for i := 0; i < cfg.Updaters; i++ {
		clients.Add(1)
		go func(id int) {
			defer clients.Done()
			for i := 0; keepGoing(i, cfg.UpdaterOps); i++ {
				tpl := updaterTemplate(id)
				ctx, cancel := reqCtx()
				t0 := time.Now()
				// A real mutation: an identity tryUpdate is absorbed by the #315
				// DeepEqual short-circuit on both backends and never reaches
				// the CAS/commit path.
				err := s.GuaranteedUpdate(ctx, tpl.baseKey, &softwarecomposition.ContainerProfile{}, true,
					nil, loadLabelUpdate, nil)
				updates.record(time.Since(t0), err)
				cancel()
				if cfg.UpdaterSleep > 0 {
					time.Sleep(cfg.UpdaterSleep)
				}
			}
		}(i)
	}

	// Listers alternate a metadata LIST of the namespace (kubectl get) with a
	// fullSpec LIST page (the network-policy generator's read): the design's
	// PM-1 detector.
	for i := 0; i < cfg.Listers; i++ {
		clients.Add(1)
		go func(id int) {
			defer clients.Done()
			for i := 0; keepGoing(i, cfg.ListerOps); i++ {
				opts := storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata, Recursive: true}
				variant := listsMeta
				if i%2 == 1 {
					opts = storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec, Recursive: true, Predicate: storage.SelectionPredicate{Limit: 50}}
					variant = listsFull
				}
				ctx, cancel := reqCtx()
				t0 := time.Now()
				err := s.GetList(ctx, nsListKey, opts, &softwarecomposition.ContainerProfileList{})
				d := time.Since(t0)
				lists.record(d, err)
				variant.record(d, err)
				cancel()
				if cfg.ListerSleep > 0 {
					time.Sleep(cfg.ListerSleep)
				}
			}
		}(i)
	}

	// Legacy writers: create → update → delete cycles of a sized sbomsyft
	// and a small vulnerability manifest, alternating (T-G3).
	var legacyCounter int64
	for i := 0; i < cfg.LegacyWriters; i++ {
		clients.Add(1)
		go func(id int) {
			defer clients.Done()
			for i := 0; keepGoing(i, cfg.LegacyOps); i++ {
				n := atomic.AddInt64(&legacyCounter, 1)
				var key string
				var obj k8sruntime.Object
				var fresh func() k8sruntime.Object
				if i%2 == 0 {
					name := "sbom-" + strconv.FormatInt(n, 36)
					key = loadLegacyKey("sbomsyft", loadLegacyNS, name)
					obj = loadSizedSBOM(name, loadLegacyNS, cfg.LegacySizeKB)
					fresh = func() k8sruntime.Object { return &softwarecomposition.SBOMSyft{} }
				} else {
					name := "vm-" + strconv.FormatInt(n, 36)
					key = loadLegacyKey("vulnerabilitymanifest", loadLegacyNS, name)
					obj = &softwarecomposition.VulnerabilityManifest{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadLegacyNS}}
					fresh = func() k8sruntime.Object { return &softwarecomposition.VulnerabilityManifest{} }
				}
				ctx, cancel := reqCtx()
				t0 := time.Now()
				err := legacyKinds.Create(ctx, key, obj, nil, 0)
				legacyCreates.record(time.Since(t0), err)
				cancel()
				ctx, cancel = reqCtx()
				t0 = time.Now()
				err = legacyKinds.GuaranteedUpdate(ctx, key, fresh(), false, nil, loadLabelUpdate, nil)
				legacyUpdates.record(time.Since(t0), err)
				cancel()
				ctx, cancel = reqCtx()
				t0 = time.Now()
				err = legacyKinds.Delete(ctx, key, fresh(), nil, nil, nil, storage.DeleteOptions{})
				legacyDeletes.record(time.Since(t0), err)
				cancel()
				if cfg.LegacySleep > 0 {
					time.Sleep(cfg.LegacySleep)
				}
			}
		}(i)
	}

	// One cleanup tick over the seeded namespace, concurrent with everything.
	if cfg.CleanupRows > 0 {
		clients.Add(1)
		go func() {
			defer clients.Done()
			t0 := time.Now()
			err := cleanup.CleanupTask(context.Background(), map[string][]TypeCleanupHandlerFunc{"sbomsyft": {deleteByImageId}})
			cleanupTicks.record(time.Since(t0), err)
		}()
	}

	// Consolidators tick on TickInterval until told to stop; in fixed-work
	// mode they are told to stop after the clients finish plus ExtraTicks.
	clientsDone := make(chan struct{})
	tickStop := make(chan struct{})
	var consolidators sync.WaitGroup
	for i := 0; i < cfg.Consolidators; i++ {
		consolidators.Add(1)
		go func() {
			defer consolidators.Done()
			extra := 0
			for {
				t0 := time.Now()
				err := processor.ConsolidateTimeSeries(context.Background())
				ticks.record(time.Since(t0), err)
				select {
				case <-tickStop:
					return
				case <-clientsDone:
					if cfg.fixedWork() {
						extra++
						if extra >= cfg.ExtraTicks {
							return
						}
					}
				default:
				}
				if cfg.TickInterval > 0 {
					select {
					case <-time.After(cfg.TickInterval):
					case <-tickStop:
						return
					}
				}
			}
		}()
	}

	if cfg.fixedWork() {
		clients.Wait()
		close(clientsDone)
		consolidators.Wait()
	} else {
		time.Sleep(cfg.Duration)
		close(stop)
		clients.Wait()
		close(tickStop)
		consolidators.Wait()
	}
	wall := time.Since(start)
	depthMax.stop()
	after := gatherStorageMetrics(t)

	classes := map[string]latencyStats{
		"create":        writes.stats(),
		"get":           reads.stats(),
		"update":        updates.stats(),
		"list":          lists.stats(),
		"list-meta":     listsMeta.stats(),
		"list-full":     listsFull.stats(),
		"tick":          ticks.stats(),
		"legacy-create": legacyCreates.stats(),
		"legacy-update": legacyUpdates.stats(),
		"legacy-delete": legacyDeletes.stats(),
		"cleanup":       cleanupTicks.stats(),
	}
	totalOps := classes["create"].Ops + classes["get"].Ops + classes["update"].Ops + classes["list"].Ops +
		classes["legacy-create"].Ops + classes["legacy-update"].Ops + classes["legacy-delete"].Ops
	writeBytes := procWriteBytes() - writeBytesBefore
	metricsDelta := diffStorageMetrics(before, after, depthMax.max())

	mode := "time"
	if cfg.fixedWork() {
		mode = "work"
	}
	eff := effectiveConfig{
		HarnessVersion:      perfABHarnessVersion,
		Mode:                mode,
		PoolSize:            probePoolSize(pool) + storeOwnedConns,
		Shards:              len(legacy.ensureWriter().shards),
		Workers:             processor.Workers,
		GOMAXPROCS:          runtime.GOMAXPROCS(0),
		SingleWriterEnabled: singleWriterEnabled,
		Writers:             cfg.Writers,
		Readers:             cfg.Readers,
		Updaters:            cfg.Updaters,
		Listers:             cfg.Listers,
		Consolidators:       cfg.Consolidators,
		WriterOps:           cfg.WriterOps,
		ReaderOps:           cfg.ReaderOps,
		UpdaterOps:          cfg.UpdaterOps,
		ListerOps:           cfg.ListerOps,
		ExtraTicks:          cfg.ExtraTicks,
		LegacyWriters:       cfg.LegacyWriters,
		LegacyOps:           cfg.LegacyOps,
		LegacySizeKB:        cfg.LegacySizeKB,
		CleanupRows:         cfg.CleanupRows,
		WriterSleepMs:       cfg.WriterSleep.Milliseconds(),
		ReaderSleepMs:       cfg.ReaderSleep.Milliseconds(),
		UpdaterSleepMs:      cfg.UpdaterSleep.Milliseconds(),
		ListerSleepMs:       cfg.ListerSleep.Milliseconds(),
		TickIntervalMs:      cfg.TickInterval.Milliseconds(),
		BusyTimeoutMs:       cfg.BusyTimeout.Milliseconds(),
		RequestTimeoutMs:    cfg.RequestTimeout.Milliseconds(),
		CollapseTTLMs:       collapseSettingsTTL.Milliseconds(),
		HotKeys:             cfg.HotKeys,
		BaseKeys:            len(templates),
		TotalClientOps:      totalOps,
	}

	rep := loadReport{
		Backend:     loadBackend(),
		WriteBytes:  writeBytes,
		Effective:   eff,
		WallSeconds: wall.Seconds(),
		OpsPerSec:   float64(totalOps) / wall.Seconds(),
		Classes:     classes,
		Metrics:     metricsDelta,
	}
	rep.Series = map[string]float64{
		"get-p99-ms":               classes["get"].P99Ms,
		"create-p99-ms":            classes["create"].P99Ms,
		"update-p99-ms":            classes["update"].P99Ms,
		"update-p95-ms":            classes["update"].P95Ms,
		"list-p99-ms":              classes["list"].P99Ms,
		"list-p95-ms":              classes["list"].P95Ms,
		"list-p50-ms":              classes["list"].P50Ms,
		"list-meta-p95-ms":         classes["list-meta"].P95Ms,
		"list-full-p95-ms":         classes["list-full"].P95Ms,
		"write-bytes":              float64(writeBytes),
		"gate-wait-p99-ms":         1000 * maxHistP99(metricsDelta.GateWait),
		"busy-wait-p99-ms":         1000 * maxHistP99(metricsDelta.BusyWait),
		"write-hold-p99-ms":        1000 * maxHistP99(metricsDelta.WriteHold),
		"tick-p50-ms":              classes["tick"].P50Ms,
		"tick-p99-ms":              classes["tick"].P99Ms,
		"tick-total-s":             classes["tick"].TotalS,
		"ops-per-s":                rep.OpsPerSec,
		"wall-s":                   rep.WallSeconds,
		"lock-wait-timeouts":       sumHistCount(metricsDelta.LockWait, "outcome=timeout"),
		"pool-wait-timeouts":       sumHistCount(metricsDelta.PoolWait, "outcome=timeout"),
		"commit-conflict-rate-pct": conflictRatePct(metricsDelta.CommitTotal),
		"err-other":                float64(classes["create"].Other + classes["get"].Other + classes["update"].Other + classes["list"].Other + classes["tick"].Other),
		"over-five-sec":            float64(classes["create"].OverFiveSec + classes["get"].OverFiveSec + classes["update"].OverFiveSec + classes["list"].OverFiveSec + classes["tick"].OverFiveSec),
		"over-one-sec":             float64(classes["create"].OverOneSec + classes["get"].OverOneSec + classes["update"].OverOneSec + classes["list"].OverOneSec + classes["tick"].OverOneSec),
		"commit-panic":             sumByLabel(metricsDelta.CommitTotal, "outcome=panic"),
		// T-G3: the legacy kinds through the shared gate.
		"legacy-create-p99-ms": classes["legacy-create"].P99Ms,
		"legacy-update-p99-ms": classes["legacy-update"].P99Ms,
		"legacy-delete-p99-ms": classes["legacy-delete"].P99Ms,
		"cleanup-tick-ms":      classes["cleanup"].MaxMs,
		"legacy-err-other":     float64(classes["legacy-create"].Other + classes["legacy-update"].Other + classes["legacy-delete"].Other + classes["cleanup"].Other),
		"legacy-over-one-sec":  float64(classes["legacy-create"].OverOneSec + classes["legacy-update"].OverOneSec + classes["legacy-delete"].OverOneSec + classes["cleanup"].OverOneSec),
		// AC-G1's production counter and AC-G3's busy wait: both must be zero
		// on the objectstore arm (the legacy arm has no gate: 0 by construction).
		"ungated-writes":   sumByLabel(metricsDelta.UngatedWrite, ""),
		"busy-wait-max-ms": 1000 * maxHistBucket(metricsDelta.BusyWait),
	}
	for path, p99 := range holdP99ByPath(metricsDelta.WriteHold) {
		rep.Series["hold-p99-ms/"+path] = 1000 * p99
	}
	return rep
}

// holdP99ByPath is the largest per-kind p99 of storage_sqlite_write_hold_seconds
// for each path label (PM-G1's per-path bound).
func holdP99ByPath(series map[string]histStat) map[string]float64 {
	out := map[string]float64{}
	for labels, h := range series {
		if h.Count == 0 {
			continue
		}
		path := ""
		for _, l := range strings.Split(labels, ",") {
			if strings.HasPrefix(l, "path=") {
				path = strings.TrimPrefix(l, "path=")
			}
		}
		if h.P99 > out[path] {
			out[path] = h.P99
		}
	}
	return out
}

// maxHistBucket is the largest MaxBucket of a histogram family (seconds).
func maxHistBucket(series map[string]histStat) float64 {
	var m float64
	for _, h := range series {
		if h.Count > 0 && h.MaxBucket > m {
			m = h.MaxBucket
		}
	}
	return m
}

// procWriteBytes reads write_bytes from /proc/self/io (bytes the process caused
// to be sent to the storage layer); 0 when unavailable. Under WAL every payload
// byte is written twice (WAL, then checkpoint) where the legacy store writes it
// once plus a row, so the A/B records the cost as a number (design C.14).
func procWriteBytes() int64 {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "write_bytes:") {
			n, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "write_bytes:")), 10, 64)
			return n
		}
	}
	return 0
}

// probePoolSize reads the pool's capacity back from the pool itself: it takes
// connections until Take times out, then returns them all.
func probePoolSize(pool *sqlitemigration.Pool) int {
	var conns []*sqlite.Conn
	defer func() {
		for _, c := range conns {
			pool.Put(c)
		}
	}()
	for len(conns) < 256 {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		c, err := pool.Take(ctx)
		cancel()
		if err != nil {
			break
		}
		conns = append(conns, c)
	}
	return len(conns)
}

// ---- process-registry metrics ----

type rawHist struct {
	count   uint64
	sum     float64
	buckets map[float64]uint64 // upper bound -> cumulative count
}

type rawMetrics struct {
	hists    map[string]map[string]rawHist // family -> labels -> hist
	counters map[string]map[string]float64
}

var storageHistFamilies = map[string]string{
	"storage_lock_wait_duration_seconds":                "lock_wait",
	"storage_pool_wait_duration_seconds":                "pool_wait",
	"storage_single_writer_queue_wait_duration_seconds": "queue_wait",
	"storage_write_gate_wait_seconds":                   "gate_wait",
	"storage_sqlite_busy_wait_seconds":                  "busy_wait",
	"storage_sqlite_write_hold_seconds":                 "write_hold",
}

var storageCounterFamilies = map[string]string{
	"storage_single_writer_commit_total":         "commit_total",
	"storage_single_writer_conflict_retry_total": "conflict_retry_total",
	"storage_sqlite_checkpoint_total":            "checkpoint_total",
	"storage_cp_cas_conflict_total":              "cas_conflict_total",
	"storage_sqlite_ungated_write_total":         "ungated_write",
}

func labelKey(m *dto.Metric) string {
	parts := make([]string, 0, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		parts = append(parts, l.GetName()+"="+l.GetValue())
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func gatherStorageMetrics(t *testing.T) rawMetrics {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	require.NoError(t, err)
	out := rawMetrics{hists: map[string]map[string]rawHist{}, counters: map[string]map[string]float64{}}
	for _, mf := range families {
		if short, ok := storageHistFamilies[mf.GetName()]; ok {
			out.hists[short] = map[string]rawHist{}
			for _, m := range mf.GetMetric() {
				h := m.GetHistogram()
				rh := rawHist{count: h.GetSampleCount(), sum: h.GetSampleSum(), buckets: map[float64]uint64{}}
				for _, b := range h.GetBucket() {
					rh.buckets[b.GetUpperBound()] = b.GetCumulativeCount()
				}
				out.hists[short][labelKey(m)] = rh
			}
		}
		if short, ok := storageCounterFamilies[mf.GetName()]; ok {
			out.counters[short] = map[string]float64{}
			for _, m := range mf.GetMetric() {
				out.counters[short][labelKey(m)] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

// histQuantile is Prometheus's histogram_quantile over cumulative bucket
// deltas (linear interpolation within the bucket, +Inf clamps to the last
// finite bound).
func histQuantile(q float64, buckets map[float64]uint64) float64 {
	bounds := make([]float64, 0, len(buckets))
	for b := range buckets {
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)
	if len(bounds) == 0 {
		return 0
	}
	total := buckets[bounds[len(bounds)-1]]
	if total == 0 {
		return 0
	}
	rank := q * float64(total)
	lower := 0.0
	prevCount := uint64(0)
	for i, ub := range bounds {
		c := buckets[ub]
		if float64(c) >= rank {
			if math.IsInf(ub, 1) {
				if i == 0 {
					return 0
				}
				return bounds[i-1]
			}
			if c == prevCount {
				return ub
			}
			return lower + (ub-lower)*(rank-float64(prevCount))/float64(c-prevCount)
		}
		lower = ub
		prevCount = c
	}
	return bounds[len(bounds)-1]
}

func diffStorageMetrics(before, after rawMetrics, depthMax map[string]float64) metricsSnapshot {
	out := metricsSnapshot{
		LockWait:           map[string]histStat{},
		PoolWait:           map[string]histStat{},
		QueueWait:          map[string]histStat{},
		CommitTotal:        map[string]float64{},
		ConflictRetryTotal: map[string]float64{},
		QueueDepthMax:      depthMax,
		GateWait:           map[string]histStat{},
		BusyWait:           map[string]histStat{},
		WriteHold:          map[string]histStat{},
		CheckpointTotal:    map[string]float64{},
		CASConflict:        map[string]float64{},
		UngatedWrite:       map[string]float64{},
	}
	histOut := map[string]map[string]histStat{"lock_wait": out.LockWait, "pool_wait": out.PoolWait, "queue_wait": out.QueueWait,
		"gate_wait": out.GateWait, "busy_wait": out.BusyWait, "write_hold": out.WriteHold}
	for fam, series := range after.hists {
		for labels, a := range series {
			b := before.hists[fam][labels]
			delta := map[float64]uint64{}
			for ub, c := range a.buckets {
				delta[ub] = c - b.buckets[ub]
			}
			histOut[fam][labels] = histStat{Count: a.count - b.count, Sum: a.sum - b.sum, P99: histQuantile(0.99, delta), MaxBucket: histMaxBucket(delta)}
		}
	}
	counterOut := map[string]map[string]float64{"commit_total": out.CommitTotal, "conflict_retry_total": out.ConflictRetryTotal,
		"checkpoint_total": out.CheckpointTotal, "cas_conflict_total": out.CASConflict, "ungated_write": out.UngatedWrite}
	for fam, series := range after.counters {
		for labels, a := range series {
			counterOut[fam][labels] = a - before.counters[fam][labels]
		}
	}
	return out
}

// histMaxBucket is the upper bound of the lowest bucket that already holds
// every observation of the delta: an upper bound on the largest one. 0 when
// nothing was observed.
func histMaxBucket(buckets map[float64]uint64) float64 {
	bounds := make([]float64, 0, len(buckets))
	for b := range buckets {
		bounds = append(bounds, b)
	}
	sort.Float64s(bounds)
	if len(bounds) == 0 {
		return 0
	}
	total := buckets[bounds[len(bounds)-1]]
	if total == 0 {
		return 0
	}
	for _, ub := range bounds {
		if buckets[ub] == total {
			return ub
		}
	}
	return bounds[len(bounds)-1]
}

// maxHistP99 is the largest per-label p99 of a histogram family (seconds).
func maxHistP99(series map[string]histStat) float64 {
	var m float64
	for _, h := range series {
		if h.Count > 0 && h.P99 > m {
			m = h.P99
		}
	}
	return m
}

func sumHistCount(series map[string]histStat, labelContains string) float64 {
	var n float64
	for labels, h := range series {
		if strings.Contains(labels, labelContains) {
			n += float64(h.Count)
		}
	}
	return n
}

func sumByLabel(series map[string]float64, labelContains string) float64 {
	var n float64
	for labels, v := range series {
		if strings.Contains(labels, labelContains) {
			n += v
		}
	}
	return n
}

// conflictRatePct is commit_total{conflict} / commit_total{committed}, in %.
func conflictRatePct(commitTotal map[string]float64) float64 {
	committed := sumByLabel(commitTotal, "outcome=committed")
	if committed == 0 {
		return 0
	}
	return 100 * sumByLabel(commitTotal, "outcome=conflict") / committed
}

// gaugeMaxSampler samples a gauge family every interval and keeps the max per
// label set (queue depth is a gauge; its peak is the backlog signal).
type gaugeMaxSampler struct {
	mu   sync.Mutex
	maxV map[string]float64
	done chan struct{}
	wg   sync.WaitGroup
}

func newGaugeMaxSampler(t *testing.T, family string, interval time.Duration) *gaugeMaxSampler {
	t.Helper()
	g := &gaugeMaxSampler{maxV: map[string]float64{}, done: make(chan struct{})}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case <-g.done:
				return
			case <-tk.C:
				families, err := legacyregistry.DefaultGatherer.Gather()
				if err != nil {
					continue
				}
				g.mu.Lock()
				for _, mf := range families {
					if mf.GetName() != family {
						continue
					}
					for _, m := range mf.GetMetric() {
						k := labelKey(m)
						if v := m.GetGauge().GetValue(); v > g.maxV[k] {
							g.maxV[k] = v
						}
					}
				}
				g.mu.Unlock()
			}
		}
	}()
	return g
}

func (g *gaugeMaxSampler) stop() {
	close(g.done)
	g.wg.Wait()
}

func (g *gaugeMaxSampler) max() map[string]float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]float64, len(g.maxV))
	for k, v := range g.maxV {
		out[k] = v
	}
	return out
}

// TestPerfABRound is one Tier B round; see the file comment. It writes the
// round's JSON to $PERF_AB_OUT and prints benchstat-format lines.
func TestPerfABRound(t *testing.T) {
	out := os.Getenv("PERF_AB_OUT")
	if out == "" {
		t.Skip("set PERF_AB_OUT=<file> to run one perf-ab round (normally via hack/perf-ab.sh)")
	}
	rep := runLoadScenario(t, perfABConfig())
	data, err := json.MarshalIndent(rep, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(out, append(data, '\n'), 0o644))

	names := make([]string, 0, len(rep.Series))
	for n := range rep.Series {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		unit := "ms"
		switch {
		case n == "ops-per-s":
			unit = "ops/s"
		case n == "wall-s", n == "tick-total-s":
			unit = "s"
		case n == "write-bytes":
			unit = "B"
		case strings.HasSuffix(n, "-pct"):
			unit = "pct"
		case !strings.HasSuffix(n, "-ms"):
			unit = "count"
		}
		fmt.Printf("BenchmarkPerfAB/%s 1 %.4f %s\n", n, rep.Series[n], unit)
	}
	t.Logf("perf-ab round: backend=%s wall=%.2fs ops/s=%.0f write_bytes=%d effective=%+v", rep.Backend, rep.WallSeconds, rep.OpsPerSec, rep.WriteBytes, rep.Effective)
}

// TestContainerProfileLoad is the time-boxed diagnostic; see the file comment.
func TestContainerProfileLoad(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "1" {
		t.Skip("set LOAD_TEST=1 to run the ContainerProfile load/stress diagnostic")
	}
	cfg := diagnosticConfig()
	rep := runLoadScenario(t, cfg)
	t.Logf("=== ContainerProfile load diagnostic: pool=%d writers=%d readers=%d updaters=%d consolidators=%d workers=%d tickInterval=%s dur=%s wall=%.2fs ===",
		cfg.PoolSize, cfg.Writers, cfg.Readers, cfg.Updaters, cfg.Consolidators, cfg.Workers, cfg.TickInterval, cfg.Duration, rep.WallSeconds)
	for _, name := range []string{"create", "get", "update", "list", "tick"} {
		c := rep.Classes[name]
		t.Logf("== %s == ops=%d ok=%d serverTimeout=%d takeConn=%d other=%d p50=%.2fms p95=%.2fms p99=%.2fms max=%.2fms >1s=%d >5s=%d",
			name, c.Ops, c.OK, c.ServerTimeout, c.TakeConn, c.Other, c.P50Ms, c.P95Ms, c.P99Ms, c.MaxMs, c.OverOneSec, c.OverFiveSec)
	}
}

// TestContainerProfileLockFailFast is the committed regression test for the
// fail-fast lock backstop. It holds a key's write lock (simulating a long
// consolidation critical section) and fires many concurrent REST GETs at that
// key, then asserts every contended GET fails fast rather than hanging to the
// request deadline.
//
// Each contended GET must return within failFastBound as an
// apierrors.IsServerTimeout (HTTP 500 + Retry-After), NOT block to the (much
// larger) request-context deadline the way the pre-fix code did (which
// produced the incident's ~60s 504 hangs). Deterministic, ~1s, ungated.
func TestContainerProfileLockFailFast(t *testing.T) {
	// Shrink the backstop so the fail-fast path resolves quickly; this exercises
	// the real child-context timeout -> newLockTimeoutError code path, just with
	// a smaller bound (same technique as TestStorageImpl_LockContentionReturnsServerTimeout).
	oldLT := lockTimeout
	lockTimeout = 500 * time.Millisecond
	defer func() { lockTimeout = oldLT }()

	const (
		n = 20
		// failFastBound is the asserted upper bound on a contended GET. It sits
		// well above lockTimeout (500ms) to absorb CI scheduling jitter, and far
		// below reqDeadline (10s) so a regression back to "hang to the request
		// deadline" fails loudly.
		failFastBound = 2 * time.Second
		reqDeadline   = 10 * time.Second
	)
	// Pool is sized > n so every GET acquires a connection immediately and the
	// only thing it can block on is the held write lock — isolating the lock
	// backstop from connection-pool queueing.
	s, _, pool := newLoadStorage(t, n+5)
	defer func() { _ = pool.Close() }()

	key := "/spdx.softwarecomposition.kubescape.io/containerprofile/kube-system/failfast-probe"
	// Hold the write lock for the whole test so every GET contends.
	require.NoError(t, s.locks.Lock(context.Background(), key))
	defer s.locks.Unlock(key)

	rec := &latencyRec{name: "Contended GET (lock held)"}
	type result struct {
		d   time.Duration
		err error
	}
	results := make([]result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), reqDeadline)
			defer cancel()
			t0 := time.Now()
			err := s.Get(ctx, key, storage.GetOptions{IgnoreNotFound: true}, &softwarecomposition.ContainerProfile{})
			d := time.Since(t0)
			results[i] = result{d: d, err: err}
			rec.record(d, err)
		}(i)
	}
	wg.Wait()

	t.Logf("=== fail-fast regression: %d concurrent GETs on a write-locked key, lockTimeout=%s bound=%s reqDeadline=%s ===",
		n, lockTimeout, failFastBound, reqDeadline)
	rec.report(t)

	for i, r := range results {
		require.Errorf(t, r.err, "GET %d: expected a fail-fast error, got nil (lock contention silently succeeded?)", i)
		assert.Truef(t, apierrors.IsServerTimeout(r.err),
			"GET %d: expected apierrors.IsServerTimeout, got %T: %v", i, r.err, r.err)
		assert.Lessf(t, r.d, failFastBound,
			"GET %d: contended GET took %s, exceeding the %s fail-fast bound (regression to hang-to-deadline?)", i, r.d, failFastBound)
	}
}
