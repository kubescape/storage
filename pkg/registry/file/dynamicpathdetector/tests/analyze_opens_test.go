package dynamicpathdetectortests

import (
	"fmt"
	"strings"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
)

func TestAnalyzeOpensWithThreshold(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	var input []types.OpenCalls
	for i := 0; i < 101; i++ {
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
	tests := []struct {
		name     string
		input    []types.OpenCalls
		expected []types.OpenCalls
	}{
		{
			name: "Merge flags for paths exceeding threshold",
			input: []types.OpenCalls{
				{Path: "/home/user1/file.txt", Flags: []string{"READ"}},
				{Path: "/home/user2/file.txt", Flags: []string{"WRITE"}},
				{Path: "/home/user3/file.txt", Flags: []string{"APPEND"}},
				{Path: "/home/user4/file.txt", Flags: []string{"READ", "WRITE"}},
			},
			expected: []types.OpenCalls{
				{Path: "/home/\u22ef/file.txt", Flags: []string{"APPEND", "READ", "WRITE"}},
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
			input: []types.OpenCalls{
				{Path: "/home/user1/common.txt", Flags: []string{"READ"}},
				{Path: "/home/user2/common.txt", Flags: []string{"WRITE"}},
				{Path: "/home/user3/common.txt", Flags: []string{"APPEND"}},
				{Path: "/home/user4/common.txt", Flags: []string{"READ", "WRITE"}},
				{Path: "/var/log/app1.log", Flags: []string{"READ"}},
				{Path: "/var/log/app2.log", Flags: []string{"WRITE"}},
			},
			expected: []types.OpenCalls{
				{Path: "/home/\u22ef/common.txt", Flags: []string{"APPEND", "READ", "WRITE"}},
				{Path: "/var/log/app1.log", Flags: []string{"READ"}},
				{Path: "/var/log/app2.log", Flags: []string{"WRITE"}},
			},
		},
		{
			name: "Multiple dynamic segments",
			input: []types.OpenCalls{
				{Path: "/home/user1/file1.txt", Flags: []string{"READ"}},
				{Path: "/home/user2/file1.txt", Flags: []string{"WRITE"}},
				{Path: "/home/user3/file1.txt", Flags: []string{"APPEND"}},
				{Path: "/home/user4/file1.txt", Flags: []string{"READ", "WRITE"}},
				{Path: "/home/user1/file2.txt", Flags: []string{"READ"}},
				{Path: "/home/user2/file2.txt", Flags: []string{"WRITE"}},
				{Path: "/home/user3/file2.txt", Flags: []string{"APPEND"}},
				{Path: "/home/user4/file2.txt", Flags: []string{"READ", "WRITE"}},
			},
			expected: []types.OpenCalls{
				{Path: "/home/\u22ef/file1.txt", Flags: []string{"APPEND", "READ", "WRITE"}},
				{Path: "/home/\u22ef/file2.txt", Flags: []string{"APPEND", "READ", "WRITE"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := dynamicpathdetector.NewPathAnalyzer(3)
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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3) // Threshold of 3 OLD BEHAVIOR

	input := []types.OpenCalls{
		// These should collapse into /home/…/file.txt
		{Path: "/home/user1/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user2/file.txt", Flags: []string{"READ"}},
		{Path: "/home/\u22ef/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user4/file.txt", Flags: []string{"READ"}},
	}

	expected := []types.OpenCalls{
		{Path: "/home/\u22ef/file.txt", Flags: []string{"READ"}},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	// Use ElementsMatch because the order of elements in the result is not guaranteed
	assert.ElementsMatch(t, expected, result)
}

func TestAnalyzeOpensWithMultiCollapse(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(5) // Threshold of 3 for /var/run prefix is set in the defaults, but here we are overwriting the defaults

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
	// Default threshold is 10, used for paths like /tmp
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
		{
			Prefix:    "/etc",
			Threshold: 50,
		},
		{
			Prefix:    "/opt",
			Threshold: 5,
		},
		{
			Prefix:    "/var/run",
			Threshold: 3,
		},
		{
			Prefix:    "/app",
			Threshold: 1,
		},
		{
			Prefix:    "/tmp",
			Threshold: 10,
		},
	})

	// The paths to be added, exercising different collapse configurations.
	pathsToAdd := []string{
		// /etc paths (Threshold: 50) - should not collapse
		"/etc/config/app.conf",
		"/etc/config/db.conf",
		"/etc/hosts",
		"/etc/resolv.conf",
		"/etc/config/cron.d/hourly",
		"/etc/systemd/system.conf",
		"/etc/hostname",
		"/etc/config/something",

		// /opt paths (Threshold: 5) - should collapse at /opt level
		"/opt/app1/binary",
		"/opt/app2/binary",
		"/opt/app3/binary",
		"/opt/app4/binary",
		"/opt/app5/binary",
		"/opt/app6/binary", // 6th child of /opt, triggers collapse

		// /var/run paths (Threshold: 3) - should collapse at /var/run level
		"/var/run/pid1.pid",
		"/var/run/pid2.pid",
		"/var/run/pid3.pid",
		"/var/run/pid4.pid", // 4th child of /var/run, triggers collapse

		// /app paths (Threshold: 1) - should immediately collapse
		"/app/some/deep/path",
		"/app/another/path", // 2nd child of /app, triggers collapse

		// /tmp paths (Default Threshold: 10) - should collapse at /tmp level
		"/tmp/user1/a",
		"/tmp/user2/a",
		"/tmp/user3/a",
		"/tmp/user4/a",
		"/tmp/user5/a",
		"/tmp/user6/a",
		"/tmp/user7/a",
		"/tmp/user8/a",
		"/tmp/user9/a",
		"/tmp/user10/a",
		"/tmp/user11/a", // 11th child of /tmp, triggers collapse
	}

	var input []types.OpenCalls
	for _, p := range pathsToAdd {
		input = append(input, types.OpenCalls{Path: p, Flags: []string{"READ"}})
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	// /etc paths (threshold 50) should NOT be collapsed - all 8 paths remain individual
	assertContainsPath(t, result, "/etc/config/app.conf")
	assertContainsPath(t, result, "/etc/config/cron.d/hourly")
	assertContainsPath(t, result, "/etc/config/db.conf")
	assertContainsPath(t, result, "/etc/config/something")
	assertContainsPath(t, result, "/etc/hostname")
	assertContainsPath(t, result, "/etc/hosts")
	assertContainsPath(t, result, "/etc/resolv.conf")
	assertContainsPath(t, result, "/etc/systemd/system.conf")

	// /app (threshold 1) - immediately collapses to wildcard
	assertContainsPath(t, result, "/app/*")

	// /opt (threshold 5) - collapses; both wildcard and dynamic-with-subtree are acceptable
	assertContainsOneOfPaths(t, result, "/opt/*", "/opt/\u22ef/binary")

	// /tmp (threshold 10) - collapses; both wildcard and dynamic-with-subtree are acceptable
	assertContainsOneOfPaths(t, result, "/tmp/*", "/tmp/\u22ef/a")

	// /var/run (threshold 3) - collapses; both forms are equivalent here (leaf nodes)
	assertContainsOneOfPaths(t, result, "/var/run/*", "/var/run/\u22ef")

	// Total: 8 etc + 1 app + 1 opt + 1 tmp + 1 var/run = 12
	assert.Equal(t, 12, len(result), "expected 12 total paths, got %d: %v", len(result), pathsFromResult(result))
}

// TestAnalyzeOpensCollapseExactBoundary verifies that threshold is strictly "greater than",
// not "greater than or equal". With threshold N, exactly N children should NOT collapse,
// but N+1 children SHOULD.
func TestAnalyzeOpensCollapseExactBoundary(t *testing.T) {
	t.Run("at threshold - no collapse", func(t *testing.T) {
		analyzer := dynamicpathdetector.NewPathAnalyzer(5)
		var input []types.OpenCalls
		for i := 0; i < 5; i++ {
			input = append(input, types.OpenCalls{
				Path:  fmt.Sprintf("/data/item%d/info", i),
				Flags: []string{"READ"},
			})
		}
		result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
		assert.NoError(t, err)
		assert.Equal(t, 5, len(result), "at exact threshold, paths should NOT collapse")
		for _, r := range result {
			assert.NotContains(t, r.Path, "\u22ef", "no dynamic segment expected")
			assert.NotContains(t, r.Path, "*", "no wildcard expected")
		}
	})

	t.Run("above threshold - collapse", func(t *testing.T) {
		analyzer := dynamicpathdetector.NewPathAnalyzer(5)
		var input []types.OpenCalls
		for i := 0; i < 6; i++ {
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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3)
	var input []types.OpenCalls
	for i := 0; i < 100; i++ {
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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3)
	input := []types.OpenCalls{
		{Path: "/data/a", Flags: []string{"READ"}},
		{Path: "/data/b/deep/file", Flags: []string{"READ"}},
		{Path: "/data/c/other", Flags: []string{"WRITE"}},
		{Path: "/data/d", Flags: []string{"APPEND"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	// 4 unique children under /data with threshold 3 -> should collapse
	// All paths should be merged under the dynamic/wildcard node
	for _, r := range result {
		assert.True(t,
			strings.Contains(r.Path, "\u22ef") || strings.Contains(r.Path, "*"),
			"path %q should contain a dynamic or wildcard segment after collapse", r.Path)
	}
}

// TestAnalyzeOpensNewPathAfterCollapse verifies that a new path arriving after
// the threshold was already crossed gets absorbed by the collapsed node.
func TestAnalyzeOpensNewPathAfterCollapse(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(3)

	// First batch: trigger collapse
	batch1 := []types.OpenCalls{
		{Path: "/srv/a/log", Flags: []string{"READ"}},
		{Path: "/srv/b/log", Flags: []string{"READ"}},
		{Path: "/srv/c/log", Flags: []string{"READ"}},
		{Path: "/srv/d/log", Flags: []string{"READ"}},
	}
	result1, err := dynamicpathdetector.AnalyzeOpens(batch1, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result1), "first batch should collapse to 1 path")

	// Second batch: add a completely new child - it should be absorbed
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

	// /unconfigured uses default threshold (50): 3 children should NOT collapse
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
	assert.Equal(t, 3, len(result2), "/unconfigured should NOT collapse with default threshold 50")
}

// TestAnalyzeOpensThreshold1ImmediateWildcard verifies that threshold 1 produces
// a wildcard (*) on the very first additional child.
func TestAnalyzeOpensThreshold1ImmediateWildcard(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
		{Prefix: "/instant", Threshold: 1},
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
			{Prefix: "/instant", Threshold: 1},
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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3)

	input := []types.OpenCalls{
		// /alpha should collapse (4 > 3)
		{Path: "/alpha/a1/file", Flags: []string{"READ"}},
		{Path: "/alpha/a2/file", Flags: []string{"READ"}},
		{Path: "/alpha/a3/file", Flags: []string{"READ"}},
		{Path: "/alpha/a4/file", Flags: []string{"READ"}},
		// /beta should NOT collapse (2 <= 3)
		{Path: "/beta/b1/file", Flags: []string{"WRITE"}},
		{Path: "/beta/b2/file", Flags: []string{"WRITE"}},
	}

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3)
	input := []types.OpenCalls{
		{Path: "/logs/service1/app.log", Flags: []string{"READ", "WRITE"}},
		{Path: "/logs/service2/app.log", Flags: []string{"WRITE", "APPEND"}},
		{Path: "/logs/service3/app.log", Flags: []string{"READ"}},
		{Path: "/logs/service4/app.log", Flags: []string{"APPEND", "READ"}},
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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3)

	var input []types.OpenCalls
	// 4 unique children under /multi, each with 4 unique grandchildren
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			input = append(input, types.OpenCalls{
				Path:  fmt.Sprintf("/multi/level%d/sub%d/file", i, j),
				Flags: []string{"READ"},
			})
		}
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	// Both /multi children and the grandchildren should collapse
	assert.Equal(t, 1, len(result), "double collapse should yield a single path")
	// The path should contain wildcard or dynamic segments
	assert.True(t,
		strings.Contains(result[0].Path, "\u22ef") || strings.Contains(result[0].Path, "*"),
		"result %q should contain dynamic or wildcard segments", result[0].Path)
}

// TestAnalyzeOpensExistingDynamicSegmentInInput verifies that input paths
// already containing ⋯ are handled correctly and merge with new paths.
func TestAnalyzeOpensExistingDynamicSegmentInInput(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)
	input := []types.OpenCalls{
		{Path: "/data/\u22ef/config", Flags: []string{"READ"}},
		{Path: "/data/specific/config", Flags: []string{"WRITE"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)
	// The specific path should be absorbed by the existing dynamic segment
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "/data/\u22ef/config", result[0].Path)
	assert.ElementsMatch(t, []string{"READ", "WRITE"}, result[0].Flags)
}

// Helper function to check if a slice of strings contains only unique elements
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

// assertContainsPath checks that at least one result has the given path.
func assertContainsPath(t *testing.T, result []types.OpenCalls, path string) {
	t.Helper()
	for _, r := range result {
		if r.Path == path {
			return
		}
	}
	assert.Fail(t, fmt.Sprintf("result does not contain path %q, got: %v", path, pathsFromResult(result)))
}

// assertContainsOneOfPaths checks that at least one result matches any of the given paths.
// Used when both the dynamic (⋯) and wildcard (*) forms are acceptable.
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

// assertPathIsOneOf checks that the given path matches one of the alternatives.
func assertPathIsOneOf(t *testing.T, actual string, alternatives ...string) {
	t.Helper()
	for _, alt := range alternatives {
		if actual == alt {
			return
		}
	}
	assert.Fail(t, fmt.Sprintf("path %q does not match any of %v", actual, alternatives))
}

// filterByPrefix returns all OpenCalls whose path starts with the given prefix.
func filterByPrefix(result []types.OpenCalls, prefix string) []types.OpenCalls {
	var filtered []types.OpenCalls
	for _, r := range result {
		if strings.HasPrefix(r.Path, prefix) {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

// pathsFromResult extracts just the paths for readable error messages.
func pathsFromResult(result []types.OpenCalls) []string {
	paths := make([]string, len(result))
	for i, r := range result {
		paths[i] = r.Path
	}
	return paths
}
