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

// VulnerabilitySummary is a summary of a vulnerabilities for a given scope.
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

func (c *VulnerabilityCounters) Add(counters *VulnerabilityCounters) {
	c.All += counters.All
	c.Relevant += counters.Relevant
}

func (s *SeveritySummary) Add(severities *SeveritySummary) {
	s.Critical.Add(&severities.Critical)
	s.High.Add(&severities.High)
	s.Medium.Add(&severities.Medium)
	s.Low.Add(&severities.Low)
	s.Negligible.Add(&severities.Negligible)
	s.Unknown.Add(&severities.Unknown)
}

func (v *VulnerabilitySummary) Merge(vulnManifestSumm *VulnerabilityManifestSummary) {
	v.Spec.Severities.Add(&vulnManifestSumm.Spec.Severities)
	workloadVulnerabilitiesObj := VulnerabilitiesObjScope{
		Name:      vulnManifestSumm.Name,
		Namespace: vulnManifestSumm.Namespace,
		Kind:      "vulnerabilitymanifestsummary",
	}
	v.Spec.WorkloadVulnerabilitiesObj = append(v.Spec.WorkloadVulnerabilitiesObj, workloadVulnerabilitiesObj)
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfile struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   ApplicationProfileSpec
	Status ApplicationProfileStatus
}

type ApplicationProfileSpec struct {
	Containers     []ApplicationProfileContainer
	InitContainers []ApplicationProfileContainer
}

type ApplicationProfileContainer struct {
	Name         string
	Capabilities []string
	Execs        []ExecCalls
	Opens        []OpenCalls
	Syscalls     []string
}

type ExecCalls struct {
	Path string
	Args []string
	Envs []string
}

type OpenCalls struct {
	Path  string
	Flags []string
}

type ApplicationProfileStatus struct {
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfileList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ApplicationProfile
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfileSummary struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfileSummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ApplicationProfileSummary
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationActivity struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   ApplicationActivitySpec
	Status ApplicationActivityStatus
}

type ApplicationActivitySpec struct {
	Syscalls []string
}

type ApplicationActivityStatus struct {
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationActivityList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ApplicationActivity
}

///////////////////////////////////////////////////////////////////////////////
// VEX
///////////////////////////////////////////////////////////////////////////////

type (
	Algorithm         string
	Hash              string
	IdentifierLocator string
	IdentifierType    string
	Status            string
)

type Justification string

type Component struct {
	// ID is an IRI identifying the component. It is optional as the component
	// can also be identified using hashes or software identifiers.
	ID string `json:"@id,omitempty"`

	// Hashes is a map of hashes to identify the component using cryptographic
	// hashes.
	Hashes map[Algorithm]Hash `json:"hashes,omitempty"`

	// Identifiers is a list of software identifiers that describe the component.
	Identifiers map[IdentifierType]string `json:"identifiers,omitempty"`

	// Supplier is an optional machine-readable identifier for the supplier of
	// the component. Valid examples include email address or IRIs.
	Supplier string `json:"supplier,omitempty"`
}

type Product struct {
	Component
	Subcomponents []Subcomponent `json:"subcomponents,omitempty"`
}

type Subcomponent struct {
	Component
}

type VexVulnerability struct {
	//  ID is an IRI to reference the vulnerability in the statement.
	ID string `json:"@id,omitempty"`

	// Name is the main vulnerability identifier.
	Name string `json:"name,omitempty"`

	// Description is a short free form text description of the vulnerability.
	Description string `json:"description,omitempty"`

	// Aliases is a list of other vulnerability identifier strings that
	// locate the vulnerability in other tracking systems.
	Aliases []string `json:"aliases,omitempty"`
}

type Statement struct {
	// ID is an optional identifier for the statement. It takes an IRI and must
	// be unique for each statement in the document.
	ID string `json:"@id,omitempty"`

	// [vul_id] SHOULD use existing and well known identifiers, for example:
	// CVE, the Global Security Database (GSD), or a supplier’s vulnerability
	// tracking system. It is expected that vulnerability identification systems
	// are external to and maintained separately from VEX.
	//
	// [vul_id] MAY be URIs or URLs.
	// [vul_id] MAY be arbitrary and MAY be created by the VEX statement [author].
	Vulnerability VexVulnerability `json:"vulnerability,omitempty"`

	// Timestamp is the time at which the information expressed in the Statement
	// was known to be true.
	Timestamp string `json:"timestamp,omitempty"`

	// LastUpdated records the time when the statement last had a modification
	LastUpdated string `json:"last_updated,omitempty"`

	// Product
	// Product details MUST specify what Status applies to.
	// Product details MUST include [product_id] and MAY include [subcomponent_id].
	Products []Product `json:"products,omitempty"`

	// A VEX statement MUST provide Status of the vulnerabilities with respect to the
	// products and components listed in the statement. Status MUST be one of the
	// Status const values, some of which have further options and requirements.
	Status Status `json:"status"`

	// [status_notes] MAY convey information about how [status] was determined
	// and MAY reference other VEX information.
	StatusNotes string `json:"status_notes,omitempty"`

	// For ”not_affected” status, a VEX statement MUST include a status Justification
	// that further explains the status.
	Justification Justification `json:"justification,omitempty"`

	// For ”not_affected” status, a VEX statement MAY include an ImpactStatement
	// that contains a description why the vulnerability cannot be exploited.
	ImpactStatement string `json:"impact_statement,omitempty"`

	// For "affected" status, a VEX statement MUST include an ActionStatement that
	// SHOULD describe actions to remediate or mitigate [vul_id].
	ActionStatement          string `json:"action_statement,omitempty"`
	ActionStatementTimestamp string `json:"action_statement_timestamp,omitempty"`
}

type VEX struct {
	Metadata
	Statements []Statement `json:"statements"`
}

type Metadata struct {
	// Context is the URL pointing to the jsonld context definition
	Context string `json:"@context"`

	// ID is the identifying string for the VEX document. This should be unique per
	// document.
	ID string `json:"@id"`

	// Author is the identifier for the author of the VEX statement, ideally a common
	// name, may be a URI. [author] is an individual or organization. [author]
	// identity SHOULD be cryptographically associated with the signature of the VEX
	// statement or document or transport.
	Author string `json:"author"`

	// AuthorRole describes the role of the document Author.
	AuthorRole string `json:"role,omitempty"`

	// Timestamp defines the time at which the document was issued.
	Timestamp string `json:"timestamp"`

	// LastUpdated marks the time when the document had its last update. When the
	// document changes both version and this field should be updated.
	LastUpdated string `json:"last_updated,omitempty"`

	// Version is the document version. It must be incremented when any content
	// within the VEX document changes, including any VEX statements included within
	// the VEX document.
	Version int `json:"version"`

	// Tooling expresses how the VEX document and contained VEX statements were
	// generated. It's optional. It may specify tools or automated processes used in
	// the document or statement generation.
	Tooling string `json:"tooling,omitempty"`

	// Supplier is an optional field.
	Supplier string `json:"supplier,omitempty"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type OpenVulnerabilityExchangeContainer struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec VEX
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type OpenVulnerabilityExchangeContainerList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []OpenVulnerabilityExchangeContainer
}

// SBOMSyftStatus is the status of a Syft SBOM.
type SBOMSyftStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyft is a custom resource that describes an SBOM in the Syft format.
type SBOMSyft struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SyftDocument
	Status SBOMSyftStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftList is a list of SBOMSyft objects.
type SBOMSyftList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SBOMSyft
}
