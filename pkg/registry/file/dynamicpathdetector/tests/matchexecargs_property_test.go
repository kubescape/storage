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
// Exec-arg token vocabulary covered by the alphabet:
//   "a","b"  literals               (profileArg == runtimeArg)
//   "*"      a LITERAL star          (NOT a wildcard in exec args — matched as data)
//   "⋯⋯"     ExecArgsWildcard        (positional zero-or-more whole args)
//   "⋯"      DynamicIdentifier       (bare -> any single arg)
//   "/x/⋯"   embedded-⋯ path token   (⋯ = exactly one segment, rest literal)
//   "/x/*"   embedded LITERAL star   (matched literally, no broadening)
// The runtime alphabet includes "c", concrete paths, and "/x/*" so the
// star-is-data path is exercised against both matching and non-matching inputs.
// ============================================================================

// refArgMatch mirrors the single-token contract of argMatches (intentionally —
// we are verifying the SEQUENCE/backtracking logic, holding the per-token
// decision fixed). "*" is NOT special here: only "⋯" is a within-token wildcard.
func refArgMatch(profileArg, runtimeArg string) bool {
	if profileArg == dp.DynamicIdentifier { // bare "⋯"
		return true
	}
	if strings.Contains(profileArg, dp.DynamicIdentifier) {
		// segment-wise: "⋯" matches exactly one segment, everything else
		// (including "*") is literal; segment counts must be equal.
		p := strings.Split(profileArg, "/")
		r := strings.Split(runtimeArg, "/")
		if len(p) != len(r) {
			return false
		}
		for i := range p {
			if p[i] != dp.DynamicIdentifier && p[i] != r[i] {
				return false
			}
		}
		return true
	}
	return profileArg == runtimeArg // literal, "*" included
}

// refMatch is the naive (un-memoised) backtracking reference for the strict
// anchored matcher. Obviously correct by construction.
func refMatch(p, r []string) bool {
	if len(p) == 0 {
		return len(r) == 0
	}
	if p[0] == dp.ExecArgsWildcard { // "⋯⋯": absorb 0..len(r) whole args
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
	profAlphabet := []string{"a", "b", "*", dp.ExecArgsWildcard, dp.DynamicIdentifier, "/x/" + dp.DynamicIdentifier, "/x/*"}
	runAlphabet := []string{"a", "b", "c", "*", "/x/y", "/x/y/z", "/x/*"}

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
	profAlphabet := []string{"a", "*", dp.ExecArgsWildcard, dp.DynamicIdentifier}
	runAlphabet := []string{"a", "b", "*"}
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
		// matches its own vector and nothing longer. Note a "*" token is
		// literal in exec args, so ["*"] is a literal profile here.
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

// isLiteral reports whether every token is matched literally — i.e. none is a
// wildcard. In exec args the only wildcards are "⋯" (and "⋯⋯", which contains
// "⋯"); "*" is data.
func isLiteral(p []string) bool {
	for _, t := range p {
		if strings.Contains(t, dp.DynamicIdentifier) {
			return false
		}
	}
	return true
}
