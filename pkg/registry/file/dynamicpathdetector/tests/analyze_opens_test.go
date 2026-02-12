package dynamicpathdetectortests

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
)

func TestAnalyzeOpensWithThreshold(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	var input []types.OpenCalls
	for i := 0; i < threshold+1; i++ {
		input = append(input, types.OpenCalls{
			Path: fmt.Sprintf("/home/user%d/file.txt", i),
		})
	}

	expected := []types.OpenCalls{
		{
			Path:  "/home/\u22ef/file.txt",
			Flags: []string{},
		},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, expected, result)
}

func TestAnalyzeOpensWithFlagMergingAndThreshold(t *testing.T) {
	// Use /var/run threshold (3) — low enough that hand-written subtests work
	threshold := configThreshold("/var/run")

	tests := []struct {
		name     string
		input    []types.OpenCalls
		expected []types.OpenCalls
	}{
		{
			name:  "Merge flags for paths exceeding threshold",
			input: generateOpenCallsWithFlags("/home", "file.txt", threshold+1),
			expected: []types.OpenCalls{
				{Path: "/home/\u22ef/file.txt", Flags: flagsForN(threshold + 1)},
			},
		},
		{
			name: "No merging for paths not exceeding threshold",
			input: []types.OpenCalls{
				{Path: "/home/user2/file2.txt", Flags: []string{"WRITE"}},
				{Path: "/home/user3/file3.txt", Flags: []string{"APPEND"}},
			},
			expected: []types.OpenCalls{
				{Path: "/home/user2/file2.txt", Flags: []string{"WRITE"}},
				{Path: "/home/user3/file3.txt", Flags: []string{"APPEND"}},
			},
		},
		{
			name: "Partial merging for some paths exceeding threshold",
			input: append(
				generateOpenCallsWithFlags("/home", "common.txt", threshold+1),
				types.OpenCalls{Path: "/var/log/app1.log", Flags: []string{"READ"}},
				types.OpenCalls{Path: "/var/log/app2.log", Flags: []string{"WRITE"}},
			),
			expected: []types.OpenCalls{
				{Path: "/home/\u22ef/common.txt", Flags: flagsForN(threshold + 1)},
				{Path: "/var/log/app1.log", Flags: []string{"READ"}},
				{Path: "/var/log/app2.log", Flags: []string{"WRITE"}},
			},
		},
		{
			name: "Multiple dynamic segments",
			input: append(
				generateOpenCallsWithFlags("/home", "file1.txt", threshold+1),
				generateOpenCallsWithFlags("/home", "file2.txt", threshold+1)...,
			),
			expected: []types.OpenCalls{
				{Path: "/home/\u22ef/file1.txt", Flags: flagsForN(threshold + 1)},
				{Path: "/home/\u22ef/file2.txt", Flags: flagsForN(threshold + 1)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)
			result, err := dynamicpathdetector.AnalyzeOpens(tt.input, analyzer, mapset.NewSet[string]())
			assert.NoError(t, err)

			assert.Equal(t, tt.expected, result)

			// Additional check for flag uniqueness
			for _, openCall := range result {
				assert.True(t, areStringSlicesUnique(openCall.Flags), "Flags are not unique for path %s: %v", openCall.Path, openCall.Flags)
			}
		})
	}
}

func TestAnalyzeOpensWithAsteriskAndEllipsis(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	// Generate threshold paths + one ⋯ path to trigger collapse
	var input []types.OpenCalls
	for i := 0; i < threshold; i++ {
		input = append(input, types.OpenCalls{
			Path: fmt.Sprintf("/home/user%d/file.txt", i), Flags: []string{"READ"},
		})
	}
	input = append(input,
		types.OpenCalls{Path: "/home/\u22ef/file.txt", Flags: []string{"READ"}},
		types.OpenCalls{Path: fmt.Sprintf("/home/user%d/file.txt", threshold), Flags: []string{"READ"}},
	)

	expected := []types.OpenCalls{
		{Path: "/home/\u22ef/file.txt", Flags: []string{"READ"}},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	assert.ElementsMatch(t, expected, result)
}

func TestAnalyzeOpensWithMultiCollapse(t *testing.T) {
	// Use a threshold higher than the /var/run config (3) so /var/run paths do NOT collapse
	threshold := dynamicpathdetector.DefaultCollapseConfig.Threshold
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	// Only 3 paths under /var/run — the per-prefix threshold for /var/run is 3,
	// but NewPathAnalyzer overrides the default to 'threshold', so /var/run inherits its own config (3).
	// 3 children <= threshold 3, so these should NOT collapse.
	input := []types.OpenCalls{
		{Path: "/var/run/txt/file.txt", Flags: []string{"READ"}},
		{Path: "/var/run/txt1/file.txt", Flags: []string{"READ"}},
		{Path: "/var/run/txt2/file.txt", Flags: []string{"READ"}},
	}

	expected := []types.OpenCalls{
		{Path: "/var/run/txt/file.txt", Flags: []string{"READ"}},
		{Path: "/var/run/txt1/file.txt", Flags: []string{"READ"}},
		{Path: "/var/run/txt2/file.txt", Flags: []string{"READ"}},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	assert.ElementsMatch(t, expected, result)
}

func TestAnalyzeOpensWithDynamicConfigs(t *testing.T) {
	etcThreshold := configThreshold("/etc")
	optThreshold := configThreshold("/opt")
	varRunThreshold := configThreshold("/var/run")
	appThreshold := configThreshold("/app")
	tmpThreshold := 10 // custom for this test

	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
		{Prefix: "/etc", Threshold: etcThreshold},
		{Prefix: "/opt", Threshold: optThreshold},
		{Prefix: "/var/run", Threshold: varRunThreshold},
		{Prefix: "/app", Threshold: appThreshold},
		{Prefix: "/tmp", Threshold: tmpThreshold},
	})

	var pathsToAdd []string

	// /etc paths (high threshold) - should not collapse
	for i := 0; i < 8; i++ {
		pathsToAdd = append(pathsToAdd, fmt.Sprintf("/etc/config/item%d", i))
	}
	pathsToAdd = append(pathsToAdd,
		"/etc/hosts",
		"/etc/resolv.conf",
		"/etc/hostname",
		"/etc/systemd/system.conf",
	)
	// Total /etc: 12, well below etcThreshold (50)

	// /opt paths — exceed optThreshold to trigger collapse
	for i := 0; i < optThreshold+1; i++ {
		pathsToAdd = append(pathsToAdd, fmt.Sprintf("/opt/app%d/binary", i))
	}

	// /var/run paths — exceed varRunThreshold to trigger collapse
	for i := 0; i < varRunThreshold+1; i++ {
		pathsToAdd = append(pathsToAdd, fmt.Sprintf("/var/run/pid%d.pid", i))
	}

	// /app paths — appThreshold is 1, so second child triggers wildcard
	pathsToAdd = append(pathsToAdd,
		"/app/some/deep/path",
		"/app/another/path",
	)

	// /tmp paths — exceed tmpThreshold to trigger collapse
	for i := 0; i < tmpThreshold+1; i++ {
		pathsToAdd = append(pathsToAdd, fmt.Sprintf("/tmp/user%d/a", i))
	}

	var input []types.OpenCalls
	for _, p := range pathsToAdd {
		input = append(input, types.OpenCalls{Path: p, Flags: []string{"READ"}})
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	// /etc paths (threshold 50) should NOT be collapsed
	etcPaths := filterByPrefix(result, "/etc/")
	assert.Equal(t, 12, len(etcPaths), "/etc paths should remain individual (below threshold %d)", etcThreshold)

	// /app (threshold 1) - immediately collapses to wildcard
	assertContainsPath(t, result, "/app/*")

	// /opt — collapses; both wildcard and dynamic-with-subtree are acceptable
	assertContainsOneOfPaths(t, result, "/opt/*", "/opt/\u22ef/binary")

	// /tmp — collapses
	assertContainsOneOfPaths(t, result, "/tmp/*", "/tmp/\u22ef/a")

	// /var/run — collapses
	assertContainsOneOfPaths(t, result, "/var/run/*", "/var/run/\u22ef")

	// Total: 12 etc + 1 app + 1 opt + 1 tmp + 1 var/run = 16
	assert.Equal(t, 16, len(result), "expected 16 total paths, got %d: %v", len(result), pathsFromResult(result))
}

// TestAnalyzeOpensCollapseExactBoundary verifies that threshold is strictly "greater than",
// not "greater than or equal". With threshold N, exactly N children should NOT collapse,
// but N+1 children SHOULD.
func TestAnalyzeOpensCollapseExactBoundary(t *testing.T) {
	threshold := dynamicpathdetector.DefaultCollapseConfig.Threshold

	t.Run("at threshold - no collapse", func(t *testing.T) {
		analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)
		var input []types.OpenCalls
		for i := 0; i < threshold; i++ {
			input = append(input, types.OpenCalls{
				Path:  fmt.Sprintf("/data/item%d/info", i),
				Flags: []string{"READ"},
			})
		}
		result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
		assert.NoError(t, err)
		assert.Equal(t, threshold, len(result), "at exact threshold, paths should NOT collapse")
		for _, r := range result {
			assert.NotContains(t, r.Path, "\u22ef", "no dynamic segment expected")
			assert.NotContains(t, r.Path, "*", "no wildcard expected")
		}
	})

	t.Run("above threshold - collapse", func(t *testing.T) {
		analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)
		var input []types.OpenCalls
		for i := 0; i < threshold+1; i++ {
			input = append(input, types.OpenCalls{
				Path:  fmt.Sprintf("/data/item%d/info", i),
				Flags: []string{"READ"},
			})
		}
		result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
		assert.NoError(t, err)
		assert.Equal(t, 1, len(result), "above threshold, paths should collapse to 1")
		assertPathIsOneOf(t, result[0].Path, "/data/*/info", "/data/\u22ef/info")
	})
}

// TestAnalyzeOpensDuplicatePathsNoCollapse verifies that repeating the same path
// many times does NOT trigger a collapse - only unique segment names count.
func TestAnalyzeOpensDuplicatePathsNoCollapse(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)
	var input []types.OpenCalls
	// Repeat the same path many times — should NOT trigger collapse
	for i := 0; i < threshold*10; i++ {
		input = append(input, types.OpenCalls{
			Path:  "/data/same-child/file.txt",
			Flags: []string{"READ"},
		})
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "/data/same-child/file.txt", result[0].Path, "duplicate paths should not trigger collapse")
}

// TestAnalyzeOpensVaryingDepthsUnderPrefix verifies collapse behavior when paths
// under the same prefix have different depths.
func TestAnalyzeOpensVaryingDepthsUnderPrefix(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	// Generate threshold+1 unique children under /data to trigger collapse
	var input []types.OpenCalls
	for i := 0; i < threshold+1; i++ {
		input = append(input, types.OpenCalls{
			Path:  fmt.Sprintf("/data/%c/deep/file", 'a'+rune(i)),
			Flags: []string{"READ"},
		})
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	for _, r := range result {
		assert.True(t,
			strings.Contains(r.Path, "\u22ef") || strings.Contains(r.Path, "*"),
			"path %q should contain a dynamic or wildcard segment after collapse", r.Path)
	}
}

// TestAnalyzeOpensNewPathAfterCollapse verifies that a new path arriving after
// the threshold was already crossed gets absorbed by the collapsed node.
func TestAnalyzeOpensNewPathAfterCollapse(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	// First batch: trigger collapse with threshold+1 children
	var batch1 []types.OpenCalls
	for i := 0; i < threshold+1; i++ {
		batch1 = append(batch1, types.OpenCalls{
			Path: fmt.Sprintf("/srv/%c/log", 'a'+rune(i)), Flags: []string{"READ"},
		})
	}
	result1, err := dynamicpathdetector.AnalyzeOpens(batch1, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result1), "first batch should collapse to 1 path")

	// Second batch: add a completely new child — it should be absorbed
	batch2 := append(batch1, types.OpenCalls{
		Path: "/srv/new-service/log", Flags: []string{"WRITE"},
	})
	result2, err := dynamicpathdetector.AnalyzeOpens(batch2, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result2), "new path after collapse should be absorbed")
	assert.Contains(t, result2[0].Flags, "WRITE", "flags from new path should be merged")
}

// TestAnalyzeOpensDefaultThresholdForUnconfiguredPrefix verifies that paths under
// a prefix without a specific config use the default threshold.
func TestAnalyzeOpensDefaultThresholdForUnconfiguredPrefix(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
		{Prefix: "/configured", Threshold: 2},
	})

	// /configured has threshold 2: 3 children should collapse
	configuredInput := []types.OpenCalls{
		{Path: "/configured/a/file", Flags: []string{"READ"}},
		{Path: "/configured/b/file", Flags: []string{"READ"}},
		{Path: "/configured/c/file", Flags: []string{"READ"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(configuredInput, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result), "/configured should collapse with threshold 2")

	// /unconfigured uses default threshold: 3 children should NOT collapse
	defaultThreshold := dynamicpathdetector.DefaultCollapseConfig.Threshold
	analyzer2 := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
		{Prefix: "/configured", Threshold: 2},
	})
	unconfiguredInput := []types.OpenCalls{
		{Path: "/unconfigured/a/file", Flags: []string{"READ"}},
		{Path: "/unconfigured/b/file", Flags: []string{"READ"}},
		{Path: "/unconfigured/c/file", Flags: []string{"READ"}},
	}
	result2, err := dynamicpathdetector.AnalyzeOpens(unconfiguredInput, analyzer2, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 3, len(result2),
		"/unconfigured should NOT collapse with default threshold %d", defaultThreshold)
}

// TestAnalyzeOpensThreshold1ImmediateWildcard verifies that threshold 1 produces
// a wildcard (*) on the very first additional child.
func TestAnalyzeOpensThreshold1ImmediateWildcard(t *testing.T) {
	appThreshold := configThreshold("/app") // threshold 1
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
		{Prefix: "/instant", Threshold: appThreshold},
	})

	t.Run("single path - no collapse yet", func(t *testing.T) {
		input := []types.OpenCalls{
			{Path: "/instant/only-child/data", Flags: []string{"READ"}},
		}
		result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
		assert.NoError(t, err)
		assert.Equal(t, 1, len(result))
		assert.Equal(t, "/instant/*", result[0].Path, "threshold 1 should wildcard immediately")
	})

	t.Run("two paths - collapsed", func(t *testing.T) {
		analyzer2 := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
			{Prefix: "/instant", Threshold: appThreshold},
		})
		input := []types.OpenCalls{
			{Path: "/instant/first/data", Flags: []string{"READ"}},
			{Path: "/instant/second/data", Flags: []string{"WRITE"}},
		}
		result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer2, mapset.NewSet[string]())
		assert.NoError(t, err)
		assert.Equal(t, 1, len(result))
		assert.Equal(t, "/instant/*", result[0].Path)
		assert.ElementsMatch(t, []string{"READ", "WRITE"}, result[0].Flags)
	})
}

// TestAnalyzeOpensCollapseDoesNotAffectSiblingPrefixes verifies that collapsing
// one prefix does not affect paths under a sibling prefix.
func TestAnalyzeOpensCollapseDoesNotAffectSiblingPrefixes(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	// /alpha: threshold+1 children → should collapse
	var input []types.OpenCalls
	for i := 0; i < threshold+1; i++ {
		input = append(input, types.OpenCalls{
			Path: fmt.Sprintf("/alpha/a%d/file", i), Flags: []string{"READ"},
		})
	}
	// /beta: 2 children → should NOT collapse (2 <= threshold)
	input = append(input,
		types.OpenCalls{Path: "/beta/b1/file", Flags: []string{"WRITE"}},
		types.OpenCalls{Path: "/beta/b2/file", Flags: []string{"WRITE"}},
	)

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	betaPaths := filterByPrefix(result, "/beta/")
	assert.Equal(t, 2, len(betaPaths), "/beta paths should remain individual")

	alphaPaths := filterByPrefix(result, "/alpha/")
	assert.Equal(t, 1, len(alphaPaths), "/alpha paths should collapse to 1")
}

// TestAnalyzeOpensFlagMergingAfterCollapse verifies that flags from all paths
// that collapse into the same dynamic node are properly merged and deduplicated.
func TestAnalyzeOpensFlagMergingAfterCollapse(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	// Generate threshold+1 children to trigger collapse, with varied flags
	var input []types.OpenCalls
	flags := [][]string{{"READ", "WRITE"}, {"WRITE", "APPEND"}, {"READ"}, {"APPEND", "READ"}}
	for i := 0; i < threshold+1; i++ {
		input = append(input, types.OpenCalls{
			Path:  fmt.Sprintf("/logs/service%d/app.log", i),
			Flags: flags[i%len(flags)],
		})
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.ElementsMatch(t, []string{"APPEND", "READ", "WRITE"}, result[0].Flags, "flags should be merged and deduplicated")
	assert.True(t, areStringSlicesUnique(result[0].Flags), "flags must be unique")
}

// TestAnalyzeOpensMultipleLevelsOfCollapse verifies behavior when both parent and
// grandchild segments independently exceed their thresholds.
func TestAnalyzeOpensMultipleLevelsOfCollapse(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	var input []types.OpenCalls
	// threshold+1 unique children under /multi, each with threshold+1 unique grandchildren
	for i := 0; i < threshold+1; i++ {
		for j := 0; j < threshold+1; j++ {
			input = append(input, types.OpenCalls{
				Path:  fmt.Sprintf("/multi/level%d/sub%d/file", i, j),
				Flags: []string{"READ"},
			})
		}
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result), "double collapse should yield a single path")
	assert.True(t,
		strings.Contains(result[0].Path, "\u22ef") || strings.Contains(result[0].Path, "*"),
		"result %q should contain dynamic or wildcard segments", result[0].Path)
}

// TestAnalyzeOpensExistingDynamicSegmentInInput verifies that input paths
// already containing ⋯ are handled correctly and merge with new paths.
func TestAnalyzeOpensExistingDynamicSegmentInInput(t *testing.T) {
	// Use a high threshold so that the two paths alone don't trigger collapse —
	// instead, the existing ⋯ segment absorbs the specific path.
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.OpenDynamicThreshold)
	input := []types.OpenCalls{
		{Path: "/data/\u22ef/config", Flags: []string{"READ"}},
		{Path: "/data/specific/config", Flags: []string{"WRITE"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "/data/\u22ef/config", result[0].Path)
	assert.ElementsMatch(t, []string{"READ", "WRITE"}, result[0].Flags)
}

// TestAnalyzeOpens_NilSbomSetNoError verifies that passing a nil sbomSet
// does not return an error.
func TestAnalyzeOpens_NilSbomSetNoError(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)
	input := []types.OpenCalls{
		{Path: "/usr/lib/libfoo.so", Flags: []string{"READ"}},
		{Path: "/usr/lib/libbar.so", Flags: []string{"READ"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err, "nil sbomSet should not cause an error")
	assert.Equal(t, 2, len(result), "paths below threshold should remain individual")
}

// TestAnalyzeOpens_NilSbomSetWithCollapse verifies that collapse works
// correctly even when sbomSet is nil.
func TestAnalyzeOpens_NilSbomSetWithCollapse(t *testing.T) {
	threshold := configThreshold("/var/run")
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	var input []types.OpenCalls
	for i := 0; i < threshold+1; i++ {
		input = append(input, types.OpenCalls{
			Path:  fmt.Sprintf("/usr/lib/lib%c.so", 'a'+rune(i)),
			Flags: []string{"READ"},
		})
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result), "%d children > threshold %d, should collapse", threshold+1, threshold)
	assert.True(t,
		strings.Contains(result[0].Path, "\u22ef") || strings.Contains(result[0].Path, "*"),
		"collapsed path should contain dynamic or wildcard segment, got %q", result[0].Path)
}

// --- Helpers ---

// generateOpenCallsWithFlags creates N OpenCalls under prefix/userN/filename with rotating flags.
func generateOpenCallsWithFlags(prefix, filename string, n int) []types.OpenCalls {
	allFlags := []string{"READ", "WRITE", "APPEND"}
	var result []types.OpenCalls
	for i := 0; i < n; i++ {
		result = append(result, types.OpenCalls{
			Path:  fmt.Sprintf("%s/user%d/%s", prefix, i, filename),
			Flags: []string{allFlags[i%len(allFlags)]},
		})
	}
	return result
}

// flagsForN returns the sorted, unique flags that generateOpenCallsWithFlags would produce for N items.
func flagsForN(n int) []string {
	allFlags := []string{"READ", "WRITE", "APPEND"}
	seen := map[string]bool{}
	for i := 0; i < n; i++ {
		seen[allFlags[i%len(allFlags)]] = true
	}
	var result []string
	for f := range seen {
		result = append(result, f)
	}
	sort.Strings(result)
	return result
}

func areStringSlicesUnique(slice []string) bool {
	seen := make(map[string]struct{})
	for _, s := range slice {
		if _, exists := seen[s]; exists {
			return false
		}
		seen[s] = struct{}{}
	}
	return true
}

func assertContainsPath(t *testing.T, result []types.OpenCalls, path string) {
	t.Helper()
	for _, r := range result {
		if r.Path == path {
			return
		}
	}
	assert.Fail(t, fmt.Sprintf("result does not contain path %q, got: %v", path, pathsFromResult(result)))
}

func assertContainsOneOfPaths(t *testing.T, result []types.OpenCalls, alternatives ...string) {
	t.Helper()
	for _, r := range result {
		for _, alt := range alternatives {
			if r.Path == alt {
				return
			}
		}
	}
	assert.Fail(t, fmt.Sprintf("result does not contain any of %v, got: %v", alternatives, pathsFromResult(result)))
}

func assertPathIsOneOf(t *testing.T, actual string, alternatives ...string) {
	t.Helper()
	for _, alt := range alternatives {
		if actual == alt {
			return
		}
	}
	assert.Fail(t, fmt.Sprintf("path %q does not match any of %v", actual, alternatives))
}

func filterByPrefix(result []types.OpenCalls, prefix string) []types.OpenCalls {
	var filtered []types.OpenCalls
	for _, r := range result {
		if strings.HasPrefix(r.Path, prefix) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func pathsFromResult(result []types.OpenCalls) []string {
	paths := make([]string, len(result))
	for i, r := range result {
		paths[i] = r.Path
	}
	return paths
}
