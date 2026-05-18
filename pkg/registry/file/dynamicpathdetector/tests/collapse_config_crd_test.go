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
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDefaultCollapseSettings_FreshCopyPerCall pins the contract that
// DefaultCollapseSettings returns a value whose CollapseConfigs slice is
// freshly allocated on every call. Without this, a caller mutating the
// returned slice could leak into a subsequent call's result and
// silently change collapse thresholds across the whole storage server.
func TestDefaultCollapseSettings_FreshCopyPerCall(t *testing.T) {
	first := dynamicpathdetector.DefaultCollapseSettings()
	require.NotEmpty(t, first.CollapseConfigs, "default settings must have per-prefix entries")

	originalThreshold := first.CollapseConfigs[0].Threshold
	first.CollapseConfigs[0].Threshold = 999_999
	first.CollapseConfigs[0].Prefix = "/poisoned"
	first.CollapseConfigs = append(first.CollapseConfigs, dynamicpathdetector.CollapseConfig{
		Prefix: "/poisoned-tail", Threshold: 1,
	})

	second := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, originalThreshold, second.CollapseConfigs[0].Threshold,
		"mutating the first call's slice must not change the package state")
	for _, cfg := range second.CollapseConfigs {
		assert.NotEqual(t, "/poisoned", cfg.Prefix, "prefix mutation must not leak")
		assert.NotEqual(t, "/poisoned-tail", cfg.Prefix, "appended entries must not leak")
	}
	if len(first.CollapseConfigs) > 0 && len(second.CollapseConfigs) > 0 {
		assert.NotSame(t, &first.CollapseConfigs[0], &second.CollapseConfigs[0],
			"DefaultCollapseSettings must return a fresh CollapseConfigs backing array")
	}
}

// TestCollapseSettingsFromCRD_NilFallsBackToDefaults documents the
// defensive nil-handling: when the storage server can't read the CRD
// (NotFound, or pre-cluster bootstrap), the deflate path must still
// produce sensible thresholds. Returning an empty struct here would
// mean "collapse never fires" which is a worse failure mode than
// "fall back to compiled defaults".
func TestCollapseSettingsFromCRD_NilFallsBackToDefaults(t *testing.T) {
	got := dynamicpathdetector.CollapseSettingsFromCRD(nil)
	want := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, want.OpenDynamicThreshold, got.OpenDynamicThreshold,
		"nil CRD must produce the default OpenDynamicThreshold")
	assert.Equal(t, want.EndpointDynamicThreshold, got.EndpointDynamicThreshold,
		"nil CRD must produce the default EndpointDynamicThreshold")
	assert.Equal(t, len(want.CollapseConfigs), len(got.CollapseConfigs),
		"nil CRD must produce the default CollapseConfigs entries")
}

// TestCollapseSettingsFromCRD_RoundTrip pins the conversion contract:
// CRD spec values land verbatim in the runtime settings, the entries
// are converted entry-by-entry preserving order, and the resulting
// slice does NOT alias the CRD's internal slice.
func TestCollapseSettingsFromCRD_RoundTrip(t *testing.T) {
	crd := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     42,
			EndpointDynamicThreshold: 84,
			CollapseConfigs: []softwarecomposition.CollapseConfigEntry{
				{Prefix: "/etc", Threshold: 100},
				{Prefix: "/var/log", Threshold: 50},
				{Prefix: "/opt", Threshold: 25},
			},
		},
	}

	settings := dynamicpathdetector.CollapseSettingsFromCRD(crd)
	assert.Equal(t, 42, settings.OpenDynamicThreshold)
	assert.Equal(t, 84, settings.EndpointDynamicThreshold)
	require.Len(t, settings.CollapseConfigs, 3)
	assert.Equal(t, "/etc", settings.CollapseConfigs[0].Prefix)
	assert.Equal(t, 100, settings.CollapseConfigs[0].Threshold)
	assert.Equal(t, "/var/log", settings.CollapseConfigs[1].Prefix)
	assert.Equal(t, "/opt", settings.CollapseConfigs[2].Prefix)

	// Mutate the converted settings — the CRD must not see the change.
	settings.CollapseConfigs[0].Threshold = 999
	settings.CollapseConfigs[0].Prefix = "/poisoned"
	assert.Equal(t, "/etc", crd.Spec.CollapseConfigs[0].Prefix,
		"settings → CRD aliasing must not leak: mutating settings must not change CRD")
	assert.EqualValues(t, 100, crd.Spec.CollapseConfigs[0].Threshold,
		"settings → CRD aliasing must not leak: threshold")
}

// TestCRDFromCollapseSettings_RoundTrip is the inverse of the above —
// pins that CRD construction also makes a fresh slice and is
// faithful to the source settings.
func TestCRDFromCollapseSettings_RoundTrip(t *testing.T) {
	settings := dynamicpathdetector.CollapseSettings{
		OpenDynamicThreshold:     11,
		EndpointDynamicThreshold: 22,
		CollapseConfigs: []dynamicpathdetector.CollapseConfig{
			{Prefix: "/etc", Threshold: 7},
			{Prefix: "/srv", Threshold: 3},
		},
	}

	crd := dynamicpathdetector.CRDFromCollapseSettings("default", settings)
	require.NotNil(t, crd)
	assert.Equal(t, "default", crd.Name)
	assert.EqualValues(t, 11, crd.Spec.OpenDynamicThreshold)
	assert.EqualValues(t, 22, crd.Spec.EndpointDynamicThreshold)
	require.Len(t, crd.Spec.CollapseConfigs, 2)
	assert.Equal(t, "/etc", crd.Spec.CollapseConfigs[0].Prefix)
	assert.EqualValues(t, 7, crd.Spec.CollapseConfigs[0].Threshold)
	assert.Equal(t, "/srv", crd.Spec.CollapseConfigs[1].Prefix)

	// Mutate the produced CRD — the source settings must not see the change.
	crd.Spec.CollapseConfigs[0].Prefix = "/poisoned"
	crd.Spec.CollapseConfigs[0].Threshold = 999
	assert.Equal(t, "/etc", settings.CollapseConfigs[0].Prefix,
		"CRD → settings aliasing must not leak: mutating CRD must not change settings")
	assert.Equal(t, 7, settings.CollapseConfigs[0].Threshold,
		"CRD → settings aliasing must not leak: threshold")
}

// TestCollapseSettings_FullRoundTrip pins that going CRD → settings →
// CRD is faithful (idempotent on canonical content).
func TestCollapseSettings_FullRoundTrip(t *testing.T) {
	original := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     50,
			EndpointDynamicThreshold: 100,
			CollapseConfigs: []softwarecomposition.CollapseConfigEntry{
				{Prefix: "/etc", Threshold: 100},
				{Prefix: "/var/run", Threshold: 50},
			},
		},
	}

	settings := dynamicpathdetector.CollapseSettingsFromCRD(original)
	roundTripped := dynamicpathdetector.CRDFromCollapseSettings("default", settings)
	assert.Equal(t, original.Spec, roundTripped.Spec,
		"CRD → settings → CRD must preserve spec content")
}
