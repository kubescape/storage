package dynamicpathdetectortests

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

func TestAnalyzeEndpoints(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	tests := []struct {
		name     string
		input    []types.HTTPEndpoint
		expected []types.HTTPEndpoint
	}{
		{
			name: "Basic test with single endpoint",
			input: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/users/123",
					Methods:  []string{"GET"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/users/123",
					Methods:  []string{"GET"},
				},
			},
		},
		{
			name: "Test with multiple endpoints",
			input: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/users/<dynamic>",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: "api.example.com/users/123",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/users/<dynamic>",
					Methods:  []string{"GET", "POST"},
				},
			},
		},
		{
			name: "Test with dynamic segments",
			input: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/users/123/posts/<dynamic>",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: "api.example.com/users/<dynamic>/posts/101",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/users/<dynamic>/posts/<dynamic>",
					Methods:  []string{"GET", "POST"},
				},
			},
		},
		{
			name: "Test with different domains",
			input: []types.HTTPEndpoint{
				{
					Endpoint: "api1.example.com/users/123",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: "api2.example.com/users/456",
					Methods:  []string{"POST"},
				},
				{
					Endpoint: "api2.example.com/x/x",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: "api2.example.com/x/x",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: "api1.example.com/users/123",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: "api2.example.com/users/456",
					Methods:  []string{"POST"},
				},
				{
					Endpoint: "api2.example.com/x/x",
					Methods:  []string{"GET", "POST"},
				},
			},
		},
		{
			name: "Test with dynamic segments and different headers",
			input: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/x/123/posts/<dynamic>",
					Methods:  []string{"GET"},
					Headers:  json.RawMessage(`{"Content-Type": ["application/json"], "X-API-Key": ["key1"]}`),
				},
				{
					Endpoint: "api.example.com/x/<dynamic>/posts/101",
					Methods:  []string{"POST"},
					Headers:  json.RawMessage(`{"Content-Type": ["application/xml"], "Authorization": ["Bearer token"]}`),
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: "api.example.com/x/<dynamic>/posts/<dynamic>",
					Methods:  []string{"GET", "POST"},
					Headers:  json.RawMessage([]byte{123, 34, 65, 117, 116, 104, 111, 114, 105, 122, 97, 116, 105, 111, 110, 34, 58, 91, 34, 66, 101, 97, 114, 101, 114, 32, 116, 111, 107, 101, 110, 34, 93, 44, 34, 67, 111, 110, 116, 101, 110, 116, 45, 84, 121, 112, 101, 34, 58, 91, 34, 97, 112, 112, 108, 105, 99, 97, 116, 105, 111, 110, 47, 106, 115, 111, 110, 34, 44, 34, 97, 112, 112, 108, 105, 99, 97, 116, 105, 111, 110, 47, 120, 109, 108, 34, 93, 44, 34, 88, 45, 65, 80, 73, 45, 75, 101, 121, 34, 58, 91, 34, 107, 101, 121, 49, 34, 93, 125}),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := dynamicpathdetector.AnalyzeEndpoints(&tt.input, analyzer)
			if err != nil {
				t.Errorf("AnalyzeEndpoints() error = %v", err)
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("AnalyzeEndpoints() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestAnalyzeEndpointsWithThreshold(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	var input []types.HTTPEndpoint
	for i := 0; i < 101; i++ {
		input = append(input, types.HTTPEndpoint{
			Endpoint: fmt.Sprintf("api.example.com/users/%d", i),
			Methods:  []string{"GET"},
		})
	}

	expected := []types.HTTPEndpoint{
		{
			Endpoint: "api.example.com/users/<dynamic>",
			Methods:  []string{"GET"},
		},
	}

	result, err := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	if err != nil {
		t.Errorf("AnalyzeEndpoints() error = %v", err)
		return
	}
	if !reflect.DeepEqual(result, expected) {
		t.Errorf("AnalyzeEndpoints() = %v, want %v", result, expected)
	}
}

func TestAnalyzeEndpointsWithExactThreshold(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	var input []types.HTTPEndpoint
	for i := 0; i < 100; i++ {
		input = append(input, types.HTTPEndpoint{
			Endpoint: fmt.Sprintf("api.example.com/users/%d", i),
			Methods:  []string{"GET"},
		})
	}

	result, err := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	if err != nil {
		t.Errorf("AnalyzeEndpoints() error = %v", err)
		return
	}

	// Check that all 100 endpoints are still individual
	if len(result) != 100 {
		t.Errorf("Expected 100 individual endpoints, got %d", len(result))
	}

	// Now add one more endpoint to trigger the dynamic behavior
	input = append(input, types.HTTPEndpoint{
		Endpoint: "api.example.com/users/100",
		Methods:  []string{"GET"},
	})

	result, err = dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	if err != nil {
		t.Errorf("AnalyzeEndpoints() error = %v", err)
		return
	}

	// Check that all endpoints are now merged into one dynamic endpoint
	expected := []types.HTTPEndpoint{
		{
			Endpoint: "api.example.com/users/<dynamic>",
			Methods:  []string{"GET"},
		},
	}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("AnalyzeEndpoints() = %v, want %v", result, expected)
	}
}

func TestAnalyzeEndpointsWithInvalidURL(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	input := []types.HTTPEndpoint{
		{
			Endpoint: ":::invalid-u323@!#rl:::",
			Methods:  []string{"GET"},
		},
	}

	result, _ := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	if len(result) != 0 {
		t.Errorf("Expected empty result, got %v", result)
	}
}
