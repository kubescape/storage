package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/client-go/kubernetes/scheme"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// ─────────────────────────────── harness ────────────────────────────────
//
// The duress suite executes DURESS.md: real file-backed SQLite, real OS
// filesystem, high-concurrency mixed workloads, fault injection, and the
// invariants asserted after every scenario:
//
//  1. no torn state (GET and LIST agree for every key),
//  2. no wedge (every key fully present or fully absent, and writable),
//  3. contracted error classes only,
//  4. reads never starve during write storms,
//  5. convergence (no orphan staged files, no invisible parts).

type duressEnv struct {
	s         *StorageImpl
	pool      *sqlitemigration.Pool
	processor *ContainerProfileProcessor
	fs        afero.Fs
	dir       string
}

func newDuressEnv(t *testing.T) *duressEnv {
	t.Helper()
	dir := t.TempDir()
	pool := NewTestPool(dir)
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })
	sch := scheme.Scheme
	install.Install(sch)
	fs := afero.NewBasePathFs(afero.NewOsFs(), dir)
	processor := &ContainerProfileProcessor{
		DeleteThreshold:         time.Hour, // duress scenarios exercise the active path
		MaxContainerProfileSize: 400000,
		Workers:                 4,
	}
	s := NewStorageImplWithCollector(fs, "/", pool, nil, sch, processor).(*StorageImpl)
	return &duressEnv{s: s, pool: pool, processor: processor, fs: fs, dir: dir}
}

func cpKey(ns, name string) string {
	return "/spdx.softwarecomposition.kubescape.io/containerprofiles/" + ns + "/" + name
}

func mkCP(ns, name string, gen int) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns,
			Labels: map[string]string{"duress/gen": fmt.Sprintf("g%d", gen)},
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Architectures: []string{"amd64"},
			Execs:         []softwarecomposition.ExecCalls{{Path: fmt.Sprintf("/bin/gen-%d", gen)}},
		},
	}
}

// contractedErr reports whether err is one of the error classes clients are
// allowed to observe under duress (DURESS.md invariant 3).
func contractedErr(err error) bool {
	if err == nil {
		return true
	}
	if storage.IsExist(err) || storage.IsNotFound(err) {
		return true
	}
	if apierrors.IsAlreadyExists(err) || apierrors.IsNotFound(err) || apierrors.IsServerTimeout(err) || apierrors.IsConflict(err) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	// self-induced cancellation surfaced through the acquisition path
	if strings.Contains(err.Error(), "acquisition cancelled") {
		return true
	}
	return false
}

// forbiddenErr matches the raw-SQLite failure classes that must never surface.
func forbiddenErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "SQLITE_BUSY") ||
		strings.Contains(msg, "interrupted")
}

// assertAgreement checks invariant 1+2 for every key: payload (GET) and
// metadata (LIST/metadata-GET) agree on existence, and the key is writable.
func assertAgreement(t *testing.T, e *duressEnv, keys []string) {
	t.Helper()
	ctx := context.Background()
	for _, key := range keys {
		var payload softwarecomposition.ContainerProfile
		perr := e.s.Get(ctx, key, storage.GetOptions{}, &payload)
		payloadPresent := perr == nil
		if perr != nil {
			require.True(t, storage.IsNotFound(perr) || apierrors.IsNotFound(perr), "GET %s: unexpected error %v", key, perr)
		}
		var meta softwarecomposition.ContainerProfile
		merr := e.s.Get(ctx, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &meta)
		metaPresent := merr == nil
		if merr != nil {
			require.True(t, storage.IsNotFound(merr) || apierrors.IsNotFound(merr), "meta-GET %s: unexpected error %v", key, merr)
		}
		require.Equal(t, payloadPresent, metaPresent,
			"torn state on %s: payload present=%v metadata present=%v", key, payloadPresent, metaPresent)
		if payloadPresent {
			require.Equal(t, payload.ResourceVersion, meta.ResourceVersion,
				"GET and metadata disagree on RV for %s", key)
		}
		// invariant 2: the key remains writable
		probe := mkCP(strings.Split(key, "/")[3], strings.Split(key, "/")[4], 999999)
		uerr := e.s.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, true, nil,
			func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
				out := probe.DeepCopy()
				out.ResourceVersion = input.(*softwarecomposition.ContainerProfile).ResourceVersion
				out.UID = input.(*softwarecomposition.ContainerProfile).UID
				return out, nil, nil
			}, nil)
		require.NoError(t, uerr, "key %s is wedged: post-storm write failed", key)
	}
}

// ─────────────────────────── row 2: interrupt lifetime ───────────────────────────

// TestDuress_InterruptLifetime pins DURESS.md row 2: the pool-acquisition
// deadline must not double as the connection's execution lifetime. An update
// whose critical section outlives the acquisition budget must complete —
// before the fix, the connection's interrupt (bound to the 5s acquisition
// context) killed its own statement with "sqlite: step: interrupted".
func TestDuress_InterruptLifetime(t *testing.T) {
	e := newDuressEnv(t)
	prev := poolTimeout
	poolTimeout = 200 * time.Millisecond
	defer func() { poolTimeout = prev }()

	key := cpKey("ns-il", "victim")
	require.NoError(t, e.s.Create(context.Background(), key, mkCP("ns-il", "victim", 0), &softwarecomposition.ContainerProfile{}, 0))

	err := e.s.GuaranteedUpdate(context.Background(), key, &softwarecomposition.ContainerProfile{}, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			time.Sleep(3 * poolTimeout) // outlive the acquisition budget mid-operation
			out := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			out.Labels["duress/gen"] = "after-sleep"
			return out, nil, nil
		}, nil)
	require.NoError(t, err, "an operation outliving the acquisition budget must not be interrupted by it")
	assertAgreement(t, e, []string{key})
}

// ─────────────────────────── row 8: part atomicity ───────────────────────────

type failingPreCommitProcessor struct {
	DefaultProcessor
	fail atomic.Bool
}

func (f *failingPreCommitProcessor) PreCommitSQL(_ context.Context, _ *sqlite.Conn, _ runtime.Object) error {
	if f.fail.Load() {
		return errors.New("injected pre-commit failure")
	}
	return nil
}

// TestDuress_PartAtomicity pins DURESS.md row 8: auxiliary SQL that belongs to
// an object (a part's time-series row) commits inside the object's savepoint.
// If it fails, NOTHING of the object survives — before the fix the object
// committed first, the auxiliary write failed after, and the client's retry
// died on KeyExists while the object stayed invisible to consolidation.
func TestDuress_PartAtomicity(t *testing.T) {
	dir := t.TempDir()
	pool := NewTestPool(dir)
	require.NotNil(t, pool)
	defer func() { _ = pool.Close() }()
	sch := scheme.Scheme
	install.Install(sch)
	proc := &failingPreCommitProcessor{}
	s := NewStorageImplWithCollector(afero.NewBasePathFs(afero.NewOsFs(), dir), "/", pool, nil, sch, proc).(*StorageImpl)

	proc.fail.Store(true)
	key := cpKey("ns-pa", "part-0001")
	err := s.Create(context.Background(), key, mkCP("ns-pa", "part-0001", 0), &softwarecomposition.ContainerProfile{}, 0)
	require.Error(t, err, "create must fail when the object's auxiliary SQL fails")

	// nothing survived: payload absent, metadata absent
	var out softwarecomposition.ContainerProfile
	gerr := s.Get(context.Background(), key, storage.GetOptions{}, &out)
	require.True(t, gerr != nil && (storage.IsNotFound(gerr) || apierrors.IsNotFound(gerr)),
		"a failed pre-commit must leave no payload behind (got err=%v)", gerr)
	merr := s.Get(context.Background(), key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &out)
	require.Error(t, merr, "a failed pre-commit must leave no metadata behind")

	// and the retry heals instead of dying on KeyExists
	proc.fail.Store(false)
	require.NoError(t, s.Create(context.Background(), key, mkCP("ns-pa", "part-0001", 1), &softwarecomposition.ContainerProfile{}, 0),
		"the client's retry must succeed after the transient failure")
}

// ─────────────────────────── row 9: atomic delete ───────────────────────────

type failingRemoveFs struct {
	afero.Fs
	failPath atomic.Value // string
}

func (f *failingRemoveFs) Remove(name string) error {
	if p, _ := f.failPath.Load().(string); p != "" && strings.HasSuffix(name, p) {
		return errors.New("injected remove failure")
	}
	return f.Fs.Remove(name)
}

// TestDuress_AtomicDelete pins DURESS.md row 9: delete commits metadata
// removal, time-series removal and payload removal as one unit. A payload
// removal failure rolls the SQL back — the object stays FULLY visible instead
// of turning into a LIST ghost (metadata gone pre-fix) or a GET orphan.
func TestDuress_AtomicDelete(t *testing.T) {
	dir := t.TempDir()
	pool := NewTestPool(dir)
	require.NotNil(t, pool)
	defer func() { _ = pool.Close() }()
	sch := scheme.Scheme
	install.Install(sch)
	ffs := &failingRemoveFs{Fs: afero.NewBasePathFs(afero.NewOsFs(), dir)}
	s := NewStorageImplWithCollector(ffs, "/", pool, nil, sch, DefaultProcessor{}).(*StorageImpl)

	key := cpKey("ns-ad", "doomed")
	require.NoError(t, s.Create(context.Background(), key, mkCP("ns-ad", "doomed", 0), &softwarecomposition.ContainerProfile{}, 0))

	ffs.failPath.Store("doomed" + GobExt)
	derr := s.Delete(context.Background(), key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
	require.Error(t, derr, "delete must fail when the payload cannot be removed")

	// the object is fully intact on BOTH sides — no ghost, no orphan
	var payload, meta softwarecomposition.ContainerProfile
	require.NoError(t, s.Get(context.Background(), key, storage.GetOptions{}, &payload), "payload must survive the failed delete")
	require.NoError(t, s.Get(context.Background(), key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &meta),
		"metadata must be ROLLED BACK on a failed delete — no LIST ghost")

	// heal and delete for real
	ffs.failPath.Store("")
	require.NoError(t, s.Delete(context.Background(), key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{}))
	gerr := s.Get(context.Background(), key, storage.GetOptions{}, &payload)
	require.Error(t, gerr)
	merr := s.Get(context.Background(), key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &meta)
	require.Error(t, merr, "both sides gone after a successful delete")
}

// ─────────────────────────── row 10: crash artifacts ───────────────────────────

// TestDuress_OrphanStagedFiles pins DURESS.md row 10: staged payload files
// left by a crash between stage and commit are swept at construction and are
// never served.
func TestDuress_OrphanStagedFiles(t *testing.T) {
	dir := t.TempDir()
	fs := afero.NewBasePathFs(afero.NewOsFs(), dir)
	orphan := "/spdx.softwarecomposition.kubescape.io/containerprofiles/ns-cr/ghost" + GobExt + ".t"
	require.NoError(t, fs.MkdirAll("/spdx.softwarecomposition.kubescape.io/containerprofiles/ns-cr", 0755))
	require.NoError(t, afero.WriteFile(fs, orphan, []byte("partial junk from a crash"), 0644))

	pool := NewTestPool(dir)
	require.NotNil(t, pool)
	defer func() { _ = pool.Close() }()
	sch := scheme.Scheme
	install.Install(sch)
	s := NewStorageImplWithCollector(fs, "/", pool, nil, sch, DefaultProcessor{}).(*StorageImpl)

	exists, _ := afero.Exists(fs, orphan)
	require.False(t, exists, "orphan staged payload must be swept at construction")

	// the key is fully absent and freshly writable
	key := cpKey("ns-cr", "ghost")
	var out softwarecomposition.ContainerProfile
	require.Error(t, s.Get(context.Background(), key, storage.GetOptions{}, &out))
	require.NoError(t, s.Create(context.Background(), key, mkCP("ns-cr", "ghost", 1), &softwarecomposition.ContainerProfile{}, 0))
}

// ─────────────────────────── row 6: stage failure ───────────────────────────

type failingOpenFs struct {
	afero.Fs
	failSuffix atomic.Value // string
}

func (f *failingOpenFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	if sfx, _ := f.failSuffix.Load().(string); sfx != "" && strings.HasSuffix(name, sfx) {
		return nil, errors.New("injected stage failure")
	}
	return f.Fs.OpenFile(name, flag, perm)
}

// TestDuress_FaultInjection pins DURESS.md row 6: a payload staging failure
// fails clean before any SQL is touched, and the retry succeeds.
func TestDuress_FaultInjection(t *testing.T) {
	dir := t.TempDir()
	pool := NewTestPool(dir)
	require.NotNil(t, pool)
	defer func() { _ = pool.Close() }()
	sch := scheme.Scheme
	install.Install(sch)
	ffs := &failingOpenFs{Fs: afero.NewBasePathFs(afero.NewOsFs(), dir)}
	s := NewStorageImplWithCollector(ffs, "/", pool, nil, sch, DefaultProcessor{}).(*StorageImpl)

	ffs.failSuffix.Store(GobExt + ".t")
	key := cpKey("ns-fi", "victim")
	require.Error(t, s.Create(context.Background(), key, mkCP("ns-fi", "victim", 0), &softwarecomposition.ContainerProfile{}, 0))
	var out softwarecomposition.ContainerProfile
	require.Error(t, s.Get(context.Background(), key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &out),
		"no metadata may exist after a failed stage")
	ffs.failSuffix.Store("")
	require.NoError(t, s.Create(context.Background(), key, mkCP("ns-fi", "victim", 1), &softwarecomposition.ContainerProfile{}, 0))
	assertAgreement(t, &duressEnv{s: s}, []string{key})
}

// ─────────────────────────── rows 2/3: cancellation storm ───────────────────────────

// TestDuress_CancellationStorm pins DURESS.md rows 2+3 under adversarial
// client behavior: writers whose contexts expire at random points, including
// mid-write. Afterwards nothing is torn, nothing is wedged, and only
// contracted errors were observed.
func TestDuress_CancellationStorm(t *testing.T) {
	e := newDuressEnv(t)
	const keys = 12
	allKeys := make([]string, keys)
	for i := range allKeys {
		allKeys[i] = cpKey("ns-cs", fmt.Sprintf("storm-%02d", i))
	}
	var wg sync.WaitGroup
	var uncontracted atomic.Int64
	var forbidden atomic.Int64
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 60; i++ {
				key := allKeys[rnd.Intn(keys)]
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(1+rnd.Intn(12))*time.Millisecond)
				var err error
				if rnd.Intn(2) == 0 {
					err = e.s.Create(ctx, key, mkCP("ns-cs", key[strings.LastIndex(key, "/")+1:], i), &softwarecomposition.ContainerProfile{}, 0)
				} else {
					err = e.s.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, true, nil,
						func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
							out := input.(*softwarecomposition.ContainerProfile).DeepCopy()
							if out.Labels == nil {
								out.Labels = map[string]string{}
							}
							out.Labels["duress/gen"] = fmt.Sprintf("w%d-i%d", w, i)
							out.Name = key[strings.LastIndex(key, "/")+1:]
							out.Namespace = "ns-cs"
							return out, nil, nil
						}, nil)
				}
				cancel()
				if forbiddenErr(err) {
					forbidden.Add(1)
					t.Logf("FORBIDDEN error class surfaced: %v", err)
				} else if !contractedErr(err) {
					uncontracted.Add(1)
					t.Logf("uncontracted error: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	require.Zero(t, forbidden.Load(), "raw SQLite failure classes surfaced during the cancellation storm")
	require.Zero(t, uncontracted.Load(), "uncontracted error classes surfaced during the cancellation storm")
	assertAgreement(t, e, allKeys)
}

// ─────────────────────────── row 11: same-key hammer ───────────────────────────

// TestDuress_SameKeyHammer pins DURESS.md row 11: heavy same-key contention
// resolves via the per-key lock; the winner is exactly one contender and both
// stores agree on it.
func TestDuress_SameKeyHammer(t *testing.T) {
	e := newDuressEnv(t)
	key := cpKey("ns-sk", "hot")
	var wg sync.WaitGroup
	var forbidden atomic.Int64
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				var err error
				switch (w + i) % 3 {
				case 0:
					err = e.s.Create(context.Background(), key, mkCP("ns-sk", "hot", w*1000+i), &softwarecomposition.ContainerProfile{}, 0)
				case 1:
					err = e.s.GuaranteedUpdate(context.Background(), key, &softwarecomposition.ContainerProfile{}, true, nil,
						func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
							out := input.(*softwarecomposition.ContainerProfile).DeepCopy()
							if out.Labels == nil {
								out.Labels = map[string]string{}
							}
							out.Labels["duress/gen"] = fmt.Sprintf("h%d-%d", w, i)
							out.Name, out.Namespace = "hot", "ns-sk"
							return out, nil, nil
						}, nil)
				default:
					err = e.s.Delete(context.Background(), key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
				}
				if forbiddenErr(err) {
					forbidden.Add(1)
					t.Logf("FORBIDDEN: %v", err)
				} else if !contractedErr(err) {
					forbidden.Add(1)
					t.Logf("uncontracted: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	require.Zero(t, forbidden.Load())
	assertAgreement(t, e, []string{key})
}

// ─────────────────────────── rows 1/3/12/13/14/15: mixed workload ───────────────────────────

// duressPartTemplate builds chained time-series part profiles from the
// committed testdata template so consolidation runs against realistic data.
// duressParts builds n time-series part profiles. It returns errors instead
// of failing the test so it is safe to call from worker goroutines —
// (*testing.T).FailNow (which require uses) must only run on the goroutine
// executing the test function.
func duressParts(ns string, seriesID string, n int) ([]*softwarecomposition.ContainerProfile, error) {
	raw, err := os.ReadFile("testdata/p1.json")
	if err != nil {
		return nil, err
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	parts := make([]*softwarecomposition.ContainerProfile, 0, n)
	for i := 0; i < n; i++ {
		var p softwarecomposition.ContainerProfile
		if err := json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}
		p.Namespace = ns
		p.UID = ""
		p.ResourceVersion = ""
		nameBase, _ := SplitProfileName(p.Name)
		p.Name = fmt.Sprintf("%s-%032x", nameBase, i+1)
		p.Annotations["kubescape.io/report-series-id"] = seriesID
		rt := base.Add(time.Duration(i) * 10 * time.Second)
		p.Annotations["kubescape.io/report-timestamp"] = rt.String()
		if i == 0 {
			p.Annotations["kubescape.io/previous-report-timestamp"] = "0001-01-01 00:00:00 +0000 UTC"
		} else {
			p.Annotations["kubescape.io/previous-report-timestamp"] = base.Add(time.Duration(i-1) * 10 * time.Second).String()
		}
		if i == n-1 {
			p.Annotations["kubescape.io/status"] = "completed"
			p.Annotations["kubescape.io/completion"] = "complete"
		} else {
			p.Annotations["kubescape.io/status"] = "ready"
			p.Annotations["kubescape.io/completion"] = "partial"
		}
		parts = append(parts, &p)
	}
	return parts, nil
}

// TestDuress_MixedWorkload is the main storm: parallel creates/updates/deletes
// across many keys, a hot key, continuous readers, part-profile ingestion and
// a concurrently running consolidation loop. Pins rows 1, 3, 12, 13, 14, 15:
// no BUSY, no interrupts, reads never starve, everything converges.
func TestDuress_MixedWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("8s storm — skipped under -short")
	}
	e := newDuressEnv(t)
	stop := make(chan struct{})
	var forbidden, uncontracted, readerErrs atomic.Int64
	var reads, writes atomic.Int64
	note := func(err error) {
		if forbiddenErr(err) {
			forbidden.Add(1)
			t.Logf("FORBIDDEN: %v", err)
		} else if !contractedErr(err) {
			uncontracted.Add(1)
			t.Logf("uncontracted: %v", err)
		}
	}

	const nKeys = 32
	allKeys := make([]string, nKeys)
	for i := range allKeys {
		allKeys[i] = cpKey("ns-mw", fmt.Sprintf("obj-%02d", i))
	}

	var wg sync.WaitGroup
	// writers across keys
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(100 + w)))
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				key := allKeys[rnd.Intn(nKeys)]
				name := key[strings.LastIndex(key, "/")+1:]
				var err error
				switch rnd.Intn(4) {
				case 0:
					err = e.s.Create(context.Background(), key, mkCP("ns-mw", name, i), &softwarecomposition.ContainerProfile{}, 0)
				case 1, 2:
					err = e.s.GuaranteedUpdate(context.Background(), key, &softwarecomposition.ContainerProfile{}, true, nil,
						func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
							out := input.(*softwarecomposition.ContainerProfile).DeepCopy()
							if out.Labels == nil {
								out.Labels = map[string]string{}
							}
							out.Labels["duress/gen"] = fmt.Sprintf("w%d-%d", w, i)
							out.Name, out.Namespace = name, "ns-mw"
							return out, nil, nil
						}, nil)
				default:
					err = e.s.Delete(context.Background(), key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
				}
				writes.Add(1)
				note(err)
			}
		}(w)
	}
	// hot-key hammer
	hot := cpKey("ns-mw", "hot")
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				err := e.s.GuaranteedUpdate(context.Background(), hot, &softwarecomposition.ContainerProfile{}, true, nil,
					func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						out := input.(*softwarecomposition.ContainerProfile).DeepCopy()
						if out.Labels == nil {
							out.Labels = map[string]string{}
						}
						out.Labels["duress/gen"] = fmt.Sprintf("hot%d-%d", w, i)
						out.Name, out.Namespace = "hot", "ns-mw"
						return out, nil, nil
					}, nil)
				writes.Add(1)
				note(err)
			}
		}(w)
	}
	// readers: must never starve (invariant 4). Reads run against keys that
	// may legitimately not exist; only non-NotFound errors count.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(int64(200 + r)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := allKeys[rnd.Intn(nKeys)]
				var out softwarecomposition.ContainerProfile
				err := e.s.Get(context.Background(), key, storage.GetOptions{IgnoreNotFound: true}, &out)
				if err != nil {
					readerErrs.Add(1)
					t.Logf("reader GET error: %v", err)
				}
				list := &softwarecomposition.ContainerProfileList{}
				err = e.s.GetList(context.Background(), "/spdx.softwarecomposition.kubescape.io/containerprofiles/ns-mw", storage.ListOptions{Recursive: true, Predicate: storage.Everything}, list)
				if err != nil {
					readerErrs.Add(1)
					t.Logf("reader LIST error: %v", err)
				}
				reads.Add(2)
			}
		}(r)
	}
	// part ingestion + consolidation loop (rows 13/14)
	wg.Add(1)
	go func() {
		defer wg.Done()
		series := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			series++
			parts, perr := duressParts("ns-mw", fmt.Sprintf("series-%04d", series), 4)
			if perr != nil {
				note(perr)
				continue
			}
			for _, p := range parts {
				key := cpKey("ns-mw", p.Name)
				if err := e.s.Create(context.Background(), key, p, &softwarecomposition.ContainerProfile{}, 0); err != nil {
					note(err)
				}
			}
			if err := e.processor.ConsolidateTimeSeries(context.Background()); err != nil {
				note(err)
			}
		}
	}()

	time.Sleep(8 * time.Second)
	close(stop)
	wg.Wait()

	t.Logf("mixed workload: %d writes, %d reads", writes.Load(), reads.Load())
	require.Zero(t, forbidden.Load(), "raw SQLite failure classes surfaced under duress")
	require.Zero(t, uncontracted.Load(), "uncontracted errors surfaced under duress")
	require.Zero(t, readerErrs.Load(), "reads starved or failed during the write storm (invariant 4)")
	require.Greater(t, writes.Load(), int64(100), "storm did not actually run")
	require.Greater(t, reads.Load(), int64(100), "readers did not actually run")

	// convergence: one more consolidation pass, then invariants over all keys
	require.NoError(t, e.processor.ConsolidateTimeSeries(context.Background()))
	assertAgreement(t, e, append(append([]string{}, allKeys...), hot))

	// no staged files left anywhere (invariant 5)
	staged := 0
	_ = afero.Walk(e.fs, "/", func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() && strings.HasSuffix(path, GobExt+".t") {
			staged++
		}
		return nil
	})
	require.Zero(t, staged, "staged payload files remained after the storm")
}

// ─────────────────────────── row 14: consolidation vs API ───────────────────────────

// TestDuress_ConsolidationVsAPI pins DURESS.md rows 4+14: consolidation of a
// key races API writers on the SAME consolidated key and deletes of its part
// keys, under the fixed lock ordering (per-key lock → gate). The test
// completing at all pins the absence of the ordering deadlock; the assertions
// pin convergence.
func TestDuress_ConsolidationVsAPI(t *testing.T) {
	e := newDuressEnv(t)
	seriesID := "series-cvapi"
	parts, perr := duressParts("ns-cv", seriesID, 6)
	require.NoError(t, perr)
	consolidatedName, _ := SplitProfileName(parts[0].Name)
	consolidatedKey := cpKey("ns-cv", consolidatedName)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var forbidden atomic.Int64
	// API writer hammering the consolidated key
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			err := e.s.GuaranteedUpdate(context.Background(), consolidatedKey, &softwarecomposition.ContainerProfile{}, true, nil,
				func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
					out := input.(*softwarecomposition.ContainerProfile).DeepCopy()
					if out.Annotations == nil {
						out.Annotations = map[string]string{}
					}
					out.Annotations["duress/api-writer"] = fmt.Sprintf("%d", i)
					out.Name, out.Namespace = consolidatedName, "ns-cv"
					return out, nil, nil
				}, nil)
			if forbiddenErr(err) {
				forbidden.Add(1)
				t.Logf("FORBIDDEN: %v", err)
			}
		}
	}()
	// ingest parts + consolidate concurrently
	for _, p := range parts {
		key := cpKey("ns-cv", p.Name)
		require.NoError(t, e.s.Create(context.Background(), key, p, &softwarecomposition.ContainerProfile{}, 0))
		require.NoError(t, e.processor.ConsolidateTimeSeries(context.Background()))
	}
	require.NoError(t, e.processor.ConsolidateTimeSeries(context.Background()))
	close(stop)
	wg.Wait()
	require.Zero(t, forbidden.Load())

	// the consolidated profile exists and both stores agree
	var got softwarecomposition.ContainerProfile
	require.NoError(t, e.s.Get(context.Background(), consolidatedKey, storage.GetOptions{}, &got),
		"consolidated profile must exist after the race")
	assertAgreement(t, e, []string{consolidatedKey})
}

// silence unused-import lint in case of build-tag shuffles
