package file

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConsolidateContainerProfileGolden is a golden-regression oracle for the
// time-series -> consolidated ContainerProfile transformation.
//
// It pins the two-stage consolidation contract that the processor runs on every
// ConsolidateTimeSeries pass, using the same package-level entry points the
// production path uses:
//
//  1. accumulation: mergeContainerProfileTS folds each successive learning-window
//     ContainerProfile into the running profile (append semantics — no dedup yet);
//  2. collapse: DeflateContainerProfileSpec deflates the accumulated spec — opens
//     wildcarding + SBOM subsumption, endpoint merge, exec/syscall/capability/arch
//     dedup+sort, rule-policy union, and network-neighbor CIDR collapse.
//
// The frozen golden (testdata/consolidate_golden/consolidated.golden.json) is the
// exact consolidated ContainerProfileSpec emitted by the current code for the
// window_*.json fixtures. Any drift in the consolidation contract — a change in
// wildcard thresholds, dedup ordering, CIDR floor behaviour, merge precedence —
// fails this test loudly.
//
// This is a single-path golden-regression oracle (frozen output), NOT a true
// two-path differential: it does not re-derive the expected output from an
// independent implementation. It catches unintended drift, not "the frozen value
// itself is wrong". Regenerate deliberately with UPDATE_GOLDEN=1 after an
// intended contract change and review the diff.
//
// The CollapseSettings below are pinned inline (not DefaultCollapseSettings) so
// the oracle is insulated from later default-threshold retuning: the thresholds
// are small on purpose so the compact fixtures still cross them and exercise the
// collapse branches (opens wildcard at 5 distinct dynamic children, network CIDR
// collapse at a 4-host group floored to /24).
func TestConsolidateContainerProfileGolden(t *testing.T) {
	const goldenDir = "testdata/consolidate_golden"
	windows := []string{"window_1.json", "window_2.json", "window_3.json"}

	settings := dynamicpathdetector.CollapseSettings{
		OpenDynamicThreshold:     5,
		EndpointDynamicThreshold: 100,
		CollapseConfigs:          nil, // flat threshold only — no per-prefix overrides
		NetworkIPGroupThreshold:  4,
		NetworkCIDRFloorBits:     24,
	}

	// Stage 1 — accumulate the time-series windows in report order, exactly as
	// mergeTimeSeriesData does when folding each TS profile into the running one.
	var accumulated *softwarecomposition.ContainerProfile
	for _, w := range windows {
		content, err := os.ReadFile(filepath.Join(goldenDir, w))
		require.NoError(t, err, "read fixture %s", w)
		var profile softwarecomposition.ContainerProfile
		require.NoError(t, json.Unmarshal(content, &profile), "unmarshal fixture %s", w)
		if accumulated == nil {
			accumulated = &profile
			continue
		}
		mergeContainerProfileTS(accumulated, &profile)
	}
	require.NotNil(t, accumulated, "no fixtures loaded")

	// Stage 2 — collapse the accumulated spec (the real deflate entry point).
	consolidated := DeflateContainerProfileSpec(accumulated.Spec, nil, settings)

	got, err := json.MarshalIndent(consolidated, "", "  ")
	require.NoError(t, err)
	got = append(got, '\n')

	goldenPath := filepath.Join(goldenDir, "consolidated.golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.WriteFile(goldenPath, got, 0o644))
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	want, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "read golden (regenerate with UPDATE_GOLDEN=1)")

	assert.Equal(t, string(want), string(got),
		"consolidated ContainerProfileSpec diverged from frozen golden %s; if intentional, regenerate with UPDATE_GOLDEN=1 and review the diff", goldenPath)
}
