package file

// AC4 load/stress benchmark for the ContainerProfile locking/connection-pool
// hardening (PRs 1f395bc5, e4149ee5, 8983b7f7). It reproduces the incident's
// concurrency shape — node-agent continuously writing ContainerProfile time
// series while background consolidation runs and REST clients (CVE scan /
// network-policy check) read the same keys — against a deliberately small
// SQLite connection pool so REST↔consolidator contention is reproducible in a
// few seconds.
//
// This file holds two tests with DIFFERENT statuses:
//
//   - TestContainerProfileLockFailFast — a committed REGRESSION TEST for PR1
//     (fail-fast lock backstop). Deterministic, runs in ~1s, asserts every
//     contended GET returns a ServerTimeout within a fail-fast bound instead of
//     hanging to the request deadline. Runs in the normal `make test` suite
//     (NOT gated). This is the AC4 "latency under a defined bound" assertion.
//
//   - TestContainerProfileLoad — the full mixed-load reproduction. High
//     variance (a single consolidation pass can dominate the window), so it is
//     a manual DIAGNOSTIC only: gated behind LOAD_TEST=1 (t.Skip otherwise) and
//     never a CI pass/fail gate. Its value is the before/after delta between
//     this tree and edd2fb80, not any absolute latency. Run explicitly:
//
//	LOAD_TEST=1 go test ./pkg/registry/file/ -run TestContainerProfileLoad -v -timeout 300s
//
//     Tunables via env: LOAD_POOL, LOAD_WRITERS, LOAD_READERS, LOAD_UPDATERS,
//     LOAD_CONSOLIDATORS, LOAD_SECONDS, LOAD_{WRITER,READER,UPDATER}_SLEEP_MS,
//     LOAD_CONSOLIDATOR_SLEEP_MS.
//
// See the AC4 discussion in .omc/plans/ralplan-improve-the-locking-mechanism-to.md.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// ---- tunables (documented defaults; overridable via env for exploration) ----
//
// Defaults reproduce the incident shape: a small pool (production is
// DefaultPoolSize=10), modest background node-agent write load, one background
// consolidation loop, and read-dominated REST traffic (the GETs that 504'd).

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func loadPoolSize() int      { return envInt("LOAD_POOL", 6) }
func loadWriters() int       { return envInt("LOAD_WRITERS", 6) }
func loadReaders() int       { return envInt("LOAD_READERS", 25) }
func loadUpdaters() int      { return envInt("LOAD_UPDATERS", 3) }
func loadConsolidators() int { return envInt("LOAD_CONSOLIDATORS", 1) }

// Per-op client think-times. Real REST clients (CVE scan, netpol check,
// node-agent) do not hammer the apiserver in a zero-gap loop; a small think-time
// keeps the workload from degenerating into a synthetic livelock on the shared
// per-key locks and lets background consolidation passes actually complete so
// steady-state REST latency is what gets measured.
var (
	loadWriterSleep = time.Duration(envInt("LOAD_WRITER_SLEEP_MS", 10)) * time.Millisecond
	readerSleep     = time.Duration(envInt("LOAD_READER_SLEEP_MS", 3)) * time.Millisecond
	updaterSleep    = time.Duration(envInt("LOAD_UPDATER_SLEEP_MS", 20)) * time.Millisecond
)

// loadProcessorWorkers returns the worker bound the processor uses in the
// benchmark. Kept a helper so the pre-fix baseline (no Workers field) can be
// adapted with a single edit.
func loadProcessorWorkers() int {
	return max(1, loadPoolSize()/4)
}

func loadDuration() time.Duration {
	return time.Duration(envInt("LOAD_SECONDS", 6)) * time.Second
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

// latencyRec accumulates per-op latencies and error classes lock-free-ish
// (mutex only on append; cheap relative to the storage ops themselves).
type latencyRec struct {
	mu          sync.Mutex
	name        string
	samples     []time.Duration
	errServerTO int64 // fail-fast ServerTimeout (post-fix clean error)
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

func (r *latencyRec) report(t *testing.T) {
	r.mu.Lock()
	s := append([]time.Duration(nil), r.samples...)
	r.mu.Unlock()
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	pct := func(p float64) time.Duration {
		if len(s) == 0 {
			return 0
		}
		idx := int(p / 100 * float64(len(s)-1))
		return s[idx]
	}
	total := len(s)
	t.Logf("== %s ==", r.name)
	t.Logf("  ops=%d ok=%d | errs: serverTimeout=%d takeConn=%d other=%d",
		total, atomic.LoadInt64(&r.okCount), atomic.LoadInt64(&r.errServerTO),
		atomic.LoadInt64(&r.errTakeConn), atomic.LoadInt64(&r.errOther))
	if total == 0 {
		return
	}
	t.Logf("  p50=%s p95=%s p99=%s max=%s", pct(50), pct(95), pct(99), s[total-1])
	t.Logf("  >1s=%d  >5s=%d", atomic.LoadInt64(&r.overOneSec), atomic.LoadInt64(&r.overFiveSec))
}

func isServerTimeoutErr(err error) bool {
	// Post-fix fail-fast lock error. On the pre-fix baseline this is always
	// false (no ServerTimeout), so those failures fall into errOther.
	return apierrors.IsServerTimeout(err)
}

func isTakeConnErr(err error) bool {
	return err != nil && strings.Contains(err.Error(), "take connection")
}

// loadPool builds a temp-dir SQLite pool identical to production NewPool EXCEPT
// it installs a bounded busy timeout on every connection. Production's NewPool
// leaves connections in SetBlockOnBusy mode (infinite block on a held write
// lock); under a deliberately tiny pool that turns ordinary SQLite write
// contention into an unbounded stall that pins every connection to the 60s
// poolContext and drowns the Go-level pool/lock behaviour this benchmark exists
// to measure. A 5s busy timeout keeps SQLite write contention bounded so the
// connection-pool-pinning (PR3) and MapMutex fail-fast (PR1) effects — which
// live ABOVE the SQLite layer — are what the latency numbers reflect. This is a
// harness isolation choice, not a claim about production; see the report note.
func loadPool(t *testing.T, path string, size int) *sqlitemigration.Pool {
	t.Helper()
	return sqlitemigration.NewPool(path,
		sqlitemigration.Schema{
			Migrations: []string{
				`CREATE TABLE IF NOT EXISTS metadata (
					kind TEXT, namespace TEXT, name TEXT, metadata JSON,
					PRIMARY KEY (kind, namespace, name)
				);`,
				`CREATE TABLE IF NOT EXISTS time_series (
					kind TEXT, namespace TEXT, name TEXT, seriesID TEXT,
					reportTimestamp TEXT, status TEXT, tsSuffix TEXT, completion TEXT,
					previousReportTimestamp TEXT, hasData INTEGER DEFAULT 0,
					PRIMARY KEY (kind, namespace, name, seriesID, tsSuffix)
				);`,
			},
		},
		sqlitemigration.Options{
			PoolSize: size,
			PrepareConn: func(conn *sqlite.Conn) error {
				conn.SetBusyTimeout(5 * time.Second)
				return nil
			},
		})
}

// newLoadStorage builds a real StorageImpl + ContainerProfileProcessor over a
// temp-dir SQLite pool of the given size. Returns storage, processor, pool.
func newLoadStorage(t *testing.T, poolSize int) (*StorageImpl, *ContainerProfileProcessor, *sqlitemigration.Pool) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "load.sq3")
	_ = os.Remove(path)
	pool := loadPool(t, path, poolSize)
	require.NotNil(t, pool)

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	processor := &ContainerProfileProcessor{
		DeleteThreshold:         0, // never expire during the run
		MaxContainerProfileSize: 40000,
		Workers:                 loadProcessorWorkers(),
	}
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	// Exercise the real CollapseConfig provider so PreSave's (post-fix cached)
	// settings lookup is on the hot path, matching AC2/AC4 intent.
	processor.CollapseSettings = NewCRDCollapseSettingsProvider(s)
	// Interval 0 => SetStorage does not spawn the maintenance goroutine; we
	// drive ConsolidateTimeSeries explicitly from the load goroutines.
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))
	return s, processor, pool
}

func TestContainerProfileLoad(t *testing.T) {
	if os.Getenv("LOAD_TEST") != "1" {
		t.Skip("set LOAD_TEST=1 to run the ContainerProfile load/stress benchmark")
	}
	s, processor, pool := newLoadStorage(t, loadPoolSize())
	defer func() { _ = pool.Close() }()

	templates := loadTemplates(t)
	dur := loadDuration()

	// Preseed: create the base testdata TS profiles so consolidation and reads
	// have real keys immediately.
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 15*time.Second)
	for _, tpl := range templates {
		p := tpl.profile.DeepCopy()
		key := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + p.Namespace + "/" + p.Name
		_ = s.Create(seedCtx, key, p, nil, 0)
	}
	seedCancel()

	writes := &latencyRec{name: "REST Create (node-agent writers)"}
	reads := &latencyRec{name: "REST Get (CVE/netpol readers)"}
	updates := &latencyRec{name: "REST GuaranteedUpdate"}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var suffixCounter int64

	// Each REST-facing call gets a request context whose deadline models the
	// apiserver's request timeout. Pre-fix, a contended lock blocks up to this
	// deadline; post-fix the 5s lockTimeout fails fast well under it. We keep it
	// generous (30s) so we measure the *actual* wait, not an artificial cap.
	reqCtx := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.Background(), 15*time.Second)
	}

	// node-agent writers: clone a template, give it a fresh ts suffix + series
	// timestamp, Create it. Funnels many TS rows into each workload's base key,
	// mirroring multi-container-per-workload streaming.
	for i := 0; i < loadWriters(); i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tpl := templates[id%len(templates)]
				p := tpl.profile.DeepCopy()
				n := atomic.AddInt64(&suffixCounter, 1)
				suffix := "ld" + strconv.FormatInt(n, 36)
				p.Name = tpl.baseNm + "-" + suffix
				if p.Annotations == nil {
					p.Annotations = map[string]string{}
				}
				p.Annotations[helpersv1.ReportTimestampMetadataKey] = time.Now().Format(time.RFC3339Nano)
				p.ResourceVersion = ""
				key := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + p.Namespace + "/" + p.Name
				ctx, cancel := reqCtx()
				t0 := time.Now()
				err := s.Create(ctx, key, p, nil, 0)
				writes.record(time.Since(t0), err)
				cancel()
				time.Sleep(loadWriterSleep)
			}
		}(i)
	}

	// REST readers: GET the consolidated base key (what CVE-scan/netpol read).
	for i := 0; i < loadReaders(); i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tpl := templates[id%len(templates)]
				ctx, cancel := reqCtx()
				t0 := time.Now()
				out := &softwarecomposition.ContainerProfile{}
				err := s.Get(ctx, tpl.baseKey, storage.GetOptions{IgnoreNotFound: true}, out)
				reads.record(time.Since(t0), err)
				cancel()
				time.Sleep(readerSleep)
			}
		}(i)
	}

	// A slice of readers instead do GuaranteedUpdate on the base key to exercise
	// the write-lock REST path too (a handful, so most traffic stays read-heavy).
	for i := 0; i < loadUpdaters(); i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				tpl := templates[id%len(templates)]
				ctx, cancel := reqCtx()
				t0 := time.Now()
				err := s.GuaranteedUpdate(ctx, tpl.baseKey, &softwarecomposition.ContainerProfile{}, true,
					nil, func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						return input, nil, nil
					}, nil)
				updates.record(time.Since(t0), err)
				cancel()
				time.Sleep(updaterSleep)
			}
		}(i)
	}

	// Consolidator loop: contends for pool connections + per-key locks. An
	// optional inter-pass sleep (LOAD_CONSOLIDATOR_SLEEP_MS) models the periodic
	// nature of the real 30s maintenance loop; the default 0 is the harshest
	// "always consolidating" stress.
	consolidations := &latencyRec{name: "ConsolidateTimeSeries pass"}
	consolSleep := time.Duration(envInt("LOAD_CONSOLIDATOR_SLEEP_MS", 0)) * time.Millisecond
	for i := 0; i < loadConsolidators(); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				t0 := time.Now()
				err := processor.ConsolidateTimeSeries(context.Background())
				consolidations.record(time.Since(t0), err)
				if consolSleep > 0 {
					time.Sleep(consolSleep)
				}
			}
		}()
	}

	time.Sleep(dur)
	close(stop)
	wg.Wait()

	t.Logf("=== ContainerProfile load benchmark: pool=%d writers=%d readers=%d updaters=%d consolidators=%d workers=%d consolSleep=%s dur=%s ===",
		loadPoolSize(), loadWriters(), loadReaders(), loadUpdaters(), loadConsolidators(), loadProcessorWorkers(), consolSleep, dur)
	writes.report(t)
	reads.report(t)
	updates.report(t)
	consolidations.report(t)
}

// TestContainerProfileLockFailFast is the committed regression test for PR1
// (fail-fast lock backstop). It holds a key's write lock (simulating a long
// consolidation critical section) and fires many concurrent REST GETs at that
// key, then asserts every contended GET fails fast rather than hanging to the
// request deadline.
//
// This is the AC4 "request latency stays under a defined bound" assertion: each
// contended GET must return within failFastBound as an apierrors.IsServerTimeout
// (HTTP 500 + Retry-After), NOT block to the (much larger) request-context
// deadline the way the pre-fix code did (which produced the incident's ~60s
// 504 hangs). It is deterministic and runs in ~1s, so unlike TestContainerProfileLoad
// it is NOT gated behind LOAD_TEST — it runs in the normal suite.
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
