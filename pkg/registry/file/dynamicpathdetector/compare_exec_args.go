package dynamicpathdetector

import "strings"

// MatchExecArgs reports whether runtimeArgs satisfies a profile entry's argv
// contract. argsRequired carries the entry's ExecCalls.ArgsRequired flag:
//
//	false → no constraint; matches any runtimeArgs.
//	true  → strict anchored match against profileArgs (empty profileArgs
//	        matches only an empty runtimeArgs).
//
// The flag exists because v1beta1.ExecCalls.Args is `json:",omitempty"`: an
// explicit `args: []` round-trips back as nil, so the stored vector alone
// cannot distinguish "no constraint" from "must have no args".
func MatchExecArgs(profileArgs []string, argsRequired bool, runtimeArgs []string) bool {
	if !argsRequired {
		return true
	}
	return matchExecArgsStrict(profileArgs, runtimeArgs)
}

// CompareExecArgs reports whether runtimeArgs matches profileArgs, treating an
// empty profileArgs as "no constraint" (matches anything). Non-empty vectors
// are matched anchored at both ends by matchExecArgsStrict.
//
// Use MatchExecArgs to express "argv must be empty"; CompareExecArgs is kept
// for callers that have not migrated to the ArgsRequired-aware API.
func CompareExecArgs(profileArgs, runtimeArgs []string) bool {
	if len(profileArgs) == 0 {
		return true
	}
	return matchExecArgsStrict(profileArgs, runtimeArgs)
}

// matchExecArgsStrict matches profileArgs against runtimeArgs position by
// position, anchored at both ends. An empty profileArgs matches only an empty
// runtimeArgs. Tokens are matched by argMatches, except a bare
// WildcardIdentifier ("*"), which absorbs zero or more consecutive runtime
// args.
//
// Index-based backtracking memoised on (profileIndex, runtimeIndex): without
// the memo, patterns like [*, *, …, x] against a long literal vector backtrack
// exponentially; the memo bounds it at O(len(profile)*len(runtime)).
func matchExecArgsStrict(profileArgs, runtimeArgs []string) bool {
	if len(profileArgs) == 0 {
		return len(runtimeArgs) == 0
	}

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

		if pi == len(profileArgs) {
			memo[s] = ri == len(runtimeArgs)
			return memo[s]
		}

		head := profileArgs[pi]

		if head == WildcardIdentifier {
			// Absorb 0..remaining runtime args; first successful split wins.
			for k := ri; k <= len(runtimeArgs); k++ {
				if match(pi+1, k) {
					memo[s] = true
					return true
				}
			}
			memo[s] = false
			return false
		}

		if ri == len(runtimeArgs) {
			memo[s] = false
			return false
		}

		if argMatches(head, runtimeArgs[ri]) {
			memo[s] = match(pi+1, ri+1)
			return memo[s]
		}

		memo[s] = false
		return false
	}

	return match(0, 0)
}

// argMatches matches one profile token against one runtime arg:
//
//	bare "⋯"                  — any single arg.
//	token containing "⋯"/"*"  — a dynamic path; matched segment-wise by
//	                            CompareDynamic ("⋯" = one segment, "*" = zero+).
//	anything else             — literal equality.
//
// A bare "*" never reaches here — matchExecArgsStrict consumes it as a
// positional wildcard first — so any "*" seen is embedded in a path token (the
// form the analyzer emits when it collapses adjacent dynamic segments).
func argMatches(profileArg, runtimeArg string) bool {
	if profileArg == DynamicIdentifier {
		return true
	}
	if strings.Contains(profileArg, DynamicIdentifier) || strings.Contains(profileArg, WildcardIdentifier) {
		return CompareDynamic(profileArg, runtimeArg)
	}
	return profileArg == runtimeArg
}
