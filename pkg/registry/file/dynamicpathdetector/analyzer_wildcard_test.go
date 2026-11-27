package dynamicpathdetector

import (
	"fmt"
	"slices"
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

// A * with segments after it matches zero or more segments in between, so
// /app/data/*/index.json covers every index.json below /app/data and nothing
// else. Many directories at two adjacent levels with a fixed file collapse to
// that pattern, and the next save must keep it: it used to come back as
// /app/data/*, which covers every file below /app/data.
func TestAnalyzeOpens_ReanalysisKeepsInnerWildcard(t *testing.T) {
	n := OpenDynamicThreshold + 1
	var opens []types.OpenCalls
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			opens = append(opens, types.OpenCalls{Path: fmt.Sprintf("/app/data/t%d/u%d/index.json", i, j), Flags: []string{"O_RDONLY"}})
		}
	}
	pattern := "/app/data/" + WildcardIdentifier + "/index.json"

	first, err := AnalyzeOpens(opens, NewPathAnalyzer(OpenDynamicThreshold), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].Path != pattern {
		t.Fatalf("first pass = %v, want only %q", first, pattern)
	}

	// The next save sees the stored pattern plus a newly observed file it covers.
	again := append(slices.Clone(first), types.OpenCalls{Path: "/app/data/t99/u3/index.json", Flags: []string{"O_WRONLY"}})
	second, err := AnalyzeOpens(again, NewPathAnalyzer(OpenDynamicThreshold), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].Path != pattern {
		t.Fatalf("second pass = %v, want only %q", second, pattern)
	}
	if !slices.Equal(second[0].Flags, []string{"O_RDONLY", "O_WRONLY"}) {
		t.Errorf("second pass flags = %v, want the covered open's flags folded in", second[0].Flags)
	}
	for _, path := range []string{"/app/data/secret.env", "/app/data/t1/u1/token"} {
		if opensCover(second, path) {
			t.Errorf("second pass %v covers %q, which no observed open matched", second, path)
		}
	}
}

// A user writes a narrow pattern in a profile, and every write of the profile
// analyzes it. It must be stored as written, not as /etc/ssl/*.
func TestAnalyzeOpens_KeepsAuthoredInnerWildcard(t *testing.T) {
	pattern := "/etc/ssl/" + WildcardIdentifier + "/cert.pem"
	opens := []types.OpenCalls{
		{Path: pattern, Flags: []string{"O_RDONLY"}},
		{Path: "/etc/ssl/openssl.cnf", Flags: []string{"O_RDONLY"}},
	}
	out, err := AnalyzeOpens(opens, NewPathAnalyzer(OpenDynamicThreshold), nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, open := range out {
		paths = append(paths, open.Path)
	}
	if !slices.Equal(paths, []string{pattern, "/etc/ssl/openssl.cnf"}) {
		t.Fatalf("AnalyzeOpens = %v, want the pattern and the literal kept as written", paths)
	}
	if opensCover(out, "/etc/ssl/private/server.key") {
		t.Errorf("%v covers /etc/ssl/private/server.key", paths)
	}
}

func TestAnalyzePath_KeepsInnerWildcard(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/a/*/b", "/a/*/b"},
		{"/*/b", "/*/b"},
		{"/a/*/b/*/c", "/a/*/b/*/c"},
		{"/a/*/b/", "/a/*/b"},
		// A trailing * still takes everything below it.
		{"/a/*", "/a/*"},
	} {
		got, err := NewPathAnalyzer(OpenDynamicThreshold).AnalyzePath(tc.in, "opens")
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("AnalyzePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// HTTP endpoints go through the same analyzer.
func TestAnalyzeEndpoints_KeepsInnerWildcard(t *testing.T) {
	endpoints := []types.HTTPEndpoint{
		{Endpoint: ":443/api/" + WildcardIdentifier + "/status", Methods: []string{"GET"}},
		{Endpoint: ":443/api/v1/users", Methods: []string{"GET"}},
	}
	out := AnalyzeEndpoints(&endpoints, NewPathAnalyzer(EndpointDynamicThreshold))
	var got []string
	for _, endpoint := range out {
		got = append(got, endpoint.Endpoint)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{":443/api/*/status", ":443/api/v1/users"}) {
		t.Fatalf("AnalyzeEndpoints = %v, want both endpoints kept as written", got)
	}
}
