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

package softwarecomposition

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

// ToolMeta describes metadata about a tool that generated an artifact
type ToolMeta struct {
	Name    string
	Version string
}

// ReportMeta describes metadata about a report
type ReportMeta struct {
	CreatedAt metav1.Time
}

// SPDXMeta describes metadata about an SPDX-formatted SBOM
type SPDXMeta struct {
	Tool   ToolMeta
	Report ReportMeta
}

// SBOMSPDXv2p3Spec is the specification of an SPDX SBOM.
type SBOMSPDXv2p3Spec struct {
	Metadata SPDXMeta
	SPDX     Document
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

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3Filtered is a custom resource that describes a filtered SBOM in the SPDX 2.3 format.
//
// Being filtered means that the SBOM contains only the relevant vulnerable materials.
type SBOMSPDXv2p3Filtered struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SBOMSPDXv2p3Spec
	Status SBOMSPDXv2p3Status
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3FilteredList is a list of SBOMSPDXv2p3Filtered objects.
type SBOMSPDXv2p3FilteredList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SBOMSPDXv2p3Filtered
}
