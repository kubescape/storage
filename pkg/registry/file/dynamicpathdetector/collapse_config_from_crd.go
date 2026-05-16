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

package dynamicpathdetector

import (
	"math"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// clampInt32 clamps a runtime int into the int32 wire range used by the
// CollapseConfiguration CRD. Thresholds are physically small (single- or
// double-digit counts of trie children); clamping defends only against
// the autotune path being handed a pathological value.
func clampInt32(v int) int32 {
	if v < 0 {
		return 0
	}
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}

// CollapseSettings is the runtime form of the CollapseConfiguration CRD —
// a single value carrying the thresholds the deflate path needs to build
// its analyzer. Use DefaultCollapseSettings for the built-in baseline,
// CollapseSettingsFromCRD to project a CRD into runtime settings, and
// CRDFromCollapseSettings to round-trip back when tooling (e.g. bobctl
// autotune) needs to write the CRD.
type CollapseSettings struct {
	OpenDynamicThreshold     int
	EndpointDynamicThreshold int
	CollapseConfigs          []CollapseConfig
}

// DefaultCollapseSettings returns the built-in baseline. The returned
// value is a fresh copy on every call — callers may freely mutate the
// CollapseConfigs slice without affecting the package state. This
// mirrors the defensive-copy contract the bare DefaultCollapseConfigs()
// accessor already enforces.
func DefaultCollapseSettings() CollapseSettings {
	return CollapseSettings{
		OpenDynamicThreshold:     OpenDynamicThreshold,
		EndpointDynamicThreshold: EndpointDynamicThreshold,
		CollapseConfigs:          DefaultCollapseConfigs(),
	}
}

// CollapseSettingsFromCRD projects a CollapseConfiguration custom resource
// into the runtime form. Both threshold fields are taken verbatim; the
// per-prefix override slice is converted entry-by-entry. Returns a value
// that does not alias the CRD's internal slice.
func CollapseSettingsFromCRD(crd *softwarecomposition.CollapseConfiguration) CollapseSettings {
	if crd == nil {
		return DefaultCollapseSettings()
	}
	configs := make([]CollapseConfig, len(crd.Spec.CollapseConfigs))
	for i, entry := range crd.Spec.CollapseConfigs {
		configs[i] = CollapseConfig{
			Prefix:    entry.Prefix,
			Threshold: int(entry.Threshold),
		}
	}
	return CollapseSettings{
		OpenDynamicThreshold:     int(crd.Spec.OpenDynamicThreshold),
		EndpointDynamicThreshold: int(crd.Spec.EndpointDynamicThreshold),
		CollapseConfigs:          configs,
	}
}

// CRDFromCollapseSettings is the inverse of CollapseSettingsFromCRD. It
// produces a fresh CollapseConfiguration suitable for client-go Create /
// Update calls. Tooling (notably bobctl autotune) uses it to push tuned
// thresholds back into a running cluster.
func CRDFromCollapseSettings(name string, settings CollapseSettings) *softwarecomposition.CollapseConfiguration {
	entries := make([]softwarecomposition.CollapseConfigEntry, len(settings.CollapseConfigs))
	for i, cfg := range settings.CollapseConfigs {
		entries[i] = softwarecomposition.CollapseConfigEntry{
			Prefix:    cfg.Prefix,
			Threshold: clampInt32(cfg.Threshold),
		}
	}
	return &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     clampInt32(settings.OpenDynamicThreshold),
			EndpointDynamicThreshold: clampInt32(settings.EndpointDynamicThreshold),
			CollapseConfigs:          entries,
		},
	}
}

// CollapseSettingsProvider is the lookup hook the deflate path uses to
// fetch effective collapse thresholds at processing time. Production
// wiring can swap the default for a provider that reads the
// CollapseConfiguration CR from the apiserver's storage; tests and the
// default constructor return DefaultCollapseSettings.
type CollapseSettingsProvider func() CollapseSettings
