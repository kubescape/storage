package dynamicpathdetector

import (
	"fmt"
	"testing"
	"time"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

const (
	hostScaleBins       = 400
	hostScaleMinArgs    = 3
	hostScaleLiterals   = 22500
	hostScalePatterns   = 1200
	hostScaleMaxArgs    = 20
	hostScaleLengthStep = 6
)

func hostScaleLiteralToken(i, k int) string {
	switch k % 3 {
	case 0:
		return fmt.Sprintf("/var/lib/app/%d/part%d", i, k)
	case 1:
		return fmt.Sprintf("--flag%d", k)
	default:
		return fmt.Sprintf("%d", 10000+i*7+k)
	}
}

func hostScalePatternToken(k int) string {
	switch k % 3 {
	case 0:
		return "/var/lib/app/" + DynamicIdentifier + fmt.Sprintf("/part%d", k)
	case 1:
		return fmt.Sprintf("--flag%d", k)
	default:
		return DynamicIdentifier
	}
}

func hostScaleExecs(literals, patterns, maxArgs int) []types.ExecCalls {
	execs := make([]types.ExecCalls, 0, literals+patterns)
	lengths := maxArgs - hostScaleMinArgs + 1
	for i := 0; i < literals; i++ {
		n := hostScaleMinArgs + i%lengths
		args := make([]string, 0, n)
		args = append(args, fmt.Sprintf("bin%d", i%hostScaleBins))
		for k := 1; k < n; k++ {
			args = append(args, hostScaleLiteralToken(i, k))
		}
		execs = append(execs, types.ExecCalls{Path: fmt.Sprintf("/usr/bin/bin%d", i%hostScaleBins), Args: args, Envs: []string{fmt.Sprintf("E=%d", i%50)}})
	}
	for p := 0; p < patterns; p++ {
		bin := p % hostScaleBins
		n := hostScaleMinArgs + ((p/hostScaleBins)*hostScaleLengthStep+bin%hostScaleLengthStep)%lengths
		args := make([]string, 0, n)
		args = append(args, fmt.Sprintf("bin%d", bin))
		if p%3 == 0 {
			args = append(args, hostScalePatternToken(1), ExecArgsWildcard, hostScalePatternToken(n-1))
		} else {
			for k := 1; k < n; k++ {
				args = append(args, hostScalePatternToken(k))
			}
		}
		execs = append(execs, types.ExecCalls{Path: fmt.Sprintf("/usr/bin/bin%d", bin), Args: args})
	}
	return dedupeByArgv(execs)
}

func TestConsolidateExecsHostScaleTiming(t *testing.T) {
	execs := hostScaleExecs(hostScaleLiterals, hostScalePatterns, hostScaleMaxArgs)
	if len(execs) != hostScaleLiterals+hostScalePatterns {
		t.Fatalf("generator produced %d distinct entries, want %d literals + %d patterns", len(execs), hostScaleLiterals, hostScalePatterns)
	}
	start := time.Now()
	out := consolidateExecs(execs)
	t.Logf("consolidateExecs over %d entries (%d patterns): %s, kept %d", len(execs), hostScalePatterns, time.Since(start), len(out))
}
