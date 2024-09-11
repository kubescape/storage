package dynamicpathdetectortests

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

func TestNewPathAnalyzer(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)
	if analyzer == nil {
		t.Error("NewPathAnalyzer() returned nil")
	}
}

func TestAnalyzePath(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

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
			if err != nil {
				t.Errorf("AnalyzePath(%q, %q) returned an error: %v", tc.path, tc.identifier, err)
			}
			if result != tc.expected {
				t.Errorf("AnalyzePath(%q, %q) = %q, want %q", tc.path, tc.identifier, result, tc.expected)
			}
		})
	}
}

func TestDynamicSegments(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	// Create 99 different paths under the 'users' segment
	for i := 0; i < 101; i++ {
		path := fmt.Sprintf("/api/users/%d", i)
		_, err := analyzer.AnalyzePath(path, "api")
		if err != nil {
			t.Errorf("AnalyzePath() returned an error: %v", err)
		}
	}

	result, err := analyzer.AnalyzePath("/api/users/101", "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	expected := "/api/users/<dynamic>"
	if result != expected {
		t.Errorf("AnalyzePath(\"/users/101\", \"api\") = %q, want %q", result, expected)
	}

	// Test with one of the original IDs to ensure it's also marked as dynamic
	result, err = analyzer.AnalyzePath("/api/users/50", "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	if result != expected {
		t.Errorf("AnalyzePath(\"/users/50\", \"api\") = %q, want %q", result, expected)
	}
}

func TestMultipleDynamicSegments(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	// Create 99 different paths for both 'users' and 'posts' segments
	for i := 0; i < 110; i++ {
		path := fmt.Sprintf("/api/users/%d/posts/%d", i, i)
		_, err := analyzer.AnalyzePath(path, "api")
		if err != nil {
			t.Errorf("AnalyzePath() returned an error: %v", err)
		}
	}

	// Test with the 100th unique user and post IDs (should trigger dynamic segments)
	result, err := analyzer.AnalyzePath("/api/users/101/posts/1031", "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	expected := "/api/users/<dynamic>/posts/<dynamic>"
	if result != expected {
		t.Errorf("AnalyzePath(\"/users/99/posts/99\", \"api\") = %q, want %q", result, expected)
	}

}

func TestMixedStaticAndDynamicSegments(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	// Create 99 different paths for 'users' but keep 'posts' static
	for i := 0; i < 101; i++ {
		path := fmt.Sprintf("/api/users/%d/posts", i)
		_, err := analyzer.AnalyzePath(path, "api")
		if err != nil {
			t.Errorf("AnalyzePath() returned an error: %v", err)
		}
	}

	// Test with the 100th unique user ID but same 'posts' segment (should trigger dynamic segment for users)
	result, err := analyzer.AnalyzePath("/api/users/99/posts", "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	expected := "/api/users/<dynamic>/posts"
	if result != expected {
		t.Errorf("AnalyzePath(\"/users/99/posts\", \"api\") = %q, want %q", result, expected)
	}

}

func TestDifferentRootIdentifiers(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	// Analyze paths with different root identifiers
	result1, _ := analyzer.AnalyzePath("/api/users/123", "api")
	result2, _ := analyzer.AnalyzePath("/api/products/456", "store")

	if result1 != "/api/users/123" {
		t.Errorf("AnalyzePath(\"/users/123\", \"api\") = %q, want \"/api/users/123\"", result1)
	}

	if result2 != "/api/products/456" {
		t.Errorf("AnalyzePath(\"/products/456\", \"store\") = %q, want \"/store/products/456\"", result2)
	}
}

func TestDynamicThreshold(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	for i := 0; i < 101; i++ {
		path := fmt.Sprintf("/api/users/%d", i)
		result, _ := analyzer.AnalyzePath(path, "api")
		if result != fmt.Sprintf("/api/users/%d", i) {
			t.Errorf("Path became dynamic before reaching 99 different paths")
		}
	}

	result, _ := analyzer.AnalyzePath("/api/users/991", "api")
	if result != "/api/users/<dynamic>" {
		t.Errorf("Path did not become dynamic after 99 different paths")
	}
}

func TestEdgeCases(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

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
			if err != nil {
				t.Errorf("AnalyzePath(%q, %q) returned an error: %v", tc.path, tc.identifier, err)
			}
			if result != tc.expected {
				t.Errorf("AnalyzePath(%q, %q) = %q, want %q", tc.path, tc.identifier, result, tc.expected)
			}
		})
	}
}

func TestDynamicInsertion(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	// Insert a new path with a different identifier
	result, err := analyzer.AnalyzePath("/api/users/<dynamic>", "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	expected := "/api/users/<dynamic>"
	if result != expected {
		t.Errorf("AnalyzePath(\"/api/users/<dynamic>\", \"api\") = %q, want %q", result, expected)
	}

	// Insert a new path with the same identifier
	result, err = analyzer.AnalyzePath("/api/users/102", "api")
	if err != nil {
		t.Errorf("AnalyzePath() returned an error: %v", err)
	}
	expected = "/api/users/<dynamic>"
	if result != expected {
		t.Errorf("AnalyzePath(\"/api/users/<dynamic>\", \"api\") = %q, want %q", result, expected)
	}

}
