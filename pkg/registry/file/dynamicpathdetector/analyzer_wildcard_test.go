package dynamicpathdetector

import (
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func opensCover(opens []types.OpenCalls, path string) bool {
	for _, open := range opens {
		if open.Path == path || CompareDynamic(open.Path, path) {
			return true
		}
	}
	return false
}

// A profile is analyzed again on every save. Analyzing an already collapsed
// profile must not narrow it: every path the first pass covered has to stay
// covered by the second.
func TestAnalyzeOpens_ReanalysisKeepsWildcardCoverage(t *testing.T) {
	n := OpenDynamicThreshold + 1
	var paths []string
	for i := 0; i < n; i++ {
		// Many files directly under /data collapse to /data/⋯, and many
		// directories with many files each collapse to /data/*.
		paths = append(paths, fmt.Sprintf("/data/f%d", i))
		for j := 0; j < n; j++ {
			paths = append(paths, fmt.Sprintf("/data/d%d/g%d", i, j))
		}
	}
	opens := make([]types.OpenCalls, 0, len(paths))
	for _, path := range paths {
		opens = append(opens, types.OpenCalls{Path: path, Flags: []string{"O_RDONLY"}})
	}

	first, err := AnalyzeOpens(opens, NewPathAnalyzer(OpenDynamicThreshold), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if !opensCover(first, path) {
			t.Fatalf("first pass does not cover %q: %v", path, first)
		}
	}

	second, err := AnalyzeOpens(first, NewPathAnalyzer(OpenDynamicThreshold), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if !opensCover(second, path) {
			t.Fatalf("second pass no longer covers %q: %v -> %v", path, first, second)
		}
	}
}

// * matches any depth and ⋯ exactly one segment, so siblings /data/* and
// /data/⋯ merge to /data/*, in either order.
func TestAnalyzeOpens_WildcardIsNotNarrowedToDynamic(t *testing.T) {
	wildcard := "/data/" + WildcardIdentifier
	dynamic := "/data/" + DynamicIdentifier
	for _, in := range [][]string{{wildcard, dynamic}, {dynamic, wildcard}} {
		opens := make([]types.OpenCalls, 0, len(in))
		for _, path := range in {
			opens = append(opens, types.OpenCalls{Path: path, Flags: []string{"O_RDONLY"}})
		}
		out, err := AnalyzeOpens(opens, NewPathAnalyzer(OpenDynamicThreshold), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(out) != 1 || out[0].Path != wildcard {
			t.Errorf("AnalyzeOpens(%v) = %v, want only %q", in, out, wildcard)
		}
		if !opensCover(out, "/data/a/b") {
			t.Errorf("AnalyzeOpens(%v) = %v no longer covers /data/a/b", in, out)
		}
	}
}
