package dynamicpathdetectortests

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kinbiko/jsonassert"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
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
					Endpoint: ":80/users/123",
					Methods:  []string{"GET"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: ":80/users/123",
					Methods:  []string{"GET"},
				},
			},
		},
		{
			name: "Test with multiple endpoints",
			input: []types.HTTPEndpoint{
				{
					Endpoint: ":80/users/\u22ef",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: ":80/users/123",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: ":80/users/\u22ef",
					Methods:  []string{"GET", "POST"},
				},
			},
		},
		{
			name: "Test with dynamic segments",
			input: []types.HTTPEndpoint{
				{
					Endpoint: ":80/users/123/posts/\u22ef",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: ":80/users/\u22ef/posts/101",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: ":80/users/\u22ef/posts/\u22ef",
					Methods:  []string{"GET", "POST"},
				},
			},
		},
		{
			name: "Test with different domains",
			input: []types.HTTPEndpoint{
				{
					Endpoint: ":81/users/123",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: ":123/users/456",
					Methods:  []string{"POST"},
				},
				{
					Endpoint: ":123/x/x",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: ":123/x/x",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: ":81/users/123",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: ":123/users/456",
					Methods:  []string{"POST"},
				},
				{
					Endpoint: ":123/x/x",
					Methods:  []string{"GET", "POST"},
				},
			},
		},
		{
			name: "Test with dynamic segments and different headers",
			input: []types.HTTPEndpoint{
				{
					Endpoint: ":80/x/123/posts/\u22ef",
					Methods:  []string{"GET"},
					Headers:  json.RawMessage(`{"Content-Type": ["application/json"], "X-API-Key": ["key1"]}`),
				},
				{
					Endpoint: ":80/x/\u22ef/posts/101",
					Methods:  []string{"POST"},
					Headers:  json.RawMessage(`{"Content-Type": ["application/xml"], "Authorization": ["Bearer token"]}`),
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: ":80/x/\u22ef/posts/\u22ef",
					Methods:  []string{"GET", "POST"},
					Headers:  json.RawMessage(`{"Authorization":["Bearer token"],"Content-Type":["<<UNORDERED>>","application/json","application/xml"],"X-API-Key":["key1"]}`),
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := dynamicpathdetector.AnalyzeEndpoints(&tt.input, analyzer)
			ja := jsonassert.New(t)
			for i := range result {
				assert.Equal(t, tt.expected[i].Endpoint, result[i].Endpoint)
				assert.Equal(t, tt.expected[i].Methods, result[i].Methods)
				ja.Assertf(string(result[i].Headers), string(tt.expected[i].Headers))
			}
		})
	}
}

func TestAnalyzeEndpointsWithThreshold(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	var input []types.HTTPEndpoint
	for i := 0; i < 101; i++ {
		input = append(input, types.HTTPEndpoint{
			Endpoint: fmt.Sprintf(":80/users/%d", i),
			Methods:  []string{"GET"},
		})
	}

	expected := []types.HTTPEndpoint{
		{
			Endpoint: ":80/users/\u22ef",
			Methods:  []string{"GET"},
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	assert.Equal(t, expected, result)
}

func TestAnalyzeEndpointsWithExactThreshold(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	var input []types.HTTPEndpoint
	for i := 0; i < 100; i++ {
		input = append(input, types.HTTPEndpoint{
			Endpoint: fmt.Sprintf(":80/users/%d", i),
			Methods:  []string{"GET"},
		})
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	// Check that all 100 endpoints are still individual
	assert.Equal(t, 100, len(result))

	// Now add one more endpoint to trigger the dynamic behavior
	input = append(input, types.HTTPEndpoint{
		Endpoint: ":80/users/100",
		Methods:  []string{"GET"},
	})

	result = dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	// Check that all endpoints are now merged into one dynamic endpoint
	expected := []types.HTTPEndpoint{
		{
			Endpoint: ":80/users/\u22ef",
			Methods:  []string{"GET"},
		},
	}
	assert.Equal(t, expected, result)
}

func TestAnalyzeEndpointsWithInvalidURL(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	input := []types.HTTPEndpoint{
		{
			Endpoint: ":::invalid-u323@!#rl:::",
			Methods:  []string{"GET"},
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	assert.Equal(t, 0, len(result))
}

func TestAnalyzeEndpointsBug(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(100)

	endpoints := []types.HTTPEndpoint{
		types.HTTPEndpoint{Endpoint: ":8000/", Methods: []string{"GET"}, Internal: false, Direction: "inbound", Headers: json.RawMessage{0x7b, 0x22, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x5b, 0x22, 0x63, 0x6c, 0x6f, 0x73, 0x65, 0x22, 0x5d, 0x2c, 0x22, 0x48, 0x6f, 0x73, 0x74, 0x22, 0x3a, 0x5b, 0x22, 0x31, 0x32, 0x37, 0x2e, 0x30, 0x2e, 0x30, 0x2e, 0x31, 0x3a, 0x38, 0x30, 0x30, 0x30, 0x22, 0x5d, 0x7d}},
		types.HTTPEndpoint{Endpoint: ":8000/", Methods: []string{"GET"}, Internal: false, Direction: "inbound", Headers: json.RawMessage{0x7b, 0x22, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x5b, 0x22, 0x63, 0x6c, 0x6f, 0x73, 0x65, 0x22, 0x5d, 0x2c, 0x22, 0x48, 0x6f, 0x73, 0x74, 0x22, 0x3a, 0x5b, 0x22, 0x31, 0x32, 0x37, 0x2e, 0x30, 0x2e, 0x30, 0x2e, 0x31, 0x3a, 0x38, 0x30, 0x30, 0x30, 0x22, 0x5d, 0x7d}},

		types.HTTPEndpoint{Endpoint: ":8000/", Methods: []string{"GET"}, Internal: false, Direction: "inbound", Headers: json.RawMessage{0x7b, 0x22, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x5b, 0x22, 0x63, 0x6c, 0x6f, 0x73, 0x65, 0x22, 0x5d, 0x2c, 0x22, 0x48, 0x6f, 0x73, 0x74, 0x22, 0x3a, 0x5b, 0x22, 0x31, 0x32, 0x37, 0x2e, 0x30, 0x2e, 0x30, 0x2e, 0x31, 0x3a, 0x38, 0x30, 0x30, 0x30, 0x22, 0x5d, 0x7d}},
	}

	for i := 0; i < 120; i++ {
		e := types.HTTPEndpoint{Endpoint: fmt.Sprintf(":8000/users/%d", i), Methods: []string{"GET"}, Internal: false, Direction: "inbound", Headers: json.RawMessage{0x7b, 0x22, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x5b, 0x22, 0x63, 0x6c, 0x6f, 0x73, 0x65, 0x22, 0x5d, 0x2c, 0x22, 0x48, 0x6f, 0x73, 0x74, 0x22, 0x3a, 0x5b, 0x22, 0x31, 0x32, 0x37, 0x2e, 0x30, 0x2e, 0x30, 0x2e, 0x31, 0x3a, 0x38, 0x30, 0x30, 0x30, 0x22, 0x5d, 0x7d}}
		endpoints = append(endpoints, e)
	}

	fmt.Println(endpoints)

	result := dynamicpathdetector.AnalyzeEndpoints(&endpoints, analyzer)

	endpoints = result
	c := types.HTTPEndpoint{Endpoint: ":8000/", Methods: []string{"POST"}, Internal: false, Direction: "inbound", Headers: json.RawMessage{0x7b, 0x22, 0x43, 0x6f, 0x6e, 0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x22, 0x3a, 0x5b, 0x22, 0x63, 0x6c, 0x6f, 0x73, 0x65, 0x22, 0x5d, 0x2c, 0x22, 0x48, 0x6f, 0x73, 0x74, 0x22, 0x3a, 0x5b, 0x22, 0x31, 0x32, 0x37, 0x2e, 0x30, 0x2e, 0x30, 0x2e, 0x31, 0x3a, 0x38, 0x30, 0x30, 0x30, 0x22, 0x5d, 0x7d}}
	endpoints = append(endpoints, c)
	result = dynamicpathdetector.AnalyzeEndpoints(&endpoints, analyzer)
	fmt.Println(result)

}
