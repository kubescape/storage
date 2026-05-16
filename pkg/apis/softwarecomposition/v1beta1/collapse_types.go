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

package v1beta1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CollapseConfiguration is a cluster-scoped resource carrying per-prefix
// thresholds for the dynamic-path-detector's open/endpoint collapse step.
// The storage server's deflate path reads the singleton (name "default")
// and feeds its entries into NewPathAnalyzerWithConfigs at runtime.
type CollapseConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec CollapseConfigurationSpec `json:"spec" protobuf:"bytes,2,req,name=spec"`
}

// CollapseConfigurationSpec carries the cluster-wide collapse thresholds.
type CollapseConfigurationSpec struct {
	// OpenDynamicThreshold is the fallback threshold for AnalyzeOpens when
	// no per-prefix entry matches the walked path.
	OpenDynamicThreshold int32 `json:"openDynamicThreshold" protobuf:"varint,1,req,name=openDynamicThreshold"`
	// EndpointDynamicThreshold is the counterpart for AnalyzeEndpoints.
	EndpointDynamicThreshold int32 `json:"endpointDynamicThreshold" protobuf:"varint,2,req,name=endpointDynamicThreshold"`
	// CollapseConfigs is the per-prefix threshold override list, evaluated
	// longest-prefix-wins. Each entry is keyed by Prefix so server-side
	// apply patches one entry at a time instead of replacing the slice.
	// +listType=map
	// +listMapKey=prefix
	CollapseConfigs []CollapseConfigEntry `json:"collapseConfigs,omitempty" protobuf:"bytes,3,rep,name=collapseConfigs"`
}

// CollapseConfigEntry is one per-prefix threshold override.
type CollapseConfigEntry struct {
	// Prefix is the path prefix to match (e.g. "/etc", "/opt").
	Prefix string `json:"prefix" protobuf:"bytes,1,req,name=prefix"`
	// Threshold is the maximum number of unique children allowed at any
	// trie node under Prefix before that node collapses to a single
	// dynamic identifier.
	Threshold int32 `json:"threshold" protobuf:"varint,2,req,name=threshold"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// CollapseConfigurationList is a list of CollapseConfiguration objects.
type CollapseConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []CollapseConfiguration `json:"items" protobuf:"bytes,2,rep,name=items"`
}
