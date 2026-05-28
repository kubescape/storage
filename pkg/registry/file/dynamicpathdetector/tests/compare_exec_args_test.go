package dynamicpathdetectortests

import (
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

// CompareExecArgs matches a runtime argument vector against a profile
// argument vector that may contain two wildcard tokens:
//
//	"⋯" (DynamicIdentifier)  — matches exactly ONE argument position.
//	"*" (WildcardIdentifier) — matches ZERO OR MORE consecutive args.
//
// Anything else is a literal string match. The match must be exact across
// the full vectors — extra runtime args after the profile is exhausted (and
// no trailing wildcard absorbs them) is a non-match.

func TestCompareExecArgs_LiteralMatch(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		runtime []string
		want    bool
	}{
		// Empty profileArgs = "no argv constraint" — matches any runtime.
		// Pinned this way so path-only Execs entries in user-defined
		// ApplicationProfiles don't silently trigger R0040 when the rule
		// consults was_executed_with_args. See storage/node-agent issue
		// where Test_28 (and others using path-only entries) failed because
		// the strict empty-empty match was firing R0040 on every legit exec.
		{"both empty", nil, nil, true},
		{"empty profile, non-empty runtime", nil, []string{"a"}, true},
		{"empty profile, multi-arg runtime", nil, []string{"a", "b", "c"}, true},
		{"non-empty profile, empty runtime", []string{"a"}, nil, false},
		{"single literal match", []string{"--help"}, []string{"--help"}, true},
		{"single literal mismatch", []string{"--help"}, []string{"--version"}, false},
		{"profile longer than runtime", []string{"a", "b"}, []string{"a"}, false},
		{"runtime longer than profile (no wildcard)", []string{"a"}, []string{"a", "b"}, false},
		{"multi-literal match", []string{"-l", "-a", "/tmp"}, []string{"-l", "-a", "/tmp"}, true},
		{"multi-literal mismatch in middle", []string{"-l", "-a", "/tmp"}, []string{"-l", "-z", "/tmp"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dynamicpathdetector.CompareExecArgs(tc.profile, tc.runtime); got != tc.want {
				t.Errorf("CompareExecArgs(%v, %v) = %v, want %v", tc.profile, tc.runtime, got, tc.want)
			}
		})
	}
}

func TestCompareExecArgs_DynamicIdentifier(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		runtime []string
		want    bool
	}{
		{"⋯ matches one arg", []string{"⋯"}, []string{"anything"}, true},
		{"⋯ does NOT match zero args", []string{"⋯"}, nil, false},
		{"⋯ does NOT match two args", []string{"⋯"}, []string{"a", "b"}, false},
		{"⋯ in middle, full vector matches", []string{"--user", "⋯", "--port", "8080"}, []string{"--user", "alice", "--port", "8080"}, true},
		{"⋯ in middle, surrounding literal mismatch", []string{"--user", "⋯", "--port", "8080"}, []string{"--user", "alice", "--port", "9090"}, false},
		{"adjacent ⋯⋯ matches exactly two args", []string{"⋯", "⋯"}, []string{"a", "b"}, true},
		{"adjacent ⋯⋯ rejects one arg", []string{"⋯", "⋯"}, []string{"a"}, false},
		{"adjacent ⋯⋯ rejects three args", []string{"⋯", "⋯"}, []string{"a", "b", "c"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dynamicpathdetector.CompareExecArgs(tc.profile, tc.runtime); got != tc.want {
				t.Errorf("CompareExecArgs(%v, %v) = %v, want %v", tc.profile, tc.runtime, got, tc.want)
			}
		})
	}
}

func TestCompareExecArgs_WildcardIdentifier(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		runtime []string
		want    bool
	}{
		{"* matches empty runtime", []string{"*"}, nil, true},
		{"* matches one arg", []string{"*"}, []string{"a"}, true},
		{"* matches many args", []string{"*"}, []string{"a", "b", "c", "d"}, true},
		{"trailing * with prefix match", []string{"-c", "*"}, []string{"-c", "echo hi"}, true},
		{"trailing * absorbs nothing when runtime exact-prefix length", []string{"-c", "*"}, []string{"-c"}, true},
		{"trailing * mismatch in literal prefix", []string{"-c", "*"}, []string{"-x", "echo hi"}, false},
		{"middle * matches and re-anchors on literal", []string{"sh", "*", "exit"}, []string{"sh", "-c", "echo hi", "exit"}, true},
		{"middle * with literal that does not appear", []string{"sh", "*", "exit"}, []string{"sh", "-c", "echo hi"}, false},
		{"middle * matches when zero args between anchors", []string{"sh", "*", "exit"}, []string{"sh", "exit"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dynamicpathdetector.CompareExecArgs(tc.profile, tc.runtime); got != tc.want {
				t.Errorf("CompareExecArgs(%v, %v) = %v, want %v", tc.profile, tc.runtime, got, tc.want)
			}
		})
	}
}

func TestCompareExecArgs_MixedTokens(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		runtime []string
		want    bool
	}{
		{"⋯ then * — needs at least one arg before the *",
			[]string{"⋯", "*"}, []string{"a"}, true},
		{"⋯ then * — empty runtime fails (⋯ needs one)",
			[]string{"⋯", "*"}, nil, false},
		{"⋯ then * — many args ok",
			[]string{"⋯", "*"}, []string{"a", "b", "c"}, true},
		{"* then ⋯ — needs at least one arg for ⋯",
			[]string{"*", "⋯"}, []string{"x"}, true},
		{"* then ⋯ — empty runtime fails",
			[]string{"*", "⋯"}, nil, false},
		{"literal, ⋯, *  — typical user pattern",
			[]string{"--user", "⋯", "*"}, []string{"--user", "alice", "--verbose", "--out", "/tmp"}, true},
		{"literal, ⋯, *  — runtime too short for ⋯",
			[]string{"--user", "⋯", "*"}, []string{"--user"}, false},
		{"only ⋯, runtime empty — fails (⋯ requires exactly one)",
			[]string{"⋯"}, []string{}, false},
		{"only *, runtime empty — passes",
			[]string{"*"}, []string{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dynamicpathdetector.CompareExecArgs(tc.profile, tc.runtime); got != tc.want {
				t.Errorf("CompareExecArgs(%v, %v) = %v, want %v", tc.profile, tc.runtime, got, tc.want)
			}
		})
	}
}

func TestCompareExecArgs_RealisticPatterns(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		runtime []string
		want    bool
	}{
		{"curl with any URL", []string{"-s", "⋯"}, []string{"-s", "https://example.com"}, true},
		{"sh -c with any command",
			[]string{"-c", "*"},
			[]string{"-c", "while true; do sleep 1; done"},
			true,
		},
		{"echo with any number of words",
			[]string{"hello", "*"},
			[]string{"hello", "world", "from", "test"},
			true,
		},
		{"ls -l in arbitrary directory",
			[]string{"-l", "⋯"},
			[]string{"-l", "/var/log"},
			true,
		},
		{"ls without args fails wildcard arg pattern",
			[]string{"-l", "⋯"},
			[]string{"-l"},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dynamicpathdetector.CompareExecArgs(tc.profile, tc.runtime); got != tc.want {
				t.Errorf("CompareExecArgs(%v, %v) = %v, want %v", tc.profile, tc.runtime, got, tc.want)
			}
		})
	}
}

// TestCompareExecArgs_Argv0BareName pins the convention used by the
// node-agent recording side: profile.Args includes argv[0] as its
// first element, and argv[0] is the BARE program name as captured by
// eBPF (e.g. "sh", not "/bin/sh"). Profile.Path holds the resolved
// kernel exepath separately ("/bin/sh"), used for ap.was_executed
// lookup; the matcher never sees Path here.
//
// This fixes the contract mismatch that broke
// Test_32_UnexpectedProcessArguments: tests were authored with
// Args[0]="/bin/sh" assuming the matcher would normalise argv[0] to
// the resolved path, but CompareExecArgs does strict positional
// compare. With Args[0]=bare-name, position 0 matches runtime argv[0]
// directly and R0040 can be tested in isolation from R0001's path
// resolution.
//
// See also: node-agent/pkg/containerprofilemanager/v1/container_data
// .go (getExecs slicing exec=[path, ...argv]) and the recording site
// in event_reporting.go that builds exec=[resolveExecPath(...),
// ...event.GetArgs()].
func TestCompareExecArgs_Argv0BareName(t *testing.T) {
	cases := []struct {
		name    string
		profile []string
		runtime []string
		want    bool
	}{
		// 32a equivalent: sh -c MATCHES.
		{
			"sh -c <anything> matches [sh, -c, *]",
			[]string{"sh", "-c", "*"},
			[]string{"sh", "-c", "echo hi"},
			true,
		},
		// 32b equivalent: sh -x MISMATCHES at literal anchor "-c".
		{
			"sh -x <anything> fails [sh, -c, *] at position 1",
			[]string{"sh", "-c", "*"},
			[]string{"sh", "-x", "echo hi"},
			false,
		},
		// 32c equivalent: echo hello MATCHES.
		{
			"echo hello <words> matches [echo, hello, *]",
			[]string{"echo", "hello", "*"},
			[]string{"echo", "hello", "world", "from", "test"},
			true,
		},
		// 32d equivalent: echo goodbye MISMATCHES at literal anchor "hello".
		{
			"echo goodbye <words> fails [echo, hello, *] at position 1",
			[]string{"echo", "hello", "*"},
			[]string{"echo", "goodbye", "world"},
			false,
		},
		// argv[0] mismatch — caller wrote profile with FULL PATH at position 0
		// but runtime captured bare name. This used to silently pass the test
		// when run with the old Test_32 profile shape but mismatches at the
		// matcher level — the test that exposed it was Test_32's 32a (which
		// expected R0040 silent on a sh -c match, but R0040 always fired
		// because of this position-0 mismatch).
		{
			"profile Args[0]=full-path WRONG SHAPE — does not match bare-name argv[0]",
			[]string{"/bin/sh", "-c", "*"},
			[]string{"sh", "-c", "echo hi"},
			false,
		},
		// Inverse: profile bare, runtime full path. Equally a non-match.
		{
			"profile Args[0]=bare-name does not match full-path argv[0]",
			[]string{"sh", "-c", "*"},
			[]string{"/bin/sh", "-c", "echo hi"},
			false,
		},
		// curl -s <one URL> — ⋯ consumes exactly one position.
		{
			"curl -s <url> matches [curl, -s, ⋯]",
			[]string{"curl", "-s", "⋯"},
			[]string{"curl", "-s", "https://example.com"},
			true,
		},
		// curl -s <url> <extra> — ⋯ refuses the extra position.
		{
			"curl -s <url> <extra> fails [curl, -s, ⋯] at one-segment limit",
			[]string{"curl", "-s", "⋯"},
			[]string{"curl", "-s", "https://example.com", "--verbose"},
			false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dynamicpathdetector.CompareExecArgs(tc.profile, tc.runtime); got != tc.want {
				t.Errorf("CompareExecArgs(profile=%v, runtime=%v) = %v, want %v", tc.profile, tc.runtime, got, tc.want)
			}
		})
	}
}

// TestCompareExecArgs_ReDoSResistance pins that the matcher handles
// adversarial wildcard-heavy inputs in bounded time. The classic
// catastrophic-backtracking case is `[*, *, *, …, "literal"]` vs a
// long literal-runtime vector that mismatches the trailing literal
// — every prefix * has multiple split choices and the suffix
// mismatch only surfaces at the very end, so each path gets
// re-explored. With memoisation this is O(P*R); without it, naïve
// recursion would be exponential.
//
// CodeRabbit flagged the unmemoised version on PR #27 (Major).
func TestCompareExecArgs_ReDoSResistance(t *testing.T) {
	// Skip in short mode: this test has a wall-clock budget that is
	// inherently sensitive to runner CPU contention. The functional
	// regression intent is preserved — the memoisation correctness is
	// also covered by the explicit case-table tests above which always
	// run. CodeRabbit upstream PR #326 finding #5.
	if testing.Short() {
		t.Skip("skip timing-sensitive ReDoS regression in short mode")
	}
	// 20 leading wildcards + a literal that won't match. Without
	// memoisation, the naïve matcher tries roughly 2^20 path splits
	// before failing — observable as a many-second test. The
	// memoised version completes in microseconds.
	profile := make([]string, 0, 21)
	for i := 0; i < 20; i++ {
		profile = append(profile, dynamicpathdetector.WildcardIdentifier)
	}
	profile = append(profile, "needle-that-does-not-exist")

	runtime := make([]string, 0, 50)
	for i := 0; i < 50; i++ {
		runtime = append(runtime, "a")
	}

	start := time.Now()
	got := dynamicpathdetector.CompareExecArgs(profile, runtime)
	elapsed := time.Since(start)

	if got {
		t.Errorf("expected mismatch for trailing-literal that isn't in runtime")
	}
	// Memoised matcher: 21 * 51 = ~1100 states, each O(R) work for
	// the wildcard split → total bound ~50K ops. Generous budget of
	// 100ms catches any regression to the unmemoised form (which
	// would be measured in seconds, not milliseconds, on this input).
	if elapsed > 100*time.Millisecond {
		t.Errorf("matcher took %v on adversarial input — memoisation regression?", elapsed)
	}
}

// TestMatchExecArgs_ContractFourStates pins the four-state contract that
// ArgsRequired makes expressible (resolves the args,omitempty round-trip
// ambiguity matthyx blocked on PR #322):
//
//   1. ArgsRequired=false, profileArgs=nil  → no constraint (any runtime)
//   2. ArgsRequired=false, profileArgs=[…]  → no constraint (back-compat)
//   3. ArgsRequired=true,  profileArgs=[]   → MUST have empty runtime args
//   4. ArgsRequired=true,  profileArgs=[…]  → strict anchored match
//
// State (3) is the case the legacy CompareExecArgs API could not express;
// MatchExecArgs uses the explicit flag to disambiguate.
func TestMatchExecArgs_ContractFourStates(t *testing.T) {
	cases := []struct {
		name         string
		profileArgs  []string
		argsRequired bool
		runtimeArgs  []string
		want         bool
	}{
		// State 1: ArgsRequired=false, profile nil → no constraint
		{
			name: "ArgsRequired=false, profile=nil, runtime=nil → true (no constraint)",
			argsRequired: false, profileArgs: nil, runtimeArgs: nil, want: true,
		},
		{
			name: "ArgsRequired=false, profile=nil, runtime=non-empty → true (no constraint)",
			argsRequired: false, profileArgs: nil, runtimeArgs: []string{"foo", "bar"}, want: true,
		},

		// State 2: ArgsRequired=false, profile populated → no constraint (back-compat)
		{
			name: "ArgsRequired=false, profile=[x], runtime=anything → true (back-compat bypass)",
			argsRequired: false, profileArgs: []string{"x"}, runtimeArgs: []string{"completely", "different"}, want: true,
		},

		// State 3: ArgsRequired=true, profile=[] → runtime MUST be empty
		{
			name: "ArgsRequired=true, profile=[], runtime=[] → true (anchored empty-empty)",
			argsRequired: true, profileArgs: []string{}, runtimeArgs: []string{}, want: true,
		},
		{
			name: "ArgsRequired=true, profile=[], runtime=nil → true (nil == empty for runtime)",
			argsRequired: true, profileArgs: []string{}, runtimeArgs: nil, want: true,
		},
		{
			name: "ArgsRequired=true, profile=[], runtime=[x] → false (extra runtime arg)",
			argsRequired: true, profileArgs: []string{}, runtimeArgs: []string{"x"}, want: false,
		},
		{
			name: "ArgsRequired=true, profile=nil, runtime=[x] → false (anchored, profile vector empty)",
			argsRequired: true, profileArgs: nil, runtimeArgs: []string{"x"}, want: false,
		},

		// State 4: ArgsRequired=true, profile populated → strict anchored match
		{
			name: "ArgsRequired=true, exact literal match",
			argsRequired: true, profileArgs: []string{"-c", "echo hi"}, runtimeArgs: []string{"-c", "echo hi"}, want: true,
		},
		{
			name: "ArgsRequired=true, exact literal mismatch",
			argsRequired: true, profileArgs: []string{"-c", "echo hi"}, runtimeArgs: []string{"-c", "echo bye"}, want: false,
		},
		{
			name: "ArgsRequired=true, trailing wildcard absorbs",
			argsRequired: true, profileArgs: []string{"-c", "*"}, runtimeArgs: []string{"-c", "echo", "hi"}, want: true,
		},
		{
			name: "ArgsRequired=true, dynamic identifier matches one position",
			argsRequired: true, profileArgs: []string{"-c", "⋯", "hi"}, runtimeArgs: []string{"-c", "anything", "hi"}, want: true,
		},
		{
			name: "ArgsRequired=true, dynamic identifier requires exactly one — fails on missing",
			argsRequired: true, profileArgs: []string{"-c", "⋯", "hi"}, runtimeArgs: []string{"-c", "hi"}, want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := dynamicpathdetector.MatchExecArgs(tc.profileArgs, tc.argsRequired, tc.runtimeArgs)
			if got != tc.want {
				t.Errorf("MatchExecArgs(%v, argsRequired=%v, %v) = %v, want %v",
					tc.profileArgs, tc.argsRequired, tc.runtimeArgs, got, tc.want)
			}
		})
	}
}

// TestMatchExecArgs_BackCompatVsCompareExecArgs proves the back-compat
// path: MatchExecArgs(args, false, runtime) ≡ CompareExecArgs(args, runtime)
// for every state where CompareExecArgs has a definite answer. This is
// the invariant that lets bob (and other callers that haven't migrated
// to MatchExecArgs) keep using CompareExecArgs without behavioural drift.
func TestMatchExecArgs_BackCompatVsCompareExecArgs(t *testing.T) {
	cases := []struct {
		profile []string
		runtime []string
	}{
		{nil, nil},
		{nil, []string{"anything"}},
		{[]string{}, nil},
		{[]string{"-c", "echo"}, []string{"-c", "echo"}},
		{[]string{"-c", "echo"}, []string{"-c", "different"}},
		{[]string{"-c", "*"}, []string{"-c", "a", "b", "c"}},
		{[]string{"⋯"}, []string{"x"}},
		{[]string{"⋯"}, []string{}},
	}
	for _, c := range cases {
		oldCompare := dynamicpathdetector.CompareExecArgs(c.profile, c.runtime)
		// MatchExecArgs with argsRequired=false has the same bypass for empty
		// profile as CompareExecArgs; for non-empty profile both go via the
		// shared strict matcher, which gives the same answer too — UNLESS
		// the profile is non-empty AND the caller passed argsRequired=false
		// which is "no constraint" → MatchExecArgs returns true. So we test
		// the CompareExecArgs-equivalent path: argsRequired = (len(profile) > 0).
		argsRequired := len(c.profile) > 0
		newMatch := dynamicpathdetector.MatchExecArgs(c.profile, argsRequired, c.runtime)
		if oldCompare != newMatch {
			t.Errorf("back-compat mismatch profile=%v runtime=%v: CompareExecArgs=%v, MatchExecArgs(argsRequired=%v)=%v",
				c.profile, c.runtime, oldCompare, argsRequired, newMatch)
		}
	}
}
