package dynamicpathdetector

// MatchExecArgs reports whether a runtime exec argument vector satisfies a
// profile entry's argv contract. argsRequired carries the profile entry's
// ExecCalls.ArgsRequired flag and disambiguates the two cases that
// CompareExecArgs alone cannot tell apart:
//
//	argsRequired = false → no argv constraint; matches any runtime args.
//	                       This is the back-compat path for profiles that
//	                       omit Args (the common case for path-only
//	                       Execs entries in user-authored profiles).
//	argsRequired = true  → strict anchored match against profileArgs.
//	                       An empty profileArgs means "argv MUST be
//	                       empty"; a non-empty profileArgs is matched
//	                       anchored with wildcard tokens (see below).
//
// This resolves the round-trip ambiguity that v1beta1.ExecCalls.Args
// (declared `json:",omitempty"`) introduced: an explicit `args: []`
// round-trips back as nil, so the storage layer alone cannot persist
// the distinction between "no constraint" and "must have no args".
// ArgsRequired persists the operator's intent explicitly.
//
// The match semantics for argsRequired=true are the anchored-with-
// wildcards form documented on CompareExecArgs.
func MatchExecArgs(profileArgs []string, argsRequired bool, runtimeArgs []string) bool {
	if !argsRequired {
		// No constraint expressed by the profile entry.
		return true
	}
	return matchExecArgsStrict(profileArgs, runtimeArgs)
}

// CompareExecArgs reports whether a runtime exec argument vector matches a
// profile argument vector. The profile vector may contain two wildcard
// tokens:
//
//	DynamicIdentifier  ("⋯") — matches exactly one argument position.
//	WildcardIdentifier ("*") — matches zero or more consecutive arguments.
//
// Anything else is a literal-equality match. The match is anchored at both
// ends: every runtime argument must be consumed by the profile vector,
// either by a literal, a DynamicIdentifier, or absorbed into a
// WildcardIdentifier run.
//
// Empty profileArgs is treated as "no argv constraint" — i.e. matches any
// runtime arg vector. This keeps path-only Execs entries (the common case
// in user-defined ApplicationProfiles, which omit the Args field) from
// silently triggering R0040 just because the rule started consulting
// was_executed_with_args.
//
// NOTE: callers that need to express "argv MUST be empty" cannot do so
// through this API alone, because v1beta1.ExecCalls.Args is declared
// `json:",omitempty"` and an explicit `args: []` round-trips back as
// nil. Use MatchExecArgs with the profile entry's ArgsRequired flag for
// that case. CompareExecArgs is preserved for back-compat with callers
// that have not migrated to the args-required-aware API.
func CompareExecArgs(profileArgs, runtimeArgs []string) bool {
	// Outer-level empty profile = "no argv constraint" — wildcard match.
	// The inner matcher keeps strict empty-empty semantics so anchoring
	// during recursion (`profile fully consumed but runtime has more`)
	// remains a mismatch.
	if len(profileArgs) == 0 {
		return true
	}
	return matchExecArgsStrict(profileArgs, runtimeArgs)
}

// matchExecArgsStrict is the anchored matcher shared by MatchExecArgs and
// CompareExecArgs — neither bypass applies; the profile vector is matched
// position-by-position with wildcard absorption. An empty profileArgs
// matches only an empty runtimeArgs.
//
// Implementation is index-based recursive backtracking with memoisation
// on (profileIndex, runtimeIndex) state pairs. The naive backtracking
// form would degrade to exponential time on adversarial inputs like
// `[*, *, *, …, x]` against a long literal vector — every prefix `*`
// has multiple split choices and the suffix mismatch only surfaces
// at the very end, so each path gets re-explored. Memoisation bounds
// the work at O(len(profile) * len(runtime)) — i.e. quadratic in the
// vector lengths, the standard wildcard-match complexity. CodeRabbit
// flagged this as a Major on PR #27.
func matchExecArgsStrict(profileArgs, runtimeArgs []string) bool {
	// Anchored empty-empty case.
	if len(profileArgs) == 0 {
		return len(runtimeArgs) == 0
	}

	// State key for memoisation: (pi, ri) is the suffix-matching position
	// in profile and runtime vectors respectively. Because both sides only
	// shrink (we never re-enter a prefix), there are at most
	// (len(profile)+1) * (len(runtime)+1) reachable states.
	type state struct{ pi, ri int }
	memo := make(map[state]bool, (len(profileArgs)+1)*(len(runtimeArgs)+1))
	seen := make(map[state]bool, (len(profileArgs)+1)*(len(runtimeArgs)+1))

	var match func(pi, ri int) bool
	match = func(pi, ri int) bool {
		s := state{pi: pi, ri: ri}
		if seen[s] {
			return memo[s]
		}
		seen[s] = true

		// Profile fully consumed → runtime must also be fully consumed
		// (anchored match).
		if pi == len(profileArgs) {
			memo[s] = ri == len(runtimeArgs)
			return memo[s]
		}

		head := profileArgs[pi]

		if head == WildcardIdentifier {
			// Try absorbing 0..(remaining runtime) into this *,
			// then match the rest. First successful split wins.
			for k := ri; k <= len(runtimeArgs); k++ {
				if match(pi+1, k) {
					memo[s] = true
					return true
				}
			}
			memo[s] = false
			return false
		}

		// Non-wildcard head needs a runtime argument to consume.
		if ri == len(runtimeArgs) {
			memo[s] = false
			return false
		}

		if head == DynamicIdentifier || head == runtimeArgs[ri] {
			memo[s] = match(pi+1, ri+1)
			return memo[s]
		}

		memo[s] = false
		return false
	}

	return match(0, 0)
}
