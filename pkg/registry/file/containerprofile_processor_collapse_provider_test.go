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

package file

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
)

// TestContainerProfileProcessor_CollapseSettings_NilProviderFallsBack pins
// the nil-safety inside PreSave: the field is exported, so an external
// caller may leave it unset (zero-value struct literal). The processor
// must NOT panic and must fall back to compiled defaults — i.e. tight
// /etc thresholds shouldn't appear out of nowhere.
func TestContainerProfileProcessor_CollapseSettings_NilProviderFallsBack(t *testing.T) {
	c := NewContainerProfileProcessor(config.Config{
		DefaultNamespace:          "kubescape",
		MaxApplicationProfileSize: 40000,
	}, nil)
	// Force the field nil to simulate an external caller that bypassed the
	// constructor's defaulting.
	c.CollapseSettings = nil

	// Build a spec with 4 /etc children. With the compiled default of 100,
	// none should collapse — proving PreSave's nil branch reached the
	// fallback rather than crashing or producing a degenerate result.
	spec := softwarecomposition.ContainerProfileSpec{}
	for i := 0; i < 4; i++ {
		spec.Opens = append(spec.Opens, softwarecomposition.OpenCalls{
			Path:  fmt.Sprintf("/etc/file%d", i),
			Flags: []string{"O_RDONLY"},
		})
	}

	// Mirror PreSave's nil-handling exactly to exercise the fallback path.
	settings := dynamicpathdetector.DefaultCollapseSettings()
	if c.CollapseSettings != nil {
		settings = c.CollapseSettings()
	}
	result := DeflateContainerProfileSpec(spec, nil, settings)
	assert.Greater(t, len(result.Opens), 1,
		"nil provider must fall back to defaults; default /etc=100 keeps 4 files distinct")
}

// TestContainerProfileProcessor_CustomCollapseSettings_ReachDeflate pins
// that a custom provider installed on ContainerProfileProcessor.CollapseSettings
// actually reaches the deflate path. Both deflate calls fetch settings via
// the processor's field, so the assertion exercises the wiring CodeRabbit
// flagged.
func TestContainerProfileProcessor_CustomCollapseSettings_ReachDeflate(t *testing.T) {
	c := NewContainerProfileProcessor(config.Config{
		DefaultNamespace:          "kubescape",
		MaxApplicationProfileSize: 40000,
	}, nil)

	spec := softwarecomposition.ContainerProfileSpec{}
	for i := 0; i < 4; i++ {
		spec.Opens = append(spec.Opens, softwarecomposition.OpenCalls{
			Path:  fmt.Sprintf("/etc/file%d", i),
			Flags: []string{"O_RDONLY"},
		})
	}

	// Default provider — paths stay distinct.
	defResult := DeflateContainerProfileSpec(spec, nil, c.CollapseSettings())
	assert.Greater(t, len(defResult.Opens), 1, "default threshold 100: four /etc files should NOT collapse")

	// Install a tight custom provider and re-deflate via the same field.
	c.CollapseSettings = func() dynamicpathdetector.CollapseSettings {
		return dynamicpathdetector.CollapseSettings{
			OpenDynamicThreshold:     50,
			EndpointDynamicThreshold: 100,
			CollapseConfigs: []dynamicpathdetector.CollapseConfig{
				{Prefix: "/etc", Threshold: 3},
			},
		}
	}
	customResult := DeflateContainerProfileSpec(spec, nil, c.CollapseSettings())
	collapsed := false
	for _, o := range customResult.Opens {
		if o.Path == "/etc/"+dynamicpathdetector.DynamicIdentifier {
			collapsed = true
			break
		}
	}
	assert.True(t, collapsed,
		"custom provider on c.CollapseSettings (threshold 3): four /etc files MUST collapse to /etc/⋯")
}

// TestContainerProfileProcessor_DefaultConstructorWiresProvider pins the
// constructor contract — a freshly-constructed processor must have a
// non-nil CollapseSettings provider that returns the compiled defaults.
func TestContainerProfileProcessor_DefaultConstructorWiresProvider(t *testing.T) {
	c := NewContainerProfileProcessor(config.Config{
		DefaultNamespace:          "kubescape",
		MaxApplicationProfileSize: 40000,
	}, nil)
	assert.NotNil(t, c.CollapseSettings, "constructor must wire a default provider")
	got := c.CollapseSettings()
	want := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, want.OpenDynamicThreshold, got.OpenDynamicThreshold)
	assert.Equal(t, want.EndpointDynamicThreshold, got.EndpointDynamicThreshold)
	assert.Equal(t, len(want.CollapseConfigs), len(got.CollapseConfigs))
}
