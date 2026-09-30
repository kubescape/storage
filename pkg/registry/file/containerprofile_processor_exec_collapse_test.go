package file

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeflateContainerProfileSpec_CollapsesExecArgs pins that the deflate path
// runs AnalyzeExecs with the settings' ExecDynamicThreshold and the shared
// CollapseConfigs, for both the flat spec and authored container sections.
func TestDeflateContainerProfileSpec_CollapsesExecArgs(t *testing.T) {
	var execs []softwarecomposition.ExecCalls
	for i := 0; i < 4; i++ {
		execs = append(execs,
			softwarecomposition.ExecCalls{Path: "/usr/bin/grep", Args: []string{"grep", fmt.Sprintf("pattern-%d", i)}},
			softwarecomposition.ExecCalls{Path: "/usr/bin/sed", Args: []string{"sed", fmt.Sprintf("s/%d//", i)}},
		)
	}
	settings := dynamicpathdetector.DefaultCollapseSettings()
	settings.ExecDynamicThreshold = 3
	settings.CollapseConfigs = append(settings.CollapseConfigs, dynamicpathdetector.CollapseConfig{Prefix: "/usr/bin/sed", Threshold: 10})

	spec := softwarecomposition.ContainerProfileSpec{
		Execs:      execs,
		Containers: []softwarecomposition.ContainerProfileContainer{{Name: "app", Execs: execs}},
	}
	result := DeflateContainerProfileSpec(spec, nil, settings)

	want := []softwarecomposition.ExecCalls{{Path: "/usr/bin/grep", Args: []string{"grep", dynamicpathdetector.DynamicIdentifier}}}
	for i := 0; i < 4; i++ {
		want = append(want, softwarecomposition.ExecCalls{Path: "/usr/bin/sed", Args: []string{"sed", fmt.Sprintf("s/%d//", i)}})
	}
	assert.Equal(t, want, result.Execs, "flat spec: grep collapses at 3, sed kept under its /usr/bin/sed override")
	require.Len(t, result.Containers, 1)
	assert.Equal(t, want, result.Containers[0].Execs, "authored container sections deflate execs the same way")
}
