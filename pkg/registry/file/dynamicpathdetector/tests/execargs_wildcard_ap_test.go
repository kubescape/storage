package dynamicpathdetectortests

import (
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	dp "github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

// These tests build real ApplicationProfile objects whose recorded exec args
// carry either a WILDCARD ("⋯" = any one arg/segment, "⋯⋯" = zero-or-more args)
// or a LITERAL "*", and drive the production matcher through them exactly as
// node-agent's was_executed_with_args does (per-vector MatchExecArgs with
// ArgsRequired=true). The point: a "*" recorded in argv is data and does NOT
// broaden, while the dedicated "⋯"/"⋯⋯" sentinels are the only wildcards.

// execAP builds a one-container ApplicationProfile with a single recorded exec.
func execAP(args []string) *types.ApplicationProfile {
	return &types.ApplicationProfile{
		Spec: types.ApplicationProfileSpec{
			Containers: []types.ApplicationProfileContainer{{
				Name: "app",
				Execs: []types.ExecCalls{
					{Path: "/usr/bin/tool", Args: args, ArgsRequired: true},
				},
			}},
		},
	}
}

// matchAP mimics node-agent: for each recorded exec vector in the container,
// MatchExecArgs(profileArgs, true, runtimeArgs); allowed if ANY vector matches.
func matchAP(ap *types.ApplicationProfile, container string, runtime []string) bool {
	for _, c := range ap.Spec.Containers {
		if c.Name != container {
			continue
		}
		for _, e := range c.Execs {
			if dp.MatchExecArgs(e.Args, e.ArgsRequired, runtime) {
				return true
			}
		}
	}
	return false
}

func TestAP_LiteralStarArg_DoesNotBroaden(t *testing.T) {
	// Recorded: the tool was invoked with the LITERAL arg "/plugins/*"
	// (e.g. a shell glob that didn't expand). Stored verbatim — "*" is data.
	ap := execAP([]string{"/usr/bin/tool", "--load", "/plugins/*"})

	cases := []struct {
		name    string
		runtime []string
		allowed bool
	}{
		{"exact literal /plugins/* is allowed", []string{"/usr/bin/tool", "--load", "/plugins/*"}, true},
		{"different plugin must NOT be allowed (no broaden)", []string{"/usr/bin/tool", "--load", "/plugins/evil.so"}, false},
		{"child path must NOT be allowed", []string{"/usr/bin/tool", "--load", "/plugins/a/b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchAP(ap, "app", c.runtime); got != c.allowed {
				t.Errorf("literal-* AP match(%q) = %v, want %v", c.runtime, got, c.allowed)
			}
		})
	}
}

func TestAP_DynamicArg_IsSingleSegmentWildcard(t *testing.T) {
	// Authored as a real wildcard: any single plugin filename under /plugins/.
	ap := execAP([]string{"/usr/bin/tool", "--load", "/plugins/⋯"})

	cases := []struct {
		name    string
		runtime []string
		allowed bool
	}{
		{"any single-segment plugin is allowed", []string{"/usr/bin/tool", "--load", "/plugins/foo.so"}, true},
		{"another single-segment plugin is allowed", []string{"/usr/bin/tool", "--load", "/plugins/evil.so"}, true},
		{"a deeper path is NOT allowed (⋯ is one segment)", []string{"/usr/bin/tool", "--load", "/plugins/a/b"}, false},
		{"unrelated path is NOT allowed", []string{"/usr/bin/tool", "--load", "/etc/passwd"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchAP(ap, "app", c.runtime); got != c.allowed {
				t.Errorf("⋯-arg AP match(%q) = %v, want %v", c.runtime, got, c.allowed)
			}
		})
	}
}

func TestAP_MultiArgWildcard_AbsorbsTail(t *testing.T) {
	// Authored: tool --load <one plugin> <any trailing flags...>.
	ap := execAP([]string{"/usr/bin/tool", "--load", "⋯", dp.ExecArgsWildcard})

	cases := []struct {
		name    string
		runtime []string
		allowed bool
	}{
		{"no trailing args is allowed (⋯⋯ absorbs zero)", []string{"/usr/bin/tool", "--load", "p.so"}, true},
		{"trailing flags are absorbed", []string{"/usr/bin/tool", "--load", "p.so", "--verbose", "--out", "/tmp"}, true},
		{"missing the required single plugin arg is NOT allowed", []string{"/usr/bin/tool", "--load"}, false},
		{"wrong literal prefix is NOT allowed", []string{"/usr/bin/tool", "--exec", "p.so"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchAP(ap, "app", c.runtime); got != c.allowed {
				t.Errorf("⋯⋯-tail AP match(%q) = %v, want %v", c.runtime, got, c.allowed)
			}
		})
	}
}

// TestAP_LiteralStarVsDynamic_DivergeOnSameInput is the crux: the identical
// runtime exec "/plugins/evil.so" is BLOCKED by the literal-"*" profile and
// ALLOWED by the "⋯"-wildcard profile — "*" is data, "⋯" is the wildcard.
func TestAP_LiteralStarVsDynamic_DivergeOnSameInput(t *testing.T) {
	runtime := []string{"/usr/bin/tool", "--load", "/plugins/evil.so"}
	literalStar := execAP([]string{"/usr/bin/tool", "--load", "/plugins/*"})
	dynamic := execAP([]string{"/usr/bin/tool", "--load", "/plugins/⋯"})

	if matchAP(literalStar, "app", runtime) {
		t.Error("literal-* AP must NOT allow /plugins/evil.so (R0040 fires)")
	}
	if !matchAP(dynamic, "app", runtime) {
		t.Error("⋯-wildcard AP must allow /plugins/evil.so")
	}
}
