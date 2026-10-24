package dynamicpathdetector

import (
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func hostLikeExecs(literals, patterns int) []types.ExecCalls {
	execs := make([]types.ExecCalls, 0, literals+patterns)
	for i := 0; i < literals; i++ {
		execs = append(execs, types.ExecCalls{
			Path: fmt.Sprintf("/usr/bin/tool%d", i%50),
			Args: []string{fmt.Sprintf("tool%d", i%50), "--config", fmt.Sprintf("/etc/app/%d/conf.yaml", i), "--id", fmt.Sprintf("%d", 100000+i), "run"},
			Envs: []string{fmt.Sprintf("JOB=%d", i)},
		})
	}
	for p := 0; p < patterns; p++ {
		args := []string{fmt.Sprintf("tool%d", p%50), "--config", "/etc/app/" + DynamicIdentifier + "/conf.yaml", "--id", DynamicIdentifier, "run"}
		if p%2 == 0 {
			args = []string{fmt.Sprintf("tool%d", p%50), ExecArgsWildcard, "run"}
		}
		execs = append(execs, types.ExecCalls{Path: fmt.Sprintf("/usr/bin/tool%d", p%50), Args: args})
	}
	return dedupeByArgv(execs)
}

func BenchmarkConsolidateExecsHostProfile(b *testing.B) {
	execs := hostLikeExecs(4000, 300)
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
