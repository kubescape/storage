/*
Copyright 2024 The Kubescape Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package dynamicpathdetectortests

import (
	"fmt"
	"testing"

	types "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	dyn     = dynamicpathdetector.DynamicIdentifier
	anyArgs = dynamicpathdetector.ExecArgsWildcard
)

func exec(path string, args ...string) types.ExecCalls {
	return types.ExecCalls{Path: path, Args: args}
}

func execArgs(execs []types.ExecCalls) [][]string {
	out := make([][]string, len(execs))
	for i, e := range execs {
		out[i] = e.Args
	}
	return out
}

// Stored profiles encode empty execs as [] rather than null (the codec
// fidelity contract DeflateStringer kept), so empty input yields a non-nil
// empty slice.
func TestAnalyzeExecs_EmptyInputIsNonNilEmpty(t *testing.T) {
	for _, in := range [][]types.ExecCalls{nil, {}} {
		out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
		assert.NotNil(t, out)
		assert.Empty(t, out)
	}
}

func TestAnalyzeExecs_BelowThresholdKeepsLiteralsAndDedupes(t *testing.T) {
	in := []types.ExecCalls{
		exec("/usr/bin/ls", "/usr/bin/ls", "-la"),
		exec("/usr/bin/cat", "/usr/bin/cat", "/etc/hosts"),
		exec("/usr/bin/ls", "/usr/bin/ls", "-la"),
		exec("/usr/bin/ls", "/usr/bin/ls", "/tmp"),
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.Equal(t, []types.ExecCalls{
		exec("/usr/bin/cat", "/usr/bin/cat", "/etc/hosts"),
		exec("/usr/bin/ls", "/usr/bin/ls", "-la"),
		exec("/usr/bin/ls", "/usr/bin/ls", "/tmp"),
	}, out)
}

func TestAnalyzeExecs_CollapsesHighVarietyPosition(t *testing.T) {
	var in []types.ExecCalls
	for i := 0; i < 4; i++ {
		in = append(in, exec("/usr/bin/grep", "grep", fmt.Sprintf("pattern-%d", i)))
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.Equal(t, []types.ExecCalls{exec("/usr/bin/grep", "grep", dyn)}, out)
}

func TestAnalyzeExecs_DifferentArgCountsDoNotMerge(t *testing.T) {
	var in []types.ExecCalls
	for i := 0; i < 4; i++ {
		in = append(in, exec("/usr/bin/grep", "grep", fmt.Sprintf("pattern-%d", i)))
	}
	in = append(in, exec("/usr/bin/grep", "grep", "-E", "^-A"))
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.ElementsMatch(t, [][]string{
		{"grep", dyn},
		{"grep", "-E", "^-A"},
	}, execArgs(out))
}

func TestAnalyzeExecs_CollapsesOnlyTheVaryingLevel(t *testing.T) {
	var in []types.ExecCalls
	for _, mode := range []string{"+x", "755"} {
		for i := 0; i < 5; i++ {
			in = append(in, exec("/usr/bin/chmod", "chmod", mode, fmt.Sprintf("/tmp/file-%d", i)))
		}
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.ElementsMatch(t, [][]string{
		{"chmod", "+x", dyn},
		{"chmod", "755", dyn},
	}, execArgs(out))
}

func TestAnalyzeExecs_NeverCollapsesArgv0(t *testing.T) {
	var in []types.ExecCalls
	for _, argv0 := range []string{"sh", "/bin/sh"} {
		for i := 0; i < 4; i++ {
			in = append(in, exec("/usr/bin/dash", argv0, fmt.Sprintf("script-%d", i)))
		}
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.ElementsMatch(t, [][]string{
		{"sh", dyn},
		{"/bin/sh", dyn},
	}, execArgs(out))
}

func TestAnalyzeExecs_PerBinaryCeilingFallsBackToAnyArgs(t *testing.T) {
	// Three different argv lengths produce three patterns, over a threshold of 2.
	in := []types.ExecCalls{
		exec("/usr/bin/bash", "bash"),
		exec("/usr/bin/bash", "bash", "-c", "id"),
		exec("/usr/bin/bash", "bash", "/opt/setup.sh", "-c", "run"),
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(2, nil))
	assert.Equal(t, []types.ExecCalls{exec("/usr/bin/bash", "bash", anyArgs)}, out)
}

func TestAnalyzeExecs_PassesThroughArgsRequiredAndEmptyArgs(t *testing.T) {
	var in []types.ExecCalls
	for i := 0; i < 4; i++ {
		e := exec("/usr/bin/curl", "curl", fmt.Sprintf("https://host-%d", i))
		e.ArgsRequired = true
		in = append(in, e)
	}
	in = append(in, types.ExecCalls{Path: "/usr/bin/true"})
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.ElementsMatch(t, in, out)
}

func TestAnalyzeExecs_UnionsEnvsOfMergedEntries(t *testing.T) {
	var in []types.ExecCalls
	for i := 0; i < 4; i++ {
		e := exec("/usr/bin/grep", "grep", fmt.Sprintf("pattern-%d", i))
		e.Envs = []string{fmt.Sprintf("VAR%d=1", i%2)}
		in = append(in, e)
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	require.Len(t, out, 1)
	assert.Equal(t, []string{"VAR0=1", "VAR1=1"}, out[0].Envs)
}

func TestAnalyzeExecs_ExistingPatternAbsorbsNewLiteral(t *testing.T) {
	in := []types.ExecCalls{
		exec("/usr/bin/grep", "grep", dyn),
		exec("/usr/bin/grep", "grep", "new-pattern"),
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.Equal(t, []types.ExecCalls{exec("/usr/bin/grep", "grep", dyn)}, out)
}

func TestAnalyzeExecs_AnyArgsPatternAbsorbsLiterals(t *testing.T) {
	in := []types.ExecCalls{
		exec("/usr/bin/bash", "bash", anyArgs),
		exec("/usr/bin/bash", "bash", "-c", "getconf _NPROCESSORS_ONLN"),
		exec("/usr/bin/bash", "bash"),
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.Equal(t, []types.ExecCalls{exec("/usr/bin/bash", "bash", anyArgs)}, out)
}

func TestAnalyzeExecs_CollapseConfigsOverrideThresholdPerBinary(t *testing.T) {
	var in []types.ExecCalls
	for i := 0; i < 4; i++ {
		in = append(in, exec("/usr/bin/grep", "grep", fmt.Sprintf("pattern-%d", i)))
		in = append(in, exec("/usr/bin/sed", "sed", fmt.Sprintf("s/%d//", i)))
	}
	analyzer := dynamicpathdetector.NewExecAnalyzer(3, []dynamicpathdetector.CollapseConfig{
		{Prefix: "/usr/bin/grep", Threshold: 10},
	})
	out := dynamicpathdetector.AnalyzeExecs(in, analyzer)
	var grepCount int
	for _, e := range out {
		if e.Path == "/usr/bin/grep" {
			grepCount++
		}
	}
	assert.Equal(t, 4, grepCount, "grep kept literal under its per-binary threshold")
	assert.Contains(t, out, exec("/usr/bin/sed", "sed", dyn), "sed collapsed at the default threshold")
}

func TestAnalyzeExecs_NonPositiveThresholdUsesDefault(t *testing.T) {
	var in []types.ExecCalls
	for i := 0; i < dynamicpathdetector.ExecDynamicThreshold; i++ {
		in = append(in, exec("/usr/bin/grep", "grep", fmt.Sprintf("pattern-%d", i)))
	}
	out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(0, nil))
	assert.Len(t, out, dynamicpathdetector.ExecDynamicThreshold, "at the default threshold nothing collapses")
}

func TestAnalyzeExecs_Idempotent(t *testing.T) {
	in := execFixture()
	analyzer := dynamicpathdetector.NewExecAnalyzer(3, nil)
	once := dynamicpathdetector.AnalyzeExecs(in, analyzer)
	twice := dynamicpathdetector.AnalyzeExecs(once, dynamicpathdetector.NewExecAnalyzer(3, nil))
	assert.Equal(t, once, twice)
}

func TestAnalyzeExecs_EveryInputStillMatches(t *testing.T) {
	in := execFixture()
	for _, threshold := range []int{1, 2, 3, 5, 50} {
		out := dynamicpathdetector.AnalyzeExecs(in, dynamicpathdetector.NewExecAnalyzer(threshold, nil))
		for _, e := range in {
			covered := false
			for _, o := range out {
				if o.Path == e.Path && dynamicpathdetector.CompareExecArgs(o.Args, e.Args) {
					covered = true
					break
				}
			}
			assert.True(t, covered, "threshold %d: %s %v not covered by output", threshold, e.Path, e.Args)
		}
	}
}

// execFixture mixes collapsible and literal argvs across several binaries,
// argv lengths and nesting levels.
func execFixture() []types.ExecCalls {
	var in []types.ExecCalls
	for i := 0; i < 6; i++ {
		in = append(in, exec("/usr/bin/grep", "grep", fmt.Sprintf("p%d", i)))
		in = append(in, exec("/usr/bin/grep", "grep", "-E", fmt.Sprintf("p%d", i)))
		in = append(in, exec("/usr/bin/chmod", "chmod", "+x", fmt.Sprintf("/tmp/f%d", i)))
		in = append(in, exec("/usr/bin/find", "find", fmt.Sprintf("/d%d", i%3), "-name", fmt.Sprintf("n%d", i)))
	}
	in = append(in,
		exec("/usr/bin/bash", "bash"),
		exec("/usr/bin/bash", "bash", "-c", "id"),
		exec("/usr/bin/bash", "bash", "/opt/setup.sh", "-c", "run"),
		exec("/usr/bin/ls", "ls", "-la"),
	)
	return in
}
