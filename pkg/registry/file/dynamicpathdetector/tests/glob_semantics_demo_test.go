package dynamicpathdetectortests

import (
	"fmt"
	"testing"

	dp "github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

// This is a DEMONSTRATION (run with -v) that prints, straight from the
// production matcher, exactly what globs and how — separately for OPENS
// (CompareDynamic over analyzer-emitted patterns, where "*" is a path wildcard)
// and for EXECS (MatchExecArgs over recorded argv, where "*" is a LITERAL and
// wildcarding is done with dedicated collision-free sentinels).
//
// Token vocabulary (dynamicpathdetector/types.go):
//   OPENS / paths:
//     "⋯"  matches exactly ONE path segment
//     "*"  matches ZERO-OR-MORE path segments
//   EXEC args:
//     "⋯"  matches exactly ONE whole arg, or one segment embedded in a token
//     "⋯⋯" matches ZERO-OR-MORE whole args (positional)
//     "*"  is a LITERAL "*" character — never a wildcard (no escaping needed)

func yn(b bool) string {
	if b {
		return "MATCH  "
	}
	return "no     "
}

// ---------------------------------------------------------------------------
// OPENS: patterns come from the analyzer (analyze_opens.go) and contain ONLY
// "*" / "⋯". CompareDynamic is the matcher. Opens are path-globs: "*" spans
// segments, "⋯" is a single segment.
// ---------------------------------------------------------------------------
func TestDemo_Opens_Globbing(t *testing.T) {
	patterns := []string{
		"/var/log/*",            // zero-or-more trailing segments
		"/var/log/⋯",            // exactly one segment
		"/proc/⋯/status",        // one dynamic segment, anchored both sides
		"/data/*/cache",         // zero-or-more segments in the middle
		"/etc/nginx/nginx.conf", // fully concrete, no glob
	}
	paths := []string{
		"/var/log",
		"/var/log/syslog",
		"/var/log/nginx/access.log",
		"/proc/123/status",
		"/proc/1/2/status",
		"/data/cache",
		"/data/a/b/cache",
		"/etc/nginx/nginx.conf",
	}
	t.Log("OPENS — dp.CompareDynamic(pattern, path)  [* = zero-or-more segments]")
	for _, p := range patterns {
		line := fmt.Sprintf("  pattern %-26q :", p)
		for _, path := range paths {
			line += fmt.Sprintf("  %s→%s", path, yn(dp.CompareDynamic(p, path)))
		}
		t.Log(line)
	}
}

// TestDemo_Opens_StarStaysWildcard documents that opens are unchanged: "*" is a
// path wildcard on the opens side, and a backslash is never special (a real
// file path containing "\" matches verbatim).
func TestDemo_Opens_StarStaysWildcard(t *testing.T) {
	if !dp.CompareDynamic("/var/lib/*/data", "/var/lib/pg/16/data") {
		t.Error("opens: * must span segments")
	}
	if !dp.CompareDynamic(`/data/odd\name`, `/data/odd\name`) {
		t.Error(`opens: literal "\" path must match itself verbatim`)
	}
}

// ---------------------------------------------------------------------------
// EXECS: recorded argv. A standalone "⋯⋯" is a positional zero-or-more
// wildcard. A standalone "⋯" is exactly one arg. "⋯" embedded in a token = one
// segment. A "*" anywhere is a LITERAL character.
// ---------------------------------------------------------------------------
func TestDemo_Execs_Globbing(t *testing.T) {
	const w = dp.ExecArgsWildcard // "⋯⋯"
	type row struct {
		desc    string
		profile []string
	}
	rows := []row{
		{"⋯⋯ = positional wildcard (0+ args)", []string{"app", w}},
		{"⋯ standalone = exactly one arg", []string{"app", "⋯"}},
		{"embedded ⋯ = one segment in one token", []string{"pg", "/usr/lib/postgresql/⋯/bin"}},
		{"* is a LITERAL (no broaden)", []string{"tool", "--load", "/plugins/*"}},
	}
	runtimes := [][]string{
		{"app"},
		{"app", "x"},
		{"app", "x", "y"},
		{"pg", "/usr/lib/postgresql/16/bin"},
		{"pg", "/usr/lib/postgresql/a/b/bin"},
		{"tool", "--load", "/plugins/*"},
		{"tool", "--load", "/plugins/evil.so"},
	}
	t.Log("EXECS — dp.MatchExecArgs(profile, true, runtime)")
	for _, r := range rows {
		t.Logf("  profile %-40v  (%s)", r.profile, r.desc)
		for _, rt := range runtimes {
			t.Logf("      vs %-45v %s", rt, yn(dp.MatchExecArgs(r.profile, true, rt)))
		}
	}
}
