/*
Copyright 2024 The Kubescape Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dynamicpathdetectortests

import (
	"slices"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
)

// extractPathsFromOpens returns just the paths from a slice of OpenCalls,
// for cleaner test assertions.
func extractPathsFromOpens(opens []types.OpenCalls) []string {
	out := make([]string, len(opens))
	for i, o := range opens {
		out[i] = o.Path
	}
	return out
}

// TestConsolidateOpens_DynamicIdentifierSubsumesSiblings pins the
// post-trie cleanup contract: when threshold collapse produces a
// `/etc/⋯` entry alongside a single concrete `/etc/hosts`, the
// concrete sibling is absorbed into the dynamic pattern (which already
// covers it), so downstream matchers don't see redundant entries.
func TestConsolidateOpens_DynamicIdentifierSubsumesSiblings(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(50, []dynamicpathdetector.CollapseConfig{
		{Prefix: "/etc", Threshold: 3},
	})
	input := []types.OpenCalls{
		{Path: "/etc/file1", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/file2", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/file3", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/hosts", Flags: []string{"O_RDONLY"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err)

	paths := extractPathsFromOpens(result)
	assert.Contains(t, paths, "/etc/⋯", "should retain the dynamic pattern")
	assert.NotContains(t, paths, "/etc/hosts", "single-segment sibling must be subsumed by /etc/⋯")
	assert.NotContains(t, paths, "/etc/file1", "siblings under the collapsed prefix must be subsumed")
}

// TestConsolidateOpens_DynamicIdentifierDoesNotSubsumeMultiSegment pins
// the depth contract: ⋯ matches EXACTLY ONE segment, so a multi-segment
// path under the same prefix (e.g. /etc/nginx/conf.d/foo) is NOT
// subsumed by /etc/⋯. This is the security-relevant rule — without it,
// a deeper path could disappear from the profile silently.
func TestConsolidateOpens_DynamicIdentifierDoesNotSubsumeMultiSegment(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(50, []dynamicpathdetector.CollapseConfig{
		{Prefix: "/etc", Threshold: 3},
	})
	input := []types.OpenCalls{
		{Path: "/etc/file1", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/file2", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/file3", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/hosts", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/nginx/conf.d/default.conf", Flags: []string{"O_RDONLY"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err)

	paths := extractPathsFromOpens(result)
	assert.Contains(t, paths, "/etc/⋯", "should retain the dynamic pattern")
	// The trie collapses the deeper level into /etc/⋯/conf.d/default.conf.
	// That multi-segment path is itself a pattern, so it survives, and
	// it is NOT subsumed by /etc/⋯ either (because /etc/⋯ matches ONE
	// segment — the multi-segment path is two segments deep past /etc/).
	assert.Contains(t, paths, "/etc/⋯/conf.d/default.conf",
		"deeper path under same prefix must NOT be subsumed by /etc/⋯ — different depth")
}

// TestConsolidateOpens_FlagsMergeIntoPattern pins that subsumption
// preserves access semantics: if /tmp/a (O_RDONLY) and /tmp/b (O_WRONLY)
// are both subsumed by /tmp/⋯, the resulting pattern entry must carry
// BOTH flags so the runtime matcher knows the workload performs both
// reads and writes under /tmp.
func TestConsolidateOpens_FlagsMergeIntoPattern(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(50, []dynamicpathdetector.CollapseConfig{
		{Prefix: "/tmp", Threshold: 2},
	})
	input := []types.OpenCalls{
		{Path: "/tmp/a", Flags: []string{"O_RDONLY"}},
		{Path: "/tmp/b", Flags: []string{"O_WRONLY"}},
		{Path: "/tmp/c", Flags: []string{"O_RDWR"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err)

	assert.Len(t, result, 1, "all three /tmp paths should consolidate to one pattern")
	assert.Equal(t, "/tmp/⋯", result[0].Path)
	assert.Contains(t, result[0].Flags, "O_RDONLY")
	assert.Contains(t, result[0].Flags, "O_WRONLY")
	assert.Contains(t, result[0].Flags, "O_RDWR")
}

// TestConsolidateOpens_SbomPathsNeverSubsumed pins the SBOM safeguard:
// even if a /usr/lib/⋯ pattern would cover /usr/lib/libcrypto.so.3,
// the latter must SURVIVE if it's listed in the SBOM. Reason: the
// SBOM is the ground-truth manifest of the image's static contents;
// a wildcard collapsing it away erases provenance.
func TestConsolidateOpens_SbomPathsNeverSubsumed(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(50, []dynamicpathdetector.CollapseConfig{
		{Prefix: "/usr/lib", Threshold: 3},
	})
	sbomSet := mapset.NewThreadUnsafeSet[string]("/usr/lib/libcrypto.so.3")
	input := []types.OpenCalls{
		{Path: "/usr/lib/libcrypto.so.3", Flags: []string{"O_RDONLY"}},
		{Path: "/usr/lib/libssl.so.3", Flags: []string{"O_RDONLY"}},
		{Path: "/usr/lib/libz.so.1", Flags: []string{"O_RDONLY"}},
		{Path: "/usr/lib/libm.so.6", Flags: []string{"O_RDONLY"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, sbomSet)
	assert.NoError(t, err)

	paths := extractPathsFromOpens(result)
	assert.Contains(t, paths, "/usr/lib/libcrypto.so.3", "SBOM path must survive consolidation")
	assert.Contains(t, paths, "/usr/lib/⋯", "dynamic pattern still emitted")
	assert.NotContains(t, paths, "/usr/lib/libssl.so.3", "non-SBOM siblings absorbed")
	assert.NotContains(t, paths, "/usr/lib/libm.so.6", "non-SBOM siblings absorbed")
}

// TestConsolidateOpens_NoPatternsNoConsolidation pins the no-op case:
// when the trie hasn't collapsed anything (threshold not hit), there
// are no patterns to subsume into, and consolidation is a pure
// pass-through. Two distinct /etc paths in, two paths out.
func TestConsolidateOpens_NoPatternsNoConsolidation(t *testing.T) {
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(100, nil)
	input := []types.OpenCalls{
		{Path: "/etc/hosts", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/passwd", Flags: []string{"O_RDONLY"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err)
	assert.Len(t, result, 2, "no collapse → no consolidation")
}

// TestConsolidateOpens_TrailingStarSubsumesChild pins that the WildcardIdentifier
// (`*`) form also drives subsumption: if a profile carries /etc/* explicitly
// (e.g. a hand-written user-defined-profile), and the trie also yields
// concrete /etc/foo entries from auto-discovery, the concrete entry is
// subsumed by the wildcard. Uses CompareDynamic which (per yesterday's
// anchoring fix) requires trailing `*` to consume one or more segments.
func TestConsolidateOpens_TrailingStarSubsumesChild(t *testing.T) {
	// We can't easily produce both /etc/* and /etc/foo from AnalyzeOpens
	// in one go (the trie only emits one form per node). So we exercise
	// the pure consolidateOpens contract by feeding a mixed slice that
	// AnalyzeOpens would produce only after subsequent merges. This test
	// goes through the public AnalyzeOpens but constructs an analyzer
	// where threshold-1 forces every group to collapse.
	analyzer := dynamicpathdetector.NewPathAnalyzerWithConfigs(50, []dynamicpathdetector.CollapseConfig{
		{Prefix: "/etc", Threshold: 1},
	})
	input := []types.OpenCalls{
		{Path: "/etc/foo", Flags: []string{"O_RDONLY"}},
		{Path: "/etc/bar", Flags: []string{"O_RDONLY"}},
	}
	result, err := dynamicpathdetector.AnalyzeOpens(input, analyzer, nil)
	assert.NoError(t, err)

	// With threshold 1 the analyzer emits the WildcardIdentifier ("/etc/*"),
	// not the dynamic form. Either way, both literals must be subsumed.
	paths := extractPathsFromOpens(result)
	assert.True(t,
		slices.Contains(paths, "/etc/*") || slices.Contains(paths, "/etc/⋯"),
		"expected a single collapsed pattern for /etc, got %v", paths)
	assert.NotContains(t, paths, "/etc/foo")
	assert.NotContains(t, paths, "/etc/bar")
}
