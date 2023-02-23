/*
Copyright 2017 The Kubernetes Authors.

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

package wardle

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3List is a list of Flunder objects.
type SBOMSPDXv2p3List struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SBOMSPDXv2p3
}

// SBOMSPDXv2p3Spec is the specification of an SPDX SBOM.
type SBOMSPDXv2p3Spec struct {
	SPDX Document
}

// SBOMSPDXv2p3Status is the status of an SPDX SBOM.
type SBOMSPDXv2p3Status struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3 is a custom resource that describes an SBOM in the SPDX 2.3 format.
type SBOMSPDXv2p3 struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SBOMSPDXv2p3Spec
	Status SBOMSPDXv2p3Status
}
