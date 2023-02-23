/*
Copyright 2018 The Kubernetes Authors.

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

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3List is a list of Flunder objects.
type SBOMSPDXv2p3List struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SBOMSPDXv2p3 `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// SBOMSPDXv2p3Spec is the specification of a Flunder.
type SBOMSPDXv2p3Spec struct {
	SPDX Document `json:"spdx,omitempty"`
}

// SBOMSPDXv2p3Status is the status of a Flunder.
type SBOMSPDXv2p3Status struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3 is an example type with a spec and a status.
type SBOMSPDXv2p3 struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SBOMSPDXv2p3Spec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SBOMSPDXv2p3Status `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}
