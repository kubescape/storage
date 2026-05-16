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

package softwarecomposition

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CollapseConfiguration is a cluster-scoped resource carrying per-prefix
// thresholds for the dynamic-path-detector's open/endpoint collapse step.
//
// At runtime the storage server's deflate path reads the singleton
// CollapseConfiguration (name "default") and feeds its entries into
// NewPathAnalyzerWithConfigs(...). When the resource is absent the deflate
// path falls back to the package-level defaultCollapseConfigs slice.
//
// Tooling (e.g. bobctl autotune) can write the singleton to push tuned
// thresholds back into a running cluster without restarting the storage
// server.
type CollapseConfiguration struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec CollapseConfigurationSpec
}

// CollapseConfigurationSpec carries the cluster-wide collapse thresholds.
type CollapseConfigurationSpec struct {
	// OpenDynamicThreshold is the fallback threshold for AnalyzeOpens when
	// no per-prefix entry matches the walked path.
	OpenDynamicThreshold int32
	// EndpointDynamicThreshold is the counterpart for AnalyzeEndpoints.
	EndpointDynamicThreshold int32
	// CollapseConfigs is the per-prefix threshold override list, evaluated
	// longest-prefix-wins.
	CollapseConfigs []CollapseConfigEntry
}

// CollapseConfigEntry is one per-prefix threshold override.
type CollapseConfigEntry struct {
	// Prefix is the path prefix to match (e.g. "/etc", "/opt").
	Prefix string
	// Threshold is the maximum number of unique children allowed at any
	// trie node under Prefix before that node collapses to a single
	// dynamic identifier.
	Threshold int32
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CollapseConfigurationList is a list of CollapseConfiguration objects.
type CollapseConfigurationList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []CollapseConfiguration
}
