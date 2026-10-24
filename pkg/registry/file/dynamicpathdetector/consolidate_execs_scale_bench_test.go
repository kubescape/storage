package dynamicpathdetector

import (
	"fmt"
	"testing"
	"time"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

func hostScaleExecs(literals, patterns, maxArgs int) []types.ExecCalls {
	execs := make([]types.ExecCalls, 0, literals+patterns)
	for i := 0; i < literals; i++ {
		n := 3 + i%(maxArgs-2)
		args := make([]string, 0, n)
		args = append(args, fmt.Sprintf("bin%d", i%400))
		for k := 1; k < n; k++ {
			switch k % 3 {
			case 0:
				args = append(args, fmt.Sprintf("/var/lib/app/%d/part%d", i, k))
			case 1:
				args = append(args, fmt.Sprintf("--flag%d", k))
			default:
				args = append(args, fmt.Sprintf("%d", 10000+i*7+k))
			}
		}
		execs = append(execs, types.ExecCalls{Path: fmt.Sprintf("/usr/bin/bin%d", i%400), Args: args, Envs: []string{fmt.Sprintf("E=%d", i%50)}})
	}
	for p := 0; p < patterns; p++ {
		args := []string{fmt.Sprintf("bin%d", p%400), "--flag1", "/var/lib/app/" + DynamicIdentifier + "/part3", DynamicIdentifier}
		if p%3 == 0 {
			args = []string{fmt.Sprintf("bin%d", p%400), ExecArgsWildcard, DynamicIdentifier}
		}
		execs = append(execs, types.ExecCalls{Path: fmt.Sprintf("/usr/bin/bin%d", p%400), Args: args})
	}
	return dedupeByArgv(execs)
}

func TestConsolidateExecsHostScaleTiming(t *testing.T) {
	execs := hostScaleExecs(22500, 1200, 20)
	start := time.Now()
	out := consolidateExecs(execs)
	t.Logf("consolidateExecs over %d entries (%d patterns): %s, kept %d", len(execs), 1200, time.Since(start), len(out))
}
