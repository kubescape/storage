package dynamicpathdetectortests

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
)

// configThreshold returns the collapse threshold for the given path prefix
// from DefaultCollapseConfigs. Falls back to DefaultCollapseConfig.Threshold.
func configThreshold(prefix string) int {
	for _, cfg := range dynamicpathdetector.DefaultCollapseConfigs {
		if cfg.Prefix == prefix {
			return cfg.Threshold
		}
	}
	return dynamicpathdetector.DefaultCollapseConfig.Threshold
}

func TestNewPathAnalyzer(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.OpenDynamicThreshold)
	if analyzer == nil {
		t.Error("NewPathAnalyzer() returned nil")
	}
}

func TestAnalyzePath(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.OpenDynamicThreshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.OpenDynamicThreshold)

	result1, _ := analyzer.AnalyzePath("/api/users/123", "api")
	result2, _ := analyzer.AnalyzePath("/api/products/456", "store")

	assert.Equal(t, "/api/users/123", result1)
	assert.Equal(t, "/api/products/456", result2)
}

func TestDynamicThreshold(t *testing.T) {
	threshold := dynamicpathdetector.OpenDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.OpenDynamicThreshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.OpenDynamicThreshold)

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
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)
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
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs([]dynamicpathdetector.CollapseConfig{
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
