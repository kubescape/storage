package dynamicpathdetectortests

// Profiling helpers for the path analyzer hot path.
//
// Invocation:
//
//   go test ./pkg/registry/file/dynamicpathdetector/tests/ \
//     -run=TestProfileAnalyzePath \
//     -profile-out=/tmp/analyzer-profile \
//     -profile-iters=200000
//
// Writes cpu.out, mem.out (heap alloc_space sampled) and goroutine.out
// to the directory, then prints the top allocators to stdout so the
// test log alone is enough to see regressions. Skipped unless
// -profile-out is set, so it stays cheap on a regular `go test ./...`.
//
// Helper benchmark BenchmarkAnalyzePathWarm exercises the steady state
// where the trie is already populated — the interesting mode for
// zero-alloc analysis, because first-insert naturally allocates.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

var profileOutDir = flag.String("profile-out", "",
	"directory to write analyzer profiles into (e.g. /tmp/analyzer-profile); if empty the profile test is skipped")
var profileIters = flag.Int("profile-iters", 100000,
	"number of AnalyzePath calls to execute during the profile run")

// TestProfileAnalyzePath runs a large in-process workload against AnalyzePath
// and writes CPU + heap profiles. Intended for interactive iteration during
// the zero-alloc rewrite, not for CI (requires -profile-out to run).
func TestProfileAnalyzePath(t *testing.T) {
	if *profileOutDir == "" {
		t.Skip("set -profile-out=<dir> to enable the profile test")
	}
	if err := os.MkdirAll(*profileOutDir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}

	// Generate a representative mixed workload once, outside the measured
	// section, so its allocations don't show up in the profile.
	paths := generateMixedPaths(10000, 0)
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)
	identifier := "profile"

	// Warm up the analyzer so the trie is populated. Steady-state calls
	// are the interesting regime for zero-alloc (cold inserts always
	// allocate a new node).
	for i := 0; i < len(paths); i++ {
		if _, err := analyzer.AnalyzePath(paths[i], identifier); err != nil {
			t.Fatalf("warmup AnalyzePath: %v", err)
		}
	}

	// CPU profile.
	cpuPath := filepath.Join(*profileOutDir, "cpu.out")
	cpuFile, err := os.Create(cpuPath)
	if err != nil {
		t.Fatalf("create cpu profile: %v", err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		t.Fatalf("start cpu profile: %v", err)
	}

	// Force a clean GC baseline so MemStats numbers reflect only the
	// measured section.
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < *profileIters; i++ {
		if _, err := analyzer.AnalyzePath(paths[i%len(paths)], identifier); err != nil {
			pprof.StopCPUProfile()
			cpuFile.Close()
			t.Fatalf("AnalyzePath iter %d: %v", i, err)
		}
	}

	// Read memstats immediately after the measured loop, BEFORE stopping
	// the CPU profile and closing the output file. Both of those do
	// non-trivial internal allocations (buffer flush, file finalization)
	// that would otherwise land in `after.TotalAlloc` / `after.Mallocs`
	// and inflate the reported per-call numbers — material noise for a
	// zero-alloc target.
	runtime.ReadMemStats(&after)

	pprof.StopCPUProfile()
	cpuFile.Close()

	// Heap profile (alloc_space).
	memPath := filepath.Join(*profileOutDir, "mem.out")
	memFile, err := os.Create(memPath)
	if err != nil {
		t.Fatalf("create mem profile: %v", err)
	}
	if err := pprof.Lookup("allocs").WriteTo(memFile, 0); err != nil {
		t.Fatalf("write mem profile: %v", err)
	}
	memFile.Close()

	// Goroutine snapshot (useful when debugging leaks).
	goPath := filepath.Join(*profileOutDir, "goroutine.out")
	goFile, err := os.Create(goPath)
	if err != nil {
		t.Fatalf("create goroutine profile: %v", err)
	}
	if err := pprof.Lookup("goroutine").WriteTo(goFile, 0); err != nil {
		t.Fatalf("write goroutine profile: %v", err)
	}
	goFile.Close()

	totalBytes := after.TotalAlloc - before.TotalAlloc
	totalMallocs := after.Mallocs - before.Mallocs
	t.Logf("AnalyzePath: %d iterations", *profileIters)
	t.Logf("  bytes allocated: %d total, %.2f B/call", totalBytes, float64(totalBytes)/float64(*profileIters))
	t.Logf("  heap objects   : %d total, %.2f allocs/call", totalMallocs, float64(totalMallocs)/float64(*profileIters))
	t.Logf("  wrote profiles to %s", *profileOutDir)
	t.Logf("  inspect with: go tool pprof -top -alloc_space %s", memPath)
	t.Logf("               go tool pprof -top %s", cpuPath)
}

// BenchmarkAnalyzePathWarm is a companion to BenchmarkAnalyzePath that
// pre-populates the analyzer's trie, so every iteration exercises the
// steady-state walk instead of first-insert. Steady-state is the regime
// we care about for zero-alloc — cold inserts will always allocate a
// new SegmentNode.
func BenchmarkAnalyzePathWarm(b *testing.B) {
	paths := generateMixedPaths(10000, 0)
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)
	identifier := "warm"
	for _, p := range paths {
		if _, err := analyzer.AnalyzePath(p, identifier); err != nil {
			b.Fatalf("warmup: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := analyzer.AnalyzePath(paths[i%len(paths)], identifier); err != nil {
			b.Fatalf("AnalyzePath: %v", err)
		}
	}
}

// BenchmarkAnalyzePathCold is the counterpart: each iteration uses a
// fresh analyzer, so it measures the cost including node allocation.
// The two benchmarks together bracket the true cost envelope.
func BenchmarkAnalyzePathCold(b *testing.B) {
	paths := generateMixedPaths(10000, 0)
	identifier := "cold"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		analyzer := dynamicpathdetector.NewPathAnalyzer(100)
		if _, err := analyzer.AnalyzePath(paths[i%len(paths)], identifier); err != nil {
			b.Fatalf("AnalyzePath: %v", err)
		}
	}
}

// Ensure fmt is kept imported when future debugging prints land here.
var _ = fmt.Sprint
