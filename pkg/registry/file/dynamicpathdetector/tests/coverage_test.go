package dynamicpathdetectortests

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
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
