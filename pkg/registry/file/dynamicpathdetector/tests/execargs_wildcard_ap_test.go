package dynamicpathdetectortests

import (
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	dp "github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

// These tests build real ContainerProfile objects whose recorded exec args
// carry either a WILDCARD ("⋯" = any one arg/segment, "⋯⋯" = zero-or-more args)
// or a LITERAL "*", and drive the production matcher through them exactly as
// node-agent's was_executed_with_args does (per-vector MatchExecArgs with
// ArgsRequired=true). The point: a "*" recorded in argv is data and does NOT
// broaden, while the dedicated "⋯"/"⋯⋯" sentinels are the only wildcards.

// execCP builds a ContainerProfile with a single recorded exec.
func execCP(args []string) *types.ContainerProfile {
	return &types.ContainerProfile{
		Spec: types.ContainerProfileSpec{
			Execs: []types.ExecCalls{
				{Path: "/usr/bin/tool", Args: args, ArgsRequired: true},
			},
		},
	}
}

// matchCP mimics node-agent: for each recorded exec vector in the profile,
// MatchExecArgs(profileArgs, true, runtimeArgs); allowed if ANY vector matches.
func matchCP(cp *types.ContainerProfile, runtime []string) bool {
	for _, e := range cp.Spec.Execs {
		if dp.MatchExecArgs(e.Args, e.ArgsRequired, runtime) {
			return true
		}
	}
	return false
}

func TestAP_LiteralStarArg_DoesNotBroaden(t *testing.T) {
	// Recorded: the tool was invoked with the LITERAL arg "/plugins/*"
	// (e.g. a shell glob that didn't expand). Stored verbatim — "*" is data.
	cp := execCP([]string{"/usr/bin/tool", "--load", "/plugins/*"})

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
			if got := matchCP(cp, c.runtime); got != c.allowed {
				t.Errorf("literal-* match(%q) = %v, want %v", c.runtime, got, c.allowed)
			}
		})
	}
}

func TestAP_DynamicArg_IsSingleSegmentWildcard(t *testing.T) {
	// Authored as a real wildcard: any single plugin filename under /plugins/.
	cp := execCP([]string{"/usr/bin/tool", "--load", "/plugins/⋯"})

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
			if got := matchCP(cp, c.runtime); got != c.allowed {
				t.Errorf("⋯-arg match(%q) = %v, want %v", c.runtime, got, c.allowed)
			}
		})
	}
}

func TestAP_MultiArgWildcard_AbsorbsTail(t *testing.T) {
	// Authored: tool --load <one plugin> <any trailing flags...>.
	cp := execCP([]string{"/usr/bin/tool", "--load", "⋯", dp.ExecArgsWildcard})

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
			if got := matchCP(cp, c.runtime); got != c.allowed {
				t.Errorf("⋯⋯-tail match(%q) = %v, want %v", c.runtime, got, c.allowed)
			}
		})
	}
}

// TestAP_LiteralStarVsDynamic_DivergeOnSameInput is the crux: the identical
// runtime exec "/plugins/evil.so" is BLOCKED by the literal-"*" profile and
// ALLOWED by the "⋯"-wildcard profile — "*" is data, "⋯" is the wildcard.
func TestAP_LiteralStarVsDynamic_DivergeOnSameInput(t *testing.T) {
	runtime := []string{"/usr/bin/tool", "--load", "/plugins/evil.so"}
	literalStar := execCP([]string{"/usr/bin/tool", "--load", "/plugins/*"})
	dynamic := execCP([]string{"/usr/bin/tool", "--load", "/plugins/⋯"})

	if matchCP(literalStar, runtime) {
		t.Error("literal-* profile must NOT allow /plugins/evil.so (R0040 fires)")
	}
	if !matchCP(dynamic, runtime) {
		t.Error("⋯-wildcard profile must allow /plugins/evil.so")
	}
}
