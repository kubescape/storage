package dynamicpathdetectortests

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// configThreshold returns the collapse threshold for the given path prefix
// as this test suite expects it — sourced from testCollapseConfigs (defined
// in analyze_opens_test.go), NOT from the production DefaultCollapseConfigs.
// The decoupling is deliberate: tests probe threshold-1/3/5 edge cases and
// shouldn't constrain what values ship in production defaults.
// Falls back to DefaultCollapseConfig.Threshold for unknown prefixes.
func configThreshold(prefix string) int {
	for _, cfg := range testCollapseConfigs {
		if cfg.Prefix == prefix {
			return cfg.Threshold
		}
	}
	return dynamicpathdetector.DefaultCollapseConfig.Threshold
}

func TestNewPathAnalyzerWithConfigs(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.OpenDynamicThreshold, nil)
	if analyzer == nil {
		t.Error("NewPathAnalyzerWithConfigs() returned nil")
	}
}

func TestAnalyzePath(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.OpenDynamicThreshold, nil)

	testCases := []struct {
		name       string
		path       string
		identifier string
		expected   string
	}{
		{"Simple path", "/api/users/123", "api", "/api/users/123"},
		{"Multiple segments", "/api/users/123/posts/456", "api", "/api/users/123/posts/456"},
		{"Root path", "/api/", "api", "/api"},
		{"Empty path", "/api/", "api", "/api"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := analyzer.AnalyzePath(tc.path, tc.identifier)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestCollapseAdjacentDynamicIdentifiers(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		expected string
	}{
		{"No dynamic identifiers", "/a/b/c", "/a/b/c"},
		{"Single dynamic identifier", "/a/\u22ef/c", "/a/\u22ef/c"},
		{"Two adjacent dynamic identifiers", "/a/\u22ef/\u22ef/d", "/a/*/d"},
		{"Three adjacent dynamic identifiers", "/a/\u22ef/\u22ef/\u22ef/e", "/a/*/e"},
		{"Dynamic identifiers separated by static segment", "/\u22ef/b/\u22ef/d", "/\u22ef/b/\u22ef/d"},
		{"Multiple groups of adjacent identifiers", "/\u22ef/\u22ef/c/\u22ef/\u22ef/f", "/*/c/*/f"},
		{"Starts with adjacent identifiers", "/\u22ef/\u22ef/c", "/*/c"},
		{"Ends with adjacent identifiers", "/a/\u22ef/\u22ef", "/a/*"},
		{"Only adjacent identifiers", "/\u22ef/\u22ef", "/*"},
		{"Path with leading slash", "/\u22ef/\u22ef", "/*"},
		{"Empty path", "", ""},
		{"Single segment path", "a", "a"},
		{"Single dynamic segment path", "\u22ef", "\u22ef"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := dynamicpathdetector.CollapseAdjacentDynamicIdentifiers(tc.path)
			assert.Equal(t, tc.expected, result, "Path was not collapsed as expected. Got %s, want %s", result, tc.expected)
		})
	}
}

func TestDynamicSegments(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)

	for i := 0; i < threshold+1; i++ {
		path := fmt.Sprintf("/api/users/%d", i)
		_, err := analyzer.AnalyzePath(path, "api")
		assert.NoError(t, err)
	}

	result, err := analyzer.AnalyzePath(fmt.Sprintf("/api/users/%d", threshold+1), "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	expected := "/api/users/\u22ef"
	assert.Equal(t, expected, result)

	// Test with one of the original IDs to ensure it's also marked as dynamic
	result, err = analyzer.AnalyzePath("/api/users/0", "api")
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestMultipleDynamicSegments(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)

	for i := 0; i < threshold+10; i++ {
		path := fmt.Sprintf("/api/users/%d/posts/%d", i, i)
		_, err := analyzer.AnalyzePath(path, "api")
		if err != nil {
			t.Errorf("AnalyzePath() returned an error: %v", err)
		}
	}

	result, err := analyzer.AnalyzePath(fmt.Sprintf("/api/users/%d/posts/%d", threshold+11, threshold+11), "api")
	assert.NoError(t, err)
	expected := "/api/users/\u22ef/posts/\u22ef"
	assert.Equal(t, expected, result)
}

func TestMixedStaticAndDynamicSegments(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)

	for i := 0; i < threshold+1; i++ {
		path := fmt.Sprintf("/api/users/%d/posts", i)
		_, err := analyzer.AnalyzePath(path, "api")
		if err != nil {
			t.Errorf("AnalyzePath() returned an error: %v", err)
		}
	}

	result, err := analyzer.AnalyzePath("/api/users/0/posts", "api")
	assert.NoError(t, err)
	expected := "/api/users/\u22ef/posts"
	assert.Equal(t, expected, result)
}

func TestDifferentRootIdentifiers(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.OpenDynamicThreshold, nil)

	result1, _ := analyzer.AnalyzePath("/api/users/123", "api")
	result2, _ := analyzer.AnalyzePath("/api/products/456", "store")

	assert.Equal(t, "/api/users/123", result1)
	assert.Equal(t, "/api/products/456", result2)
}

func TestDynamicThreshold(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)

	for i := 0; i < threshold+1; i++ {
		path := fmt.Sprintf("/api/users/%d", i)
		result, _ := analyzer.AnalyzePath(path, "api")
		if result != fmt.Sprintf("/api/users/%d", i) {
			t.Errorf("Path became dynamic before reaching %d different paths", threshold)
		}
	}

	result, _ := analyzer.AnalyzePath(fmt.Sprintf("/api/users/%d", threshold+2), "api")
	assert.Equal(t, "/api/users/\u22ef", result)
}

func TestEdgeCases(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.OpenDynamicThreshold, nil)

	testCases := []struct {
		name       string
		path       string
		identifier string
		expected   string
	}{
		{"Path with multiple slashes", "//users///123////", "api", "/users/123"},
		{"Path with special characters", "/users/@johndoe/settings", "api", "/users/@johndoe/settings"},
		{"Very long path", "/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p", "api", "/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := analyzer.AnalyzePath(tc.path, tc.identifier)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDynamicInsertion(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.OpenDynamicThreshold, nil)

	result, err := analyzer.AnalyzePath("/api/users/\u22ef", "api")
	assert.NoError(t, err)
	expected := "/api/users/\u22ef"
	assert.Equal(t, expected, result)

	result, err = analyzer.AnalyzePath("/api/users/102", "api")
	assert.NoError(t, err)
	expected = "/api/users/\u22ef"
	assert.Equal(t, expected, result)
}

func TestDynamic(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)
	for i := 0; i < threshold+1; i++ {
		path := fmt.Sprintf("/api/users/%d", i)
		_, err := analyzer.AnalyzePath(path, "api")
		assert.NoError(t, err)
	}
	result, err := analyzer.AnalyzePath(fmt.Sprintf("/api/users/%d", threshold+1), "api")
	assert.NoError(t, err)
	expected := "/api/users/\u22ef"
	assert.Equal(t, expected, result)
}

func TestCollapseConfig(t *testing.T) {
	appThreshold := configThreshold("/app")
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.OpenDynamicThreshold, []dynamicpathdetector.CollapseConfig{
		{
			Prefix:    "/api",
			Threshold: appThreshold,
		},
		{
			Prefix:    "/169.254.169.254",
			Threshold: configThreshold("/etc"),
		},
	})
	for i := 0; i < appThreshold+1; i++ {
		path := fmt.Sprintf("/api/users/%d", i)
		_, err := analyzer.AnalyzePath(path, "api")
		assert.NoError(t, err)
	}
	result, err := analyzer.AnalyzePath(fmt.Sprintf("/api/users/%d", appThreshold+1), "api")
	assert.NoError(t, err)
	expected := "/api/*"
	assert.Equal(t, expected, result)
}

// TestProcessSegments_WildcardWiringRegressions pins three correctness
// properties of processSegments that were broken in the zero-alloc rewrite
// of analyzer.go and caused node-agent component-test Test_27
// (ApplicationProfileOpens) to fail at runtime.
//
// Each sub-case is small, self-contained, and would fail against the
// broken implementation — keeping them here means a future refactor of
// the zero-alloc hot path can't silently re-introduce any of these bugs.
func TestProcessSegments_WildcardWiringRegressions(t *testing.T) {
	// Bug 1: threshold=1 configured for prefix P must wildcard P's
	// CHILDREN, not P itself. The broken code used p[:i] (path
	// including the current segment) for the threshold lookup, so
	// inserting segment "app" saw threshold=1 from {Prefix:"/app"}
	// and wildcarded at the root, producing "/*/*/*" instead of
	// "/app/*". Fix: use p[:start] (path BEFORE the current segment)
	// for the insertion-threshold lookup so the config governs the
	// parent's children, not the current segment's insertion.
	t.Run("threshold_1_wildcards_children_not_prefix", func(t *testing.T) {
		analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(
			dynamicpathdetector.OpenDynamicThreshold,
			[]dynamicpathdetector.CollapseConfig{{Prefix: "/app", Threshold: 1}},
		)
		got, err := analyzer.AnalyzePath("/app/service-a/config", "id")
		assert.NoError(t, err)
		assert.Equal(t, "/app/*", got,
			"threshold-1 on /app must wildcard /app's children only, not collapse the /app prefix itself")
	})

	// Bug 2: once a segment has been emitted as `*`, the walk must
	// stop appending subsequent path segments — otherwise every
	// remaining segment re-follows the wildcard branch and emits
	// "/*" again, producing "/a/*/*/*" where "/a/*" is correct.
	// Fix: break out of the segment-walk loop as soon as
	// currentNode.SegmentName == WildcardIdentifier.
	t.Run("wildcard_absorbs_remaining_path_tail", func(t *testing.T) {
		analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(
			dynamicpathdetector.OpenDynamicThreshold,
			[]dynamicpathdetector.CollapseConfig{{Prefix: "/short", Threshold: 1}},
		)
		got, err := analyzer.AnalyzePath("/short/a/b/c/d/e/f", "id")
		assert.NoError(t, err)
		assert.Equal(t, "/short/*", got,
			"once the wildcard fires, the remaining 5 segments must not each emit an extra '/*'")
	})

	// Bug 3: when updateNodeStats collapses N children into a single
	// ⋯ node, the ⋯ node's Count was left at 0. Subsequent walks
	// descending into ⋯ then never re-triggered the collapse check,
	// even when the absorbed grandchildren independently exceeded
	// the threshold at the next level. Result: a grid like
	// /a/{many}/{many}/leaf collapsed the first level but left
	// grandchild literals visible in the output (e.g.
	// "/a/⋯/sub0/⋯", "/a/⋯/sub1/⋯", ...). Fix: set
	// dynamicChild.Count = len(dynamicChild.Children) after merge
	// so the next level's updateNodeStats sees the true branching.
	t.Run("multi_level_collapse_propagates_to_grandchildren", func(t *testing.T) {
		threshold := 3
		analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)
		// threshold+1 (=4) children × threshold+1 (=4) grandchildren = 16 paths.
		// Both levels independently exceed threshold 3.
		for i := 0; i <= threshold; i++ {
			for j := 0; j <= threshold; j++ {
				_, _ = analyzer.AnalyzePath(
					fmt.Sprintf("/grid/level%d/sub%d/file", i, j), "id")
			}
		}
		// Any path from the grid, re-analyzed, should be maximally
		// collapsed — no grandchild literals should survive.
		got, err := analyzer.AnalyzePath("/grid/level0/sub0/file", "id")
		assert.NoError(t, err)
		assert.NotContains(t, got, "sub0",
			"grandchild literals must not survive in the collapsed output — got %q", got)
		assert.NotContains(t, got, "level0",
			"child literals must not survive in the collapsed output — got %q", got)
	})
}

// TestCompareDynamic_WildcardRegressions pins two cases that were
// silently wrong in the original compareSegments implementation:
//
//  1. Consecutive wildcards (`/*/*`): the inner loop required
//     regular[i] == nextDynamic *before* recursing, which never fires
//     when nextDynamic is itself `*` (no real path segment literally
//     equals "*"). A user-authored profile with `/*/*` could never
//     match any concrete path → R0002 fires where it shouldn't.
//
//  2. Zero-segment wildcard consumption (`/*/foo` matching `/foo`):
//     the wildcard should be allowed to consume zero segments and
//     then let the next static segment match. The old optimistic
//     peek at dynamic[1] happened to get this case right when
//     dynamic[1] was a literal matching regular[0], but was broken
//     for cases where the next segment was itself a wildcard.
//
// Both are fixed by dropping the peek: unconditionally recurse at
// every i in [0, len(regular)] and let the recursion decide.
func TestCompareDynamic_WildcardRegressions(t *testing.T) {
	tests := []struct {
		name    string
		dynamic string
		regular string
		want    bool
	}{
		// Bug 1: consecutive wildcards.
		{
			name:    "consecutive_wildcards_match_two_segments",
			dynamic: "/*/*",
			regular: "/foo/bar",
			want:    true,
		},
		{
			name:    "consecutive_wildcards_match_one_segment",
			dynamic: "/*/*",
			regular: "/foo",
			want:    true, // second * consumes zero segments
		},
		{
			name:    "triple_wildcards_match_any_depth",
			dynamic: "/*/*/*",
			regular: "/a/b/c",
			want:    true,
		},
		// Bug 2: zero-segment wildcard consumption.
		{
			name:    "wildcard_consumes_zero_then_literal_match",
			dynamic: "/*/foo",
			regular: "/foo",
			want:    true,
		},
		{
			name:    "wildcard_consumes_zero_between_literals",
			dynamic: "/a/*/b",
			regular: "/a/b",
			want:    true,
		},
		{
			name:    "wildcard_consumes_many_then_literal_match",
			dynamic: "/*/foo",
			regular: "/a/b/c/foo",
			want:    true,
		},
		// Sanity: non-regressions — cases that must still return false.
		{
			name:    "literal_suffix_mismatch_still_false",
			dynamic: "/*/foo",
			regular: "/a/b/baz",
			want:    false,
		},
		{
			name:    "literal_prefix_mismatch_still_false",
			dynamic: "/api/*",
			regular: "/web/a",
			want:    false,
		},
		{
			name:    "empty_tail_with_unsatisfied_literal_false",
			dynamic: "/*/x",
			regular: "/",
			want:    false,
		},
		// Interaction with DynamicIdentifier (⋯, single segment).
		{
			name:    "mixed_wildcard_and_dynamic_match",
			dynamic: "/⋯/*",
			regular: "/foo/bar/baz",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dynamicpathdetector.CompareDynamic(tt.dynamic, tt.regular)
			assert.Equal(t, tt.want, got,
				"CompareDynamic(%q, %q) = %v, want %v", tt.dynamic, tt.regular, got, tt.want)
		})
	}
}

// TestCompareDynamic_AnchoringAndTrailing pins the trailing-`*` /
// anchoring contract reported on upstream PR #316.
//
// The first wildcard-aware implementation made a trailing `*` match
// zero-or-more remaining segments, so `/etc/*` silently matched the
// bare `/etc` directory — widening R0002's blind spot to cover the
// profiled directory's parent. Standard shell glob requires trailing
// `*` to consume one or more segments. Anchoring rules:
//
//   - Anchored: leading `/` makes `/*` "any path strictly under /"
//     and explicitly excludes the bare `/` directory.
//   - Unanchored: a bare `*` (no leading slash) is the only way to
//     allowlist the root path itself.
//
// Trailing slashes on the regular path are normalized away so
// `/etc/passwd/` is treated as `/etc/passwd`.
func TestCompareDynamic_AnchoringAndTrailing(t *testing.T) {
	tests := []struct {
		name    string
		dynamic string
		regular string
		want    bool
	}{
		// Root-only cases — anchored vs unanchored distinction.
		{"anchored_star_does_not_match_root", "/*", "/", false},
		{"anchored_star_does_not_match_empty", "/*", "", false},
		{"anchored_star_matches_top_level_child", "/*", "/foo", true},
		{"anchored_star_matches_deeper_child", "/*", "/foo/bar", true},
		{"unanchored_star_matches_root", "*", "/", true},
		{"unanchored_star_matches_top_level_child", "*", "/foo", true},
		{"unanchored_star_does_not_match_empty", "*", "", false},

		// Bare-parent boundary — the original /etc/* regression.
		{"trailing_star_does_not_match_bare_parent", "/etc/*", "/etc", false},
		{"trailing_star_does_not_match_parent_with_slash", "/etc/*", "/etc/", false},
		{"trailing_star_matches_immediate_child", "/etc/*", "/etc/passwd", true},
		{"trailing_star_matches_deep_child", "/etc/*", "/etc/ssh/sshd_config", true},
		{"trailing_star_matches_child_with_trailing_slash", "/etc/*", "/etc/passwd/", true},
		{"deep_trailing_star_does_not_match_short_path", "/var/log/*", "/var/log", false},
		{"deep_trailing_star_does_not_match_grandparent", "/var/log/*", "/var", false},
		{"deep_trailing_star_matches_child", "/var/log/*", "/var/log/syslog", true},

		// Multiple trailing wildcards — zero-or-more mid-* + one-or-more
		// final-* together. The mid-* may consume zero, so /etc/*/* still
		// matches /etc/ssh (one segment) by having the inner * consume 0
		// and the trailing * consume the segment.
		{"double_trailing_does_not_match_parent", "/etc/*/*", "/etc", false},
		{"double_trailing_does_not_match_parent_slash", "/etc/*/*", "/etc/", false},
		{"double_trailing_matches_one_child", "/etc/*/*", "/etc/ssh", true},
		{"double_trailing_matches_two_children", "/etc/*/*", "/etc/ssh/sshd_config", true},
		{"double_trailing_matches_deep", "/etc/*/*", "/etc/ssh/dir/file", true},

		// Empty / bare-pattern edges.
		{"empty_dynamic_does_not_match_path", "", "/foo", false},
		{"empty_dynamic_does_not_match_root", "", "/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dynamicpathdetector.CompareDynamic(tt.dynamic, tt.regular)
			assert.Equal(t, tt.want, got,
				"CompareDynamic(%q, %q) = %v, want %v", tt.dynamic, tt.regular, got, tt.want)
		})
	}
}

// TestCompareDynamic_EllipsisAndStar pins the interaction between the
// two wildcard kinds:
//
//   - DynamicIdentifier (⋯) consumes EXACTLY ONE segment.
//   - WildcardIdentifier (*) consumes ZERO-OR-MORE segments mid-path
//     and ONE-OR-MORE segments when trailing.
//
// Mixing them (e.g. `/⋯/*`) is the analyzer's normal output for a
// fully-collapsed grandchild branch: ⋯ pins the immediate child to
// "any single segment" and * accepts the deeper tail.
func TestCompareDynamic_EllipsisAndStar(t *testing.T) {
	tests := []struct {
		name    string
		dynamic string
		regular string
		want    bool
	}{
		// ⋯ alone: exactly one segment.
		{"ellipsis_matches_exactly_one", "/⋯/foo", "/x/foo", true},
		{"ellipsis_does_not_consume_zero", "/⋯/foo", "/foo", false},
		{"ellipsis_does_not_consume_two", "/⋯/foo", "/x/y/foo", false},

		// ⋯ then trailing *: ⋯ consumes 1, * needs ≥1 more.
		{"ellipsis_then_trailing_star_two_segments", "/⋯/*", "/x/y", true},
		{"ellipsis_then_trailing_star_three_segments", "/⋯/*", "/x/y/z", true},
		{"ellipsis_then_trailing_star_one_segment_fails", "/⋯/*", "/x", false},
		{"ellipsis_then_trailing_star_root_fails", "/⋯/*", "/", false},

		// Mid-* before ⋯: * may consume zero, ⋯ still needs exactly one.
		{"star_then_ellipsis_two_segments", "/*/⋯", "/a/b", true},
		{"star_consumed_zero_then_ellipsis_matches_one", "/*/⋯", "/b", true},
		{"star_then_ellipsis_one_segment_fails_when_zero_consumed", "/*/⋯", "/", false},

		// Nested ⋯.
		{"nested_ellipsis_matches_two", "/⋯/⋯/foo", "/x/y/foo", true},
		{"nested_ellipsis_does_not_match_one", "/⋯/⋯/foo", "/x/foo", false},

		// * literal * pattern around a static segment.
		{"star_literal_star_matches", "/*/etc/*", "/foo/etc/passwd", true},
		{"star_literal_star_no_trailing_segment_fails", "/*/etc/*", "/foo/etc", false},
		{"star_literal_star_no_leading_consumed_zero_matches", "/*/etc/*", "/etc/passwd", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dynamicpathdetector.CompareDynamic(tt.dynamic, tt.regular)
			assert.Equal(t, tt.want, got,
				"CompareDynamic(%q, %q) = %v, want %v", tt.dynamic, tt.regular, got, tt.want)
		})
	}
}

// TestCompareDynamic_MidPathStarZeroOrMore explicitly pins the
// zero-or-more semantics for `*` when it appears mid-path. This is
// distinct from trailing `*` (which is one-or-more) and is what allows
// auto-generated patterns like `/foo/*/bar` to also match `/foo/bar`
// (the wildcard consumed nothing, then the literal segment matched).
func TestCompareDynamic_MidPathStarZeroOrMore(t *testing.T) {
	tests := []struct {
		name    string
		dynamic string
		regular string
		want    bool
	}{
		{"mid_star_consumes_zero", "/a/*/b", "/a/b", true},
		{"mid_star_consumes_one", "/a/*/b", "/a/x/b", true},
		{"mid_star_consumes_many", "/a/*/b", "/a/x/y/b", true},
		{"mid_star_literal_after_mismatches", "/a/*/b", "/a/x/c", false},
		{"mid_star_literal_prefix_mismatch", "/a/*/b", "/z/x/b", false},

		// Consecutive mid-stars — both can independently consume zero.
		{"consecutive_mid_star_both_zero", "/a/*/*/b", "/a/b", true},
		{"consecutive_mid_star_one_zero_one_one", "/a/*/*/b", "/a/x/b", true},
		{"consecutive_mid_star_both_one", "/a/*/*/b", "/a/x/y/b", true},
		{"consecutive_mid_star_deeper", "/a/*/*/b", "/a/x/y/z/b", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dynamicpathdetector.CompareDynamic(tt.dynamic, tt.regular)
			assert.Equal(t, tt.want, got,
				"CompareDynamic(%q, %q) = %v, want %v", tt.dynamic, tt.regular, got, tt.want)
		})
	}
}

// TestDefaultCollapseConfigs_DefensiveCopy pins the contract that the
// public DefaultCollapseConfigs() accessor returns a fresh slice on
// every call, so callers cannot accidentally mutate the package-level
// state. Without this guard, a downstream consumer modifying the
// returned slice (sorting, appending, swapping prefixes) would silently
// affect every subsequent call, including AnalyzeOpens in the storage
// deflate path. The unexported var is the canonical source of truth.
func TestDefaultCollapseConfigs_DefensiveCopy(t *testing.T) {
	first := dynamicpathdetector.DefaultCollapseConfigs()
	require.NotEmpty(t, first, "default configs must not be empty")

	// Mutate the returned slice in two ways: change a Threshold and
	// append a junk config. Neither should leak to the next call.
	first[0].Threshold = 999_999
	first = append(first, dynamicpathdetector.CollapseConfig{
		Prefix: "/poisoned", Threshold: 1,
	})

	second := dynamicpathdetector.DefaultCollapseConfigs()
	assert.NotEqual(t, 999_999, second[0].Threshold,
		"mutating the first call's slice must not change the package state")
	for _, cfg := range second {
		assert.NotEqual(t, "/poisoned", cfg.Prefix,
			"appending to the first call's slice must not leak into the second call")
	}

	// Also assert the two slices are distinct backing arrays — without
	// this, len(second) would happen to be safe but a future caller
	// reading first[len-1] could observe the appended element.
	if len(first) > 0 && len(second) > 0 {
		// Address-of-element comparison is sufficient; if the underlying
		// array is shared, &first[0] == &second[0].
		assert.NotSame(t, &first[0], &second[0],
			"DefaultCollapseConfigs must return a fresh backing array")
	}
}

// TestCompareDynamic_PathSeparatorEdges documents how `/`-related
// edges are normalized: trailing slashes are insignificant, the
// regular path `""` is treated as no-path (matches nothing), and the
// internal split-and-trim normalization is exercised on both sides.
func TestCompareDynamic_PathSeparatorEdges(t *testing.T) {
	tests := []struct {
		name    string
		dynamic string
		regular string
		want    bool
	}{
		// Trailing slash on regular — should match same as without.
		{"trailing_slash_on_regular_matches_literal", "/etc/passwd", "/etc/passwd/", true},
		{"trailing_slash_on_regular_for_directory_match", "/etc", "/etc/", true},

		// Trailing slash on dynamic — should match same as without.
		{"trailing_slash_on_dynamic_literal", "/etc/passwd/", "/etc/passwd", true},
		{"trailing_slash_on_dynamic_with_star", "/etc/*/", "/etc/passwd", true},

		// Empty regular path — matches nothing, including the bare star.
		{"empty_regular_does_not_match_anchored", "/foo", "", false},
		{"empty_regular_does_not_match_unanchored_literal", "foo", "", false},
		{"empty_regular_does_not_match_star", "*", "", false},

		// Empty dynamic — matches nothing.
		{"empty_dynamic_does_not_match_anything", "", "/foo", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dynamicpathdetector.CompareDynamic(tt.dynamic, tt.regular)
			assert.Equal(t, tt.want, got,
				"CompareDynamic(%q, %q) = %v, want %v", tt.dynamic, tt.regular, got, tt.want)
		})
	}
}
