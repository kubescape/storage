package dynamicpathdetector

import (
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

const (
	hostLikeTools    = 50
	hostLikeVariants = 6
)

func hostLikePattern(tool string, variant int) []string {
	conf := "/etc/app/" + DynamicIdentifier + "/conf.yaml"
	switch variant % hostLikeVariants {
	case 0:
		return []string{tool, "--config", conf, "--id", DynamicIdentifier, "run"}
	case 1:
		return []string{tool, ExecArgsWildcard, "run"}
	case 2:
		return []string{tool, "--config", ExecArgsWildcard, "run"}
	case 3:
		return []string{tool, ExecArgsWildcard, "--id", DynamicIdentifier, "run"}
	case 4:
		return []string{tool, "--config", conf, ExecArgsWildcard}
	default:
		return []string{tool, ExecArgsWildcard}
	}
}

func hostLikeExecs(literals, patterns int) []types.ExecCalls {
	execs := make([]types.ExecCalls, 0, literals+patterns)
	for i := 0; i < literals; i++ {
		execs = append(execs, types.ExecCalls{
			Path: fmt.Sprintf("/usr/bin/tool%d", i%hostLikeTools),
			Args: []string{fmt.Sprintf("tool%d", i%hostLikeTools), "--config", fmt.Sprintf("/etc/app/%d/conf.yaml", i), "--id", fmt.Sprintf("%d", 100000+i), "run"},
			Envs: []string{fmt.Sprintf("JOB=%d", i)},
		})
	}
	for p := 0; p < patterns; p++ {
		tool := fmt.Sprintf("tool%d", p%hostLikeTools)
		execs = append(execs, types.ExecCalls{Path: "/usr/bin/" + tool, Args: hostLikePattern(tool, p/hostLikeTools)})
	}
	return dedupeByArgv(execs)
}

func BenchmarkConsolidateExecsHostProfile(b *testing.B) {
	execs := hostLikeExecs(4000, 300)
	if len(execs) != 4300 {
		b.Fatalf("generator produced %d distinct entries, want 4000 literals + 300 patterns", len(execs))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = consolidateExecs(execs)
	}
}

func BenchmarkMatchExecArgsStrictWildcard(b *testing.B) {
	profile := []string{"tool", ExecArgsWildcard, "--id", DynamicIdentifier, "run"}
	runtime := []string{"tool", "--config", "/etc/app/7/conf.yaml", "--verbose", "--id", "100007", "run"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !matchExecArgsStrict(profile, runtime) {
			b.Fatal("expected a match")
		}
	}
}
