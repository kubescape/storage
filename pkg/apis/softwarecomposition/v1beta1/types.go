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

// ToolMeta describes metadata about a tool that generated an artifact
type ToolMeta struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ReportMeta describes metadata about a report
type ReportMeta struct {
	CreatedAt metav1.Time `json:"createdAt"`
}

// SPDXMeta describes metadata about an SPDX-formatted SBOM
type SPDXMeta struct {
	Tool   ToolMeta   `json:"tool"`
	Report ReportMeta `json:"report"`
}

// SBOMSPDXv2p3Spec is the specification of a Flunder.
type SBOMSPDXv2p3Spec struct {
	Metadata SPDXMeta `json:"metadata"`
	SPDX     Document `json:"spdx,omitempty"`
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

// SBOMSummarySpec is the spec for the SBOM summary
//
// Since the summary spec is supposed to hold no data, only used as a low
// footprint way to watch for heavy full-sized SBOMs, the spec is supposed to be
// empty on purpose.
type SBOMSummarySpec struct{}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSummary is a summary of an SBOM. It is not meant to be changed and only
// works as a lightweight facade for watching proper SBOMs.
type SBOMSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SBOMSummarySpec    `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SBOMSPDXv2p3Status `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSummaryList is a list of SBOM summaries
type SBOMSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SBOMSummary `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3Filtered is a custom resource that describes a filtered SBOM in the SPDX 2.3 format.
//
// Being filtered means that the SBOM contains only the relevant vulnerable materials.
type SBOMSPDXv2p3Filtered struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SBOMSPDXv2p3Spec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SBOMSPDXv2p3Status `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSPDXv2p3FilteredList is a list of SBOMSPDXv2p3Filtered objects.
type SBOMSPDXv2p3FilteredList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SBOMSPDXv2p3Filtered `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// VulnerabilityManifestReportMeta holds metadata about the specific report
// tied to a vulnerability manifest
type VulnerabilityManifestReportMeta struct {
	CreatedAt metav1.Time `json:"createdAt"`
}

// VulnerabilityManifestToolMeta describes data about the tool used to generate
// the vulnerability manifest’s report
type VulnerabilityManifestToolMeta struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	DatabaseVersion string `json:"databaseVersion"`
}

// VulnerabilityManifestMeta holds metadata about a vulnerability manifest
type VulnerabilityManifestMeta struct {
	WithRelevancy bool                            `json:"withRelevancy"`
	Tool          VulnerabilityManifestToolMeta   `json:"tool"`
	Report        VulnerabilityManifestReportMeta `json:"report"`
}

type VulnerabilityManifestSpec struct {
	Metadata VulnerabilityManifestMeta `json:"metadata,omitempty"`
	Payload  GrypeDocument             `json:"payload,omitempty"`
}

type VulnerabilityManifestStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifest is a custom resource that describes a manifest of found vulnerabilities.
type VulnerabilityManifest struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   VulnerabilityManifestSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status VulnerabilityManifestStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestList is a list of Vulnerability manifests.
type VulnerabilityManifestList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []VulnerabilityManifest `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// VulnerabilityCounters describes a counter of vulnerabilityes.
//
// Intended to store relevant and total vulnerabilities in the future.
type VulnerabilityCounters struct {
	All      int `json:"all"`
	Relevant int `json:"relevant"`
}

// SeveritySummary is a summary of all vulnerabilities included in vulnerability manifest
type SeveritySummary struct {
	Critical   VulnerabilityCounters `json:"critical,omitempty"`
	High       VulnerabilityCounters `json:"high,omitempty"`
	Medium     VulnerabilityCounters `json:"medium,omitempty"`
	Low        VulnerabilityCounters `json:"low,omitempty"`
	Negligible VulnerabilityCounters `json:"negligible,omitempty"`
	Unknown    VulnerabilityCounters `json:"unknown,omitempty"`
}

type VulnerabilitiesObjScope struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
}

type VulnerabilitiesComponents struct {
	ImageVulnerabilitiesObj    VulnerabilitiesObjScope `json:"basic"`
	WorkloadVulnerabilitiesObj VulnerabilitiesObjScope `json:"filtered"`
}

type VulnerabilityManifestSummarySpec struct {
	Severities      SeveritySummary           `json:"severities"`
	Vulnerabilities VulnerabilitiesComponents `json:"vulnerabilities"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestSummary is a summary of a VulnerabilityManifest.
type VulnerabilityManifestSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   VulnerabilityManifestSummarySpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status VulnerabilityManifestStatus      `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestSummaryList is a list of VulnerabilityManifest summaries.
type VulnerabilityManifestSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []VulnerabilityManifestSummary `json:"items" protobuf:"bytes,2,rep,name=items"`
}
