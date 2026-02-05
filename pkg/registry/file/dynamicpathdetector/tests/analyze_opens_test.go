package dynamicpathdetectortests

import (
	"fmt"
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

func TestAnalyzeOpensWithThresholdAndExclusion(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	var input []types.OpenCalls
	for i := 0; i < 101; i++ {
		input = append(input, types.OpenCalls{
			Path:  fmt.Sprintf("/home/user%d/file.txt", i),
			Flags: []string{"READ"},
		})
	}

	expected := []types.OpenCalls{
		{
			Path:  "/home/user42/file.txt",
			Flags: []string{"READ"},
		},
		{
			Path:  "/home/\u22ef/file.txt",
			Flags: []string{"READ"},
		},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]("/home/user42/file.txt"))
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
	analyzer := dynamicpathdetector.NewPathAnalyzer(3) // Threshold of 3

	input := []types.OpenCalls{
		// These should collapse into /home/…/file.txt
		{Path: "/home/user1/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user2/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user3/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user4/file.txt", Flags: []string{"READ"}},
		// This path with an asterisk should be treated as a literal path and not interfere as it has a different FLAG
		{Path: "/home/user*/file.txt", Flags: []string{"WRITE"}},
	}

	expected := []types.OpenCalls{
		{Path: "/home/user*/file.txt", Flags: []string{"WRITE"}},
		{Path: "/home/\u22ef/file.txt", Flags: []string{"READ"}},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	// Use ElementsMatch because the order of elements in the result is not guaranteed
	assert.ElementsMatch(t, expected, result)
}

func TestAnalyzeOpensWithAsteriskAndEllipsisNotCollapse(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(3) // Threshold of 3

	input := []types.OpenCalls{
		// These should collapse into /home/…/file.txt
		{Path: "/home/user1/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user2/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user3/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user4/file.txt", Flags: []string{"READ"}},
		// This path with an asterisk must not be collapsed, as it has a different meaning
		{Path: "/home/user*/file.txt", Flags: []string{"READ"}},
	}

	expected := []types.OpenCalls{
		{Path: "/home/\u22ef/file.txt", Flags: []string{"READ"}},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	assert.ElementsMatch(t, expected, result)
}

func TestAnalyzeOpensWithMultiCollapse(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(3) // Threshold of 3

	input := []types.OpenCalls{
		// These should collapse into /home/*/file.txt  and that may not be great, but lets first check if it actually does it
		{Path: "/home/user1/txt/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user2/tmp/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user3/blu/file.txt", Flags: []string{"READ"}},
		{Path: "/home/user4/brr/file.txt", Flags: []string{"READ"}},
	}

	expected := []types.OpenCalls{
		{Path: "/home/*/file.txt", Flags: []string{"READ"}},
	}

	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, mapset.NewSet[string]())
	assert.NoError(t, err)

	assert.ElementsMatch(t, expected, result)
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
