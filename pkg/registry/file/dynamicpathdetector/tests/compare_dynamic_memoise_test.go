package dynamicpathdetectortests

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Memoisation contract for `*` backtracking in the segment matcher: the
// recursive expansion explores all suffix splits, and without caching that
// becomes exponential for multi-wildcard patterns on the hot runtime path.
//
// This file pins the contract end-to-end:
//
//   1. Behavioural parity: every existing accept/reject decision the
//      pre-memoisation matcher made is preserved exactly. The cases
//      below cover the same shapes the in-tree coverage_test.go file
//      already validates (anchored vs trailing `*`, mid-path `*`,
//      ellipsis `⋯`, multi-`*` patterns, edge inputs), plus a set of
//      adversarial ReDoS-style cases that previously could blow the
//      stack or burn CPU.
//
//   2. Performance ceiling: the adversarial inputs MUST complete inside
//      a budget that an un-memoised exponential matcher cannot meet on
//      any reasonable CI runner. The budget is generous so the test is
//      not timing-flaky on slow runners — un-memoised compareSegments
//      times out by ~3 orders of magnitude on these inputs.
//
// Skipped under `testing.Short()` per the CI-flakiness mitigation
// pattern (compare_exec_args_test.go:313).

// adversarialMultiWildcardInputs builds dynamic patterns shaped to
// trigger the worst-case backtracking in the un-memoised matcher: a
// run of `*` segments followed by a literal tail that does NOT match
// the regular path's tail. The pre-memoisation matcher explores every
// possible split point for each `*` against every suffix offset of the
// regular path — 2^n / n! style explosion.
func adversarialMultiWildcardInputs(starCount, regularDepth int) (dynamic, regular string) {
	stars := make([]string, starCount)
	for i := range stars {
		stars[i] = "*"
	}
	dynamic = "/" + strings.Join(stars, "/") + "/literal_tail_that_will_not_match"

	segs := make([]string, regularDepth)
	for i := range segs {
		segs[i] = fmt.Sprintf("seg%d", i)
	}
	regular = "/" + strings.Join(segs, "/") + "/different_tail"
	return
}

// TestCompareDynamic_MemoiseGoldenAcceptance is the parity gate. Every
// pair below MUST match the pre-memoisation answer. If any entry flips
// after applying the memoisation fix, the fix has changed semantics
// and the commit must be reverted.
//
// Cases enumerated:
//   - empty inputs (false on either side)
//   - exact literal match / mismatch
//   - leading `*` consuming various counts of regular segments
//   - trailing `*` (one-or-more) vs the bare parent directory (no match)
//   - trailing `*` vs deeper paths (match)
//   - mid-path `*` consuming zero / many segments
//   - dynamic identifier `⋯` matching exactly one segment
//   - consecutive `*/*` (must match, see comment in compareSegments)
//   - `*` followed by literal segments that match / don't match
//   - DNS-style nested paths (busybox/systemd-style symlink fan-outs)
func TestCompareDynamic_MemoiseGoldenAcceptance(t *testing.T) {
	cases := []struct {
		name    string
		dynamic string
		regular string
		want    bool
	}{
		// --- empty inputs ---
		{"both_empty_no_match", "", "", false},
		{"empty_dynamic_no_match", "", "/etc/passwd", false},
		{"empty_regular_no_match", "/etc/passwd", "", false},

		// --- literal exact ---
		{"literal_exact", "/etc/passwd", "/etc/passwd", true},
		{"literal_different_tail", "/etc/passwd", "/etc/shadow", false},
		{"literal_different_depth", "/etc/passwd", "/etc/passwd/extra", false},

		// --- trailing `*` ---
		{"trailing_star_matches_child", "/etc/*", "/etc/passwd", true},
		{"trailing_star_matches_deeper", "/etc/*", "/etc/ssh/sshd_config", true},
		{"trailing_star_no_match_on_parent", "/etc/*", "/etc", false},
		{"trailing_star_no_match_on_root", "/*", "/", false},

		// --- mid-path `*` (zero-or-more) ---
		{"mid_star_zero_consumed", "/a/*/b", "/a/b", true},
		{"mid_star_one_consumed", "/a/*/b", "/a/x/b", true},
		{"mid_star_many_consumed", "/a/*/b", "/a/x/y/z/b", true},
		{"mid_star_no_match_at_tail", "/a/*/b", "/a/x/c", false},

		// --- dynamic identifier `⋯` (one segment) ---
		{"ellipsis_short", "/var/log/⋯/access.log", "/var/log/nginx/access.log", true},
		{"ellipsis_must_consume_one_segment", "/var/log/⋯/access.log", "/var/log/access.log", false},
		{"ellipsis_no_match_different_tail", "/var/log/⋯/access.log", "/var/log/nginx/error.log", false},

		// --- consecutive `*/*` (per analyzer comment about non-collapse).
		// Each `*` is zero-or-more (except the trailing `*` which is
		// one-or-more), so /*/* effectively reduces to /* — it matches
		// any non-empty path because the first `*` consumes zero and the
		// trailing `*` consumes one or more.
		{"consecutive_star_star_matches_two", "/*/*", "/a/b", true},
		{"consecutive_star_star_matches_deeper", "/*/*", "/a/b/c", true},
		{"consecutive_star_star_matches_one", "/*/*", "/a", true},

		// --- `*` then literal segments ---
		{"star_then_literal_match", "/a/*/etc/passwd", "/a/x/etc/passwd", true},
		{"star_then_literal_match_long", "/a/*/etc/passwd", "/a/x/y/z/etc/passwd", true},
		{"star_then_literal_no_match", "/a/*/etc/passwd", "/a/x/etc/shadow", false},

		// --- busybox-style symlink shape (real-world hipster_shop) ---
		{"busybox_match", "/bin/busybox", "/bin/busybox", true},
		{"busybox_via_star", "/bin/*", "/bin/busybox", true},
		{"busybox_via_star_to_grpc", "/bin/*", "/bin/grpc_health_probe", true},

		// --- multi-wildcard (each `*` is zero-or-more, so /*/*/* matches
		// any depth from 1 to N before the literal tail).
		{"multi_star_match", "/*/*/*/etc/passwd", "/a/b/c/etc/passwd", true},
		{"multi_star_match_with_extra", "/*/*/*/etc/passwd", "/a/b/c/d/etc/passwd", true},
		{"multi_star_match_shorter", "/*/*/*/etc/passwd", "/a/b/etc/passwd", true},
		{"multi_star_no_match_wrong_tail", "/*/*/*/etc/passwd", "/a/b/c/etc/shadow", false},

		// --- root path edge ---
		{"unanchored_star_matches_root", "*", "/", true},
		{"unanchored_star_matches_anything", "*", "/var/log/nginx", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dynamicpathdetector.CompareDynamic(tc.dynamic, tc.regular)
			assert.Equalf(t, tc.want, got,
				"CompareDynamic(%q, %q) want=%v got=%v", tc.dynamic, tc.regular, tc.want, got)
		})
	}
}

// TestCompareDynamic_MemoiseAdversarialReDoS pins the wall-clock upper
// bound. The dynamic pattern has many `*` segments and a literal
// tail; the regular path has many segments and a non-matching tail.
// Un-memoised compareSegments explores every (di, ri) state pair via
// re-entry, which is 2^n / n! style runtime. With memoisation, every
// (di, ri) pair is visited at most once → O(d * r) time.
//
// Budget: 200ms wall-clock for the largest input. An un-memoised matcher
// takes seconds-to-tens-of-seconds on these inputs. Even on a heavily
// loaded CI runner, the memoised matcher completes well under the
// budget. Skipped under testing.Short() so quick local runs aren't
// burdened.
func TestCompareDynamic_MemoiseAdversarialReDoS(t *testing.T) {
	if testing.Short() {
		t.Skip("skip timing-sensitive ReDoS regression in short mode")
	}

	cases := []struct {
		stars        int
		regularDepth int
		budget       time.Duration
	}{
		{stars: 12, regularDepth: 12, budget: 50 * time.Millisecond},
		{stars: 16, regularDepth: 16, budget: 100 * time.Millisecond},
		{stars: 20, regularDepth: 20, budget: 200 * time.Millisecond},
		{stars: 24, regularDepth: 24, budget: 200 * time.Millisecond},
	}

	for _, tc := range cases {
		tc := tc
		name := fmt.Sprintf("stars=%d_depth=%d", tc.stars, tc.regularDepth)
		t.Run(name, func(t *testing.T) {
			dynamic, regular := adversarialMultiWildcardInputs(tc.stars, tc.regularDepth)

			start := time.Now()
			got := dynamicpathdetector.CompareDynamic(dynamic, regular)
			elapsed := time.Since(start)

			// Behavioural: tail mismatch means no match.
			require.False(t, got,
				"CompareDynamic should reject %q against %q (tail mismatch)", dynamic, regular)

			// Performance ceiling.
			require.LessOrEqualf(t, elapsed.Nanoseconds(), tc.budget.Nanoseconds(),
				"CompareDynamic took %v on adversarial input (stars=%d depth=%d); budget was %v — "+
					"un-memoised exponential matcher detected",
				elapsed, tc.stars, tc.regularDepth, tc.budget)
		})
	}
}

// TestCompareDynamic_MemoiseAdversarialPositive pins the dual: an
// adversarial multi-`*` input that DOES match must also complete
// inside the budget. Without memoisation, even positive cases can
// hit pathological re-entry on early backtracks.
func TestCompareDynamic_MemoiseAdversarialPositive(t *testing.T) {
	if testing.Short() {
		t.Skip("skip timing-sensitive ReDoS regression in short mode")
	}

	// /*/*/*/.../tail against /a/b/c/.../tail
	stars := 20
	starsList := make([]string, stars)
	for i := range starsList {
		starsList[i] = "*"
	}
	dynamic := "/" + strings.Join(starsList, "/") + "/tail"

	segs := make([]string, stars)
	for i := range segs {
		segs[i] = fmt.Sprintf("seg%d", i)
	}
	regular := "/" + strings.Join(segs, "/") + "/tail"

	start := time.Now()
	got := dynamicpathdetector.CompareDynamic(dynamic, regular)
	elapsed := time.Since(start)

	require.True(t, got,
		"CompareDynamic should accept %q against %q (matching tail)", dynamic, regular)
	require.LessOrEqualf(t, elapsed.Nanoseconds(), (200 * time.Millisecond).Nanoseconds(),
		"positive-case adversarial took %v; budget 200ms — memoisation regressed",
		elapsed)
}

// TestCompareDynamic_MemoiseAllocCeiling pins the allocation profile:
// memoisation must not introduce per-call heap growth that scales with
// pattern complexity for the COMMON-shape inputs the matcher sees
// in production (the BenchmarkCompareDynamic shapes in benchmark_test.go).
// We measure allocs/op via runtime.ReadMemStats around a tight call
// loop and require the per-call growth to remain bounded.
//
// Tolerance is intentionally loose (12 allocs/op) so a future tuning
// pass with a smaller memo table doesn't false-trip this. The current
// pre-memoisation matcher reports 2 allocs/op on these shapes; the
// memoised version is expected to remain in single digits.
func TestCompareDynamic_MemoiseAllocCeiling(t *testing.T) {
	if testing.Short() {
		t.Skip("skip alloc-tracking gate in short mode")
	}

	cases := []struct {
		name    string
		dynamic string
		regular string
	}{
		{"trailing_star", "/etc/*", "/etc/passwd"},
		{"mid_star_zero", "/a/*/b", "/a/b"},
		{"ellipsis_deep", "/var/log/⋯/access.log", "/var/log/nginx/sub/access.log"},
		{"deep_literal_match", "/a/b/c/d/e/f/g/h", "/a/b/c/d/e/f/g/h"},
	}

	const iter = 100_000
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Warm up — first call may incur one-time setup allocs.
			_ = dynamicpathdetector.CompareDynamic(tc.dynamic, tc.regular)

			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)

			for i := 0; i < iter; i++ {
				_ = dynamicpathdetector.CompareDynamic(tc.dynamic, tc.regular)
			}

			runtime.ReadMemStats(&after)
			perCall := float64(after.Mallocs-before.Mallocs) / float64(iter)
			require.LessOrEqualf(t, perCall, 12.0,
				"%s: per-call allocs = %.2f, expected ≤ 12 — memoisation introduced unbounded heap growth",
				tc.name, perCall)
		})
	}
}

// TestCompareDynamic_ZeroAllocHotPath pins the hot-path perf contract:
// the 0-or-1 `*` shapes — including the R0002 hot path
// (`/etc/*` vs `/etc/ssh/sshd_config`) — MUST execute with zero
// allocations. The pre-PR splitPath dispatch made every call allocate
// 2 slices (~112 B); the index-based compareSegmentsIndex path restored
// the upstream zero-alloc target.
//
// This pins the contract structurally — any future refactor that
// re-introduces splitPath on the 0/1-`*` path fails this test.
func TestCompareDynamic_ZeroAllocHotPath(t *testing.T) {
	cases := []struct {
		name, dyn, reg string
	}{
		// 0-`*` shapes
		{"literal_exact_match", "/etc/resolv.conf", "/etc/resolv.conf"},
		{"literal_mismatch", "/etc/resolv.conf", "/etc/passwd"},
		{"ellipsis_match", "/api/⋯/users", "/api/v1/users"},
		// 1-`*` shapes — the R0002 hot path
		{"trailing_star_match", "/etc/*", "/etc/ssh/sshd_config"},
		{"trailing_star_no_match_on_parent", "/etc/*", "/etc"},
		{"mid_star_zero", "/a/*/b", "/a/b"},
		{"mid_star_many", "/a/*/b", "/a/x/y/z/b"},
		{"unanchored_star", "*", "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Warm-up to absorb any one-off setup cost.
			for i := 0; i < 100; i++ {
				_ = dynamicpathdetector.CompareDynamic(tc.dyn, tc.reg)
			}
			var before, after runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&before)
			const iters = 10_000
			for i := 0; i < iters; i++ {
				_ = dynamicpathdetector.CompareDynamic(tc.dyn, tc.reg)
			}
			runtime.ReadMemStats(&after)
			allocs := after.Mallocs - before.Mallocs
			require.Equalf(t, uint64(0), allocs,
				"%s: %d allocs across %d iters — 0/1-`*` shapes MUST be zero-allocation",
				tc.name, allocs, iters)
		})
	}
}
