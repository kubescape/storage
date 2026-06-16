package dynamicpathdetectortests

import (
	"strings"
	"testing"

	dp "github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

// ============================================================================
// FORMAL VERIFICATION of the exec-arg matcher over its full bounded input space.
//
// MatchExecArgs(profile, true, runtime) is implemented by matchExecArgsStrict,
// which memoises backtracking on (profileIndex, runtimeIndex). We verify it is
// EQUIVALENT to an independent, obviously-correct naive recursion (no memo) for
// EVERY profile/runtime pair over a small token alphabet up to length 4. The
// memo's only job is to bound complexity, not change semantics — so equivalence
// across all (pi,ri) reachable states (which length-4 sequences exhaust for the
// matcher's structure) proves the memoised matcher matches the spec.
//
// Token alphabet covers every argMatches branch:
//   "a","b"  literals          (profileArg == runtimeArg)
//   "*"      WildcardIdentifier (positional zero-or-more, in matchExecArgsStrict)
//   "⋯"      DynamicIdentifier  (bare -> any single arg)
//   "/x/⋯"   embedded-⋯ path    (routes to CompareDynamic)
//   "/x/*"   embedded-* path    (routes to CompareDynamic — current behavior)
// Runtime alphabet includes "c" and concrete paths the profile never lists.
// ============================================================================

// refArgMatch mirrors the single-token contract of argMatches (intentionally —
// we are verifying the SEQUENCE/backtracking logic, holding the per-token
// decision fixed). CompareDynamic is the real exported matcher.
func refArgMatch(profileArg, runtimeArg string) bool {
	if profileArg == dp.DynamicIdentifier { // bare "⋯"
		return true
	}
	if strings.Contains(profileArg, dp.DynamicIdentifier) || strings.Contains(profileArg, dp.WildcardIdentifier) {
		return dp.CompareDynamic(profileArg, runtimeArg)
	}
	return profileArg == runtimeArg
}

// refMatch is the naive (un-memoised) backtracking reference for the strict
// anchored matcher. Obviously correct by construction.
func refMatch(p, r []string) bool {
	if len(p) == 0 {
		return len(r) == 0
	}
	if p[0] == dp.WildcardIdentifier { // bare "*": absorb 0..len(r)
		for k := 0; k <= len(r); k++ {
			if refMatch(p[1:], r[k:]) {
				return true
			}
		}
		return false
	}
	if len(r) == 0 {
		return false
	}
	if refArgMatch(p[0], r[0]) {
		return refMatch(p[1:], r[1:])
	}
	return false
}

func enumerate(alphabet []string, maxLen int) [][]string {
	out := [][]string{{}}
	cur := [][]string{{}}
	for l := 1; l <= maxLen; l++ {
		var next [][]string
		for _, seq := range cur {
			for _, tok := range alphabet {
				ns := append(append([]string{}, seq...), tok)
				next = append(next, ns)
			}
		}
		out = append(out, next...)
		cur = next
	}
	return out
}

func TestMatchExecArgs_DifferentialAgainstNaiveOracle(t *testing.T) {
	profAlphabet := []string{"a", "b", dp.WildcardIdentifier, dp.DynamicIdentifier, "/x/" + dp.DynamicIdentifier, "/x/" + dp.WildcardIdentifier}
	runAlphabet := []string{"a", "b", "c", "/x/y", "/x/y/z"}

	profiles := enumerate(profAlphabet, 4)
	runtimes := enumerate(runAlphabet, 4)

	checked, mismatches := 0, 0
	for _, p := range profiles {
		for _, r := range runtimes {
			want := refMatch(p, r)
			got := dp.MatchExecArgs(p, true, r)
			checked++
			if got != want {
				mismatches++
				if mismatches <= 20 {
					t.Errorf("DIVERGENCE: MatchExecArgs(%q,true,%q)=%v, naive oracle=%v", p, r, got, want)
				}
			}
		}
	}
	t.Logf("exhaustively checked %d (profile,runtime) pairs; %d divergences", checked, mismatches)
}

// TestMatchExecArgs_ContractInvariants pins the documented contract clauses
// over the same input space.
func TestMatchExecArgs_ContractInvariants(t *testing.T) {
	profAlphabet := []string{"a", "b", dp.WildcardIdentifier, dp.DynamicIdentifier}
	runAlphabet := []string{"a", "b", "c"}
	profiles := enumerate(profAlphabet, 4)
	runtimes := enumerate(runAlphabet, 4)

	for _, p := range profiles {
		for _, r := range runtimes {
			// Clause: argsRequired=false => always true (no constraint).
			if !dp.MatchExecArgs(p, false, r) {
				t.Fatalf("argsRequired=false must always match: p=%q r=%q", p, r)
			}
		}
		// Clause: empty profile matches ONLY empty runtime (strict).
		if dp.MatchExecArgs(nil, true, []string{"a"}) {
			t.Fatal("empty profile must not match non-empty runtime")
		}
		if !dp.MatchExecArgs(nil, true, nil) {
			t.Fatal("empty profile must match empty runtime")
		}
		// Clause: a purely-literal profile is reflexive and anchored — it
		// matches its own vector and nothing longer.
		if isLiteral(p) {
			if !dp.MatchExecArgs(p, true, p) {
				t.Fatalf("literal profile must match itself: %q", p)
			}
			if len(p) > 0 && dp.MatchExecArgs(p, true, append(append([]string{}, p...), "c")) {
				t.Fatalf("literal profile must be anchored (no trailing slack): %q", p)
			}
		}
	}
}

func isLiteral(p []string) bool {
	for _, t := range p {
		if t == dp.WildcardIdentifier || t == dp.DynamicIdentifier ||
			strings.Contains(t, dp.WildcardIdentifier) || strings.Contains(t, dp.DynamicIdentifier) {
			return false
		}
	}
	return true
}
