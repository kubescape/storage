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
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SBOMSummarySpec
	Status SBOMSPDXv2p3Status
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSummaryList is a list of SBOM summaries
type SBOMSummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SBOMSummary
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

// VulnerabilityManifestReportMeta holds metadata about the specific report
// tied to a vulnerability manifest
type VulnerabilityManifestReportMeta struct {
	CreatedAt metav1.Time
}

// VulnerabilityManifestToolMeta describes data about the tool used to generate
// the vulnerability manifest’s report
type VulnerabilityManifestToolMeta struct {
	Name            string
	Version         string
	DatabaseVersion string
}

// VulnerabilityManifestMeta holds metadata about a vulnerability manifest
type VulnerabilityManifestMeta struct {
	WithRelevancy bool
	Tool          VulnerabilityManifestToolMeta
	Report        VulnerabilityManifestReportMeta
}

type VulnerabilityManifestSpec struct {
	Metadata VulnerabilityManifestMeta
	Payload  GrypeDocument
}

type VulnerabilityManifestStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifest is a custom resource that describes a manifest of found vulnerabilities.
type VulnerabilityManifest struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   VulnerabilityManifestSpec
	Status VulnerabilityManifestStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestList is a list of Vulnerability manifests.
type VulnerabilityManifestList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []VulnerabilityManifest
}

// VulnerabilityCounters describes a counter of vulnerabilityes.
//
// Intended to store relevant and total vulnerabilities in the future.
type VulnerabilityCounters struct {
	All      int
	Relevant int
}

// SeveritySummary is a summary of all vulnerabilities included in vulnerability manifest
type SeveritySummary struct {
	Critical   VulnerabilityCounters
	High       VulnerabilityCounters
	Medium     VulnerabilityCounters
	Low        VulnerabilityCounters
	Negligible VulnerabilityCounters
	Unknown    VulnerabilityCounters
}

type VulnerabilitiesObjScope struct {
	Namespace string
	Name      string
	Kind      string
}

type VulnerabilitiesComponents struct {
	ImageVulnerabilitiesObj    VulnerabilitiesObjScope
	WorkloadVulnerabilitiesObj VulnerabilitiesObjScope
}

type VulnerabilityManifestSummarySpec struct {
	Severities      SeveritySummary
	Vulnerabilities VulnerabilitiesComponents
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestSummary is a summary of a VulnerabilityManifest.
type VulnerabilityManifestSummary struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   VulnerabilityManifestSummarySpec
	Status VulnerabilityManifestStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestSummaryList is a list of VulnerabilityManifest summaries.
type VulnerabilityManifestSummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []VulnerabilityManifestSummary
}

type VulnerabilitySummarySpec struct {
	Severities                 SeveritySummary
	WorkloadVulnerabilitiesObj []VulnerabilitiesObjScope
}

type VulnerabilitySummaryStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilitySummary is a aggregation of a VulnerabilityManifestSummary by scope(namespace/cluster).
type VulnerabilitySummary struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   VulnerabilitySummarySpec
	Status VulnerabilitySummaryStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilitySummaryList is a list of VulnerabilitySummaries.
type VulnerabilitySummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []VulnerabilitySummary
}
