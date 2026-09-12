package dynamicpathdetector

import (
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func analyzePaths(t *testing.T, paths ...string) []string {
	t.Helper()
	in := make([]types.OpenCalls, 0, len(paths))
	for _, p := range paths {
		in = append(in, types.OpenCalls{Path: p, Flags: []string{"O_RDONLY"}})
	}
	out, err := AnalyzeOpens(in, NewPathAnalyzer(100), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(out))
	for _, o := range out {
		got = append(got, o.Path)
	}
	return got
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// ⋯ is exactly one segment and * is zero-or-more, so a node carrying both must
// keep the *. Routing the * through the existing ⋯ child rewrites it, which
// NARROWS the stored profile: an authored profile that declared
// /bitnami/postgresql/data/base/* was stored with only .../base/⋯, and every
// relation file two levels down — .../base/16384/2650 — then raised R0002.
func TestWildcardSurvivesAlongsideDynamicAtTheSameNode(t *testing.T) {
	for _, order := range [][]string{
		{"/a/b/⋯", "/a/b/*"},
		{"/a/b/*", "/a/b/⋯"},
	} {
		got := analyzePaths(t, order...)
		if !contains(got, "/a/b/*") {
			t.Errorf("input %v stored as %v — the * was rewritten and the profile is narrower than authored", order, got)
		}
	}
}

// A * that is not the leaf may be stored in a broader form — a trailing * already
// spans the segments below it — but never in one that stops matching.
func TestWildcardInAMiddleSegmentStaysAtLeastAsBroad(t *testing.T) {
	got := analyzePaths(t, "/a/b/⋯", "/a/b/*/c")
	for _, in := range []string{"/a/b/x/c", "/a/b/x/y/c"} {
		if !CompareDynamic("/a/b/*/c", in) {
			continue
		}
		matched := false
		for _, p := range got {
			if CompareDynamic(p, in) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("stored %v — none match %q, which /a/b/*/c admitted", got, in)
		}
	}
}

// What the fix has to protect: a stored pattern must still match everything the
// authored one did.
func TestStoredPatternStillMatchesWhatTheAuthorDeclared(t *testing.T) {
	const deep = "/bitnami/postgresql/data/base/16384/2650"
	if !CompareDynamic("/bitnami/postgresql/data/base/*", deep) {
		t.Fatal("fixture is wrong: the authored pattern does not match")
	}
	got := analyzePaths(t, "/bitnami/postgresql/data/base/⋯", "/bitnami/postgresql/data/base/*")

	matched := false
	for _, p := range got {
		if CompareDynamic(p, deep) {
			matched = true
		}
	}
	if !matched {
		t.Errorf("stored %v — none of these match %q, which the author declared", got, deep)
	}
}

// A * alone, and a ⋯ alone, must each be stored as written.
func TestSingleWildcardFormsAreUnchanged(t *testing.T) {
	if got := analyzePaths(t, "/a/b/*"); !contains(got, "/a/b/*") {
		t.Errorf("stored %v, want /a/b/*", got)
	}
	if got := analyzePaths(t, "/a/b/⋯"); !contains(got, "/a/b/⋯") {
		t.Errorf("stored %v, want /a/b/⋯", got)
	}
}
