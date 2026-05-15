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
			// A single :0 (wildcard-port) entry MUST NOT contaminate
			// unrelated concrete-port endpoints. Only same-(path, direction)
			// siblings of an explicit :0 entry are folded into it; here the
			// :80 and :8770 paths are distinct from the :0 path, so each
			// endpoint stays on its own port. Regression test for the bug
			// flagged in upstream review on kubescape/storage#316.
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
			analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)
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
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)

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
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(threshold, nil)

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
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)

	input := []types.HTTPEndpoint{
		{
			Endpoint: ":::invalid-u323@!#rl:::",
			Methods:  []string{"GET"},
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)
	assert.Equal(t, 0, len(result))
}

// TestAnalyzeEndpoints_WildcardDoesNotContaminateUnrelatedPaths pins the bug
// flagged by upstream review on kubescape/storage#316: a single wildcard-port
// endpoint must NOT cause unrelated specific-port endpoints (different path)
// to be rewritten to :0. Only same-path siblings should fold into the wildcard.
func TestAnalyzeEndpoints_WildcardDoesNotContaminateUnrelatedPaths(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":0/health",
			Methods:   []string{"GET"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":443/login",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	endpoints := make(map[string]bool, len(result))
	for _, ep := range result {
		endpoints[ep.Endpoint] = true
	}
	assert.Equal(t, 2, len(result), "unrelated paths must not be merged: got %v", endpoints)
	assert.True(t, endpoints[":0/health"], "wildcard endpoint :0/health must be preserved")
	assert.True(t, endpoints[":443/login"], "specific-port endpoint :443/login must keep its port (no wildcard sibling on the same path)")
}

// TestAnalyzeEndpoints_SamePathSpecificFirstThenWildcard exercises the
// reverse-order case: the specific-port endpoint comes first in the slice,
// then a wildcard sibling on the SAME path. The two must merge into the
// wildcard. Without symmetric merging in MergeDuplicateEndpoints, the
// specific endpoint sticks around alongside the wildcard.
func TestAnalyzeEndpoints_SamePathSpecificFirstThenWildcard(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":443/login",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":0/login",
			Methods:   []string{"GET"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	assert.Equal(t, 1, len(result), "specific-port sibling must fold into the wildcard regardless of order")
	assert.Equal(t, ":0/login", result[0].Endpoint)
	assert.ElementsMatch(t, []string{"GET", "POST"}, result[0].Methods, "methods from both ports must be merged")
}

// TestAnalyzeEndpoints_NoWildcardKeepsSpecificPort asserts that without ANY
// wildcard sibling, specific-port endpoints stay specific. A regression here
// would mean the analyzer is wildcarding too aggressively.
func TestAnalyzeEndpoints_NoWildcardKeepsSpecificPort(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":443/login",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	assert.Equal(t, 1, len(result))
	assert.Equal(t, ":443/login", result[0].Endpoint, "no wildcard sibling => port must be preserved")
}

// TestAnalyzeEndpoints_OnlyMatchingPathsFoldIntoWildcard combines the
// wildcard-contamination case with the same-path-merge case to verify both
// invariants hold simultaneously. :0/api absorbs :80/api (same path); but
// :443/admin (different path, no wildcard sibling) keeps its port.
func TestAnalyzeEndpoints_OnlyMatchingPathsFoldIntoWildcard(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)

	input := []types.HTTPEndpoint{
		{
			Endpoint:  ":0/api",
			Methods:   []string{"GET"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":80/api",
			Methods:   []string{"POST"},
			Direction: "outbound",
		},
		{
			Endpoint:  ":443/admin",
			Methods:   []string{"DELETE"},
			Direction: "outbound",
		},
	}

	result := dynamicpathdetector.AnalyzeEndpoints(&input, analyzer)

	endpoints := make(map[string][]string, len(result))
	for _, ep := range result {
		endpoints[ep.Endpoint] = ep.Methods
	}

	assert.Equal(t, 2, len(result), "expected :0/api and :443/admin, got %v", endpoints)
	assert.ElementsMatch(t, []string{"GET", "POST"}, endpoints[":0/api"], "/api siblings must merge into wildcard")
	assert.ElementsMatch(t, []string{"DELETE"}, endpoints[":443/admin"], ":443/admin must NOT be wildcarded — no wildcard sibling on /admin")
}

func TestAnalyzeEndpointsMultiplePortsMergeIntoWildcard(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(dynamicpathdetector.EndpointDynamicThreshold, nil)

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

// TestMergeDuplicateEndpoints_SpecificFirstThenWildcard pins the reverse
// order — specific-port endpoint encountered first, wildcard sibling second.
// Without symmetric merging in MergeDuplicateEndpoints both entries survive,
// which CodeRabbit flagged on PR #316. Locking the contract here.
func TestMergeDuplicateEndpoints_SpecificFirstThenWildcard(t *testing.T) {
	specificEP := &types.HTTPEndpoint{
		Endpoint:  ":80/api/data",
		Methods:   []string{"POST"},
		Direction: "outbound",
	}
	wildcardEP := &types.HTTPEndpoint{
		Endpoint:  ":0/api/data",
		Methods:   []string{"GET"},
		Direction: "outbound",
	}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{specificEP, wildcardEP})

	assert.Equal(t, 1, len(result), "wildcard sibling must absorb the earlier specific-port entry")
	assert.Equal(t, ":0/api/data", result[0].Endpoint)
	assert.ElementsMatch(t, []string{"GET", "POST"}, result[0].Methods)
}

// TestMergeDuplicateEndpoints_NoWildcardKeepsAllSpecificPorts asserts that
// without a wildcard sibling, distinct (port,path) pairs all survive.
// A regression here would mean the merge is collapsing too aggressively.
func TestMergeDuplicateEndpoints_NoWildcardKeepsAllSpecificPorts(t *testing.T) {
	a := &types.HTTPEndpoint{Endpoint: ":80/api/data", Methods: []string{"GET"}, Direction: "outbound"}
	b := &types.HTTPEndpoint{Endpoint: ":443/api/data", Methods: []string{"POST"}, Direction: "outbound"}
	c := &types.HTTPEndpoint{Endpoint: ":8080/api/data", Methods: []string{"PUT"}, Direction: "outbound"}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{a, b, c})

	assert.Equal(t, 3, len(result), "no wildcard sibling => all specific-port endpoints must be kept")
}

// ---------------------------------------------------------------------------
// Internal-field isolation tests.
//
// `HTTPEndpoint.Equal` distinguishes endpoints by (Endpoint, Direction,
// Internal). The merge key in `MergeDuplicateEndpoints` and the wildcard
// sweep must therefore also distinguish Internal — otherwise an
// internally-originating endpoint can absorb an externally-originating one
// (or vice versa) just because they share path + direction.
//
// Flagged by upstream review on kubescape/storage#316 (matthyx).
// ---------------------------------------------------------------------------

// TestMergeDuplicateEndpoints_InternalFieldDistinguishesDuplicates asserts
// that two endpoints differing ONLY in Internal are NOT collapsed by the
// duplicate-key check at the top of the merge loop.
func TestMergeDuplicateEndpoints_InternalFieldDistinguishesDuplicates(t *testing.T) {
	external := &types.HTTPEndpoint{
		Endpoint:  ":443/login",
		Methods:   []string{"POST"},
		Direction: "outbound",
		Internal:  false,
	}
	internal := &types.HTTPEndpoint{
		Endpoint:  ":443/login",
		Methods:   []string{"GET"},
		Direction: "outbound",
		Internal:  true,
	}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{external, internal})

	assert.Equal(t, 2, len(result),
		"endpoints with different Internal must NOT merge (HTTPEndpoint.Equal distinguishes Internal)")
	// Each output must keep its own Internal value and methods.
	for _, ep := range result {
		switch ep.Internal {
		case false:
			assert.Equal(t, []string{"POST"}, ep.Methods, "external endpoint methods must not be polluted")
		case true:
			assert.Equal(t, []string{"GET"}, ep.Methods, "internal endpoint methods must not be polluted")
		}
	}
}

// TestMergeDuplicateEndpoints_InternalFieldGuardsWildcardAbsorbingPrior
// pins the wildcard-after-specific path: an Internal=false :0 wildcard must
// NOT sweep up a previously-recorded Internal=true specific-port sibling.
func TestMergeDuplicateEndpoints_InternalFieldGuardsWildcardAbsorbingPrior(t *testing.T) {
	specificInternal := &types.HTTPEndpoint{
		Endpoint:  ":443/login",
		Methods:   []string{"POST"},
		Direction: "outbound",
		Internal:  true,
	}
	wildcardExternal := &types.HTTPEndpoint{
		Endpoint:  ":0/login",
		Methods:   []string{"GET"},
		Direction: "outbound",
		Internal:  false,
	}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{specificInternal, wildcardExternal})

	assert.Equal(t, 2, len(result),
		"wildcard with Internal=false must NOT absorb a specific-port sibling with Internal=true")
}

// TestMergeDuplicateEndpoints_InternalFieldGuardsSpecificFoldingIntoWildcard
// pins the wildcard-first path: a previously-recorded Internal=true wildcard
// must NOT absorb a later Internal=false specific-port sibling.
func TestMergeDuplicateEndpoints_InternalFieldGuardsSpecificFoldingIntoWildcard(t *testing.T) {
	wildcardInternal := &types.HTTPEndpoint{
		Endpoint:  ":0/login",
		Methods:   []string{"GET"},
		Direction: "outbound",
		Internal:  true,
	}
	specificExternal := &types.HTTPEndpoint{
		Endpoint:  ":443/login",
		Methods:   []string{"POST"},
		Direction: "outbound",
		Internal:  false,
	}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{wildcardInternal, specificExternal})

	assert.Equal(t, 2, len(result),
		"specific-port with Internal=false must NOT fold into a wildcard sibling with Internal=true")
}

// TestMergeDuplicateEndpoints_InternalFieldMatching_StillFolds is the
// positive sanity check: when Internal DOES match, the existing
// path+direction merge contract still holds. A regression here would mean
// the Internal guard accidentally blocks legitimate folding.
func TestMergeDuplicateEndpoints_InternalFieldMatching_StillFolds(t *testing.T) {
	wildcard := &types.HTTPEndpoint{
		Endpoint:  ":0/login",
		Methods:   []string{"GET"},
		Direction: "outbound",
		Internal:  true,
	}
	specific := &types.HTTPEndpoint{
		Endpoint:  ":443/login",
		Methods:   []string{"POST"},
		Direction: "outbound",
		Internal:  true,
	}

	result := dynamicpathdetector.MergeDuplicateEndpoints([]*types.HTTPEndpoint{wildcard, specific})

	assert.Equal(t, 1, len(result), "matching Internal => specific-port sibling still folds into wildcard")
	assert.Equal(t, ":0/login", result[0].Endpoint)
	assert.True(t, result[0].Internal, "merged endpoint must preserve Internal=true")
	assert.ElementsMatch(t, []string{"GET", "POST"}, result[0].Methods)
}
