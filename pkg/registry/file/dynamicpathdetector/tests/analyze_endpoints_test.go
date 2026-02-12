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
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.EndpointDynamicThreshold)

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
			name: "Test with 0 port",
			input: []types.HTTPEndpoint{
				{
					Endpoint: ":0/users/123/posts/\u22ef",
					Methods:  []string{"GET"},
				},
				{
					Endpoint: ":80/users/\u22ef/posts/101",
					Methods:  []string{"POST"},
				},
				{
					Endpoint: ":8770/users/blub/posts/101",
					Methods:  []string{"POST"},
				},
			},
			expected: []types.HTTPEndpoint{
				{
					Endpoint: ":0/users/\u22ef/posts/\u22ef",
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
				ja.Assert(string(result[i].Headers), string(tt.expected[i].Headers))
			}
		})
	}
}

func TestAnalyzeEndpointsWithThreshold(t *testing.T) {
	threshold := dynamicpathdetector.EndpointDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	var input []types.HTTPEndpoint
	for i := 0; i < threshold+1; i++ {
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
	threshold := dynamicpathdetector.EndpointDynamicThreshold
	analyzer := dynamicpathdetector.NewPathAnalyzer(threshold)

	var input []types.HTTPEndpoint
	for i := 0; i < threshold; i++ {
		input = append(input, types.HTTPEndpoint{
			Endpoint: fmt.Sprintf(":80/users/%d", i),
			Methods:  []string{"GET"},
		})
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	// At exact threshold: all endpoints should remain individual
	assert.Equal(t, threshold, len(result))

	// Now add one more endpoint to trigger the dynamic behavior
	input = append(input, types.HTTPEndpoint{
		Endpoint: fmt.Sprintf(":80/users/%d", threshold),
		Methods:  []string{"GET"},
	})

	result = dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	expected := []types.HTTPEndpoint{
		{
			Endpoint: ":80/users/\u22ef",
			Methods:  []string{"GET"},
		},
	}
	assert.Equal(t, expected, result)
}

func TestAnalyzeEndpointsWithInvalidURL(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.EndpointDynamicThreshold)

	input := []types.HTTPEndpoint{
		{
			Endpoint: ":::invalid-u323@!#rl:::",
			Methods:  []string{"GET"},
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	assert.Equal(t, 0, len(result))
}

func TestAnalyzeEndpointsWildcardPortAbsorbsSpecificPort(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.EndpointDynamicThreshold)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":0/users/123",
			Methods:   []string{"GET"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":80/users/456",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	for _, ep := range result {
		port := ep.Endpoint[:len(":0")]
		assert.Equal(t, ":0", port, "endpoint %s should have wildcard port", ep.Endpoint)
	}
}

func TestAnalyzeEndpointsWildcardPortAfterSpecificPorts(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.EndpointDynamicThreshold)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":80/api/data",
			Methods:   []string{"GET"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":0/api/info",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	for _, ep := range result {
		port := ep.Endpoint[:len(":0")]
		assert.Equal(t, ":0", port, "endpoint %s should have wildcard port", ep.Endpoint)
	}
}

func TestAnalyzeEndpointsMultiplePortsMergeIntoWildcard(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzer(dynamicpathdetector.EndpointDynamicThreshold)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":0/api/data",
			Methods:   []string{"GET"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":80/api/data",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":81/api/data",
			Methods:   []string{"PUT"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	assert.Equal(t, 1, len(result))
	assert.Equal(t, ":0/api/data", result[0].Endpoint)
	assert.Equal(t, []string{"GET", "POST", "PUT"}, result[0].Methods)
}

func TestMergeDuplicateEndpointsWildcardPort(t *testing.T) {
	wildcardEP := &types.HTTPEndpoint{
		Endpoint:  ":0/api/data",
		Methods:   []string{"GET"},
		Direction: "outbound",
	}
	specificEP := &types.HTTPEndpoint{
		Endpoint:  ":80/api/data",
		Methods:   []string{"POST"},
		Direction: "outbound",
	}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{wildcardEP, specificEP})

	assert.Equal(t, 1, len(result))
	assert.Equal(t, ":0/api/data", result[0].Endpoint)
	assert.Equal(t, []string{"GET", "POST"}, result[0].Methods)
}
