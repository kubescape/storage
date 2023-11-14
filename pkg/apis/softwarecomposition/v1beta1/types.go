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

// VulnerabilityCounters describes a counter of vulnerabilities.
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
	ImageVulnerabilitiesObj    VulnerabilitiesObjScope `json:"all"`
	WorkloadVulnerabilitiesObj VulnerabilitiesObjScope `json:"relevant"`
}

type VulnerabilityManifestSummarySpec struct {
	Severities      SeveritySummary           `json:"severities"`
	Vulnerabilities VulnerabilitiesComponents `json:"vulnerabilitiesRef"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilityManifestSummary is a summary of a VulnerabilityManifests.
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

type VulnerabilitySummarySpec struct {
	Severities                 SeveritySummary           `json:"severities"`
	WorkloadVulnerabilitiesObj []VulnerabilitiesObjScope `json:"vulnerabilitiesRef"`
}

type VulnerabilitySummaryStatus struct {
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilitySummary is a summary of a vulnerabilities for a given scope.
type VulnerabilitySummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   VulnerabilitySummarySpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status VulnerabilitySummaryStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// VulnerabilitySummaryList is a list of VulnerabilitySummary.
type VulnerabilitySummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []VulnerabilitySummary `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   ApplicationProfileSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status ApplicationProfileStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type ApplicationProfileSpec struct {
	Containers     []ApplicationProfileContainer `json:"containers,omitempty"`
	InitContainers []ApplicationProfileContainer `json:"initContainers,omitempty"`
}

type ApplicationProfileContainer struct {
	Name         string      `json:"name,omitempty"`
	Capabilities []string    `json:"capabilities,omitempty"`
	Execs        []ExecCalls `json:"execs,omitempty"`
	Opens        []OpenCalls `json:"opens,omitempty"`
	Syscalls     []string    `json:"syscalls,omitempty"`
}

type ExecCalls struct {
	Path string   `json:"path,omitempty"`
	Args []string `json:"args,omitempty"`
	Envs []string `json:"envs,omitempty"`
}

type OpenCalls struct {
	Path  string   `json:"path" yaml:"path"`
	Flags []string `json:"flags" yaml:"flags"`
}

type ApplicationProfileStatus struct {
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ApplicationProfile `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfileSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationProfileSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ApplicationProfileSummary `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationActivity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   ApplicationActivitySpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status ApplicationActivityStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type ApplicationActivitySpec struct {
	Syscalls []string `json:"syscalls,omitempty"`
}

type ApplicationActivityStatus struct {
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ApplicationActivityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ApplicationActivity `json:"items" protobuf:"bytes,2,rep,name=items"`
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
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec VEX `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type OpenVulnerabilityExchangeContainerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []OpenVulnerabilityExchangeContainer `json:"items" protobuf:"bytes,2,rep,name=items"`
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

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftFiltered is a custom resource that describes a filtered SBOM in the Syft format.
//
// Being filtered means that the SBOM contains only the relevant vulnerable materials.
type SBOMSyftFiltered struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SyftDocument   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SBOMSyftStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftFilteredList is a list of SBOMSyftFiltered objects.
type SBOMSyftFilteredList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SBOMSyftFiltered `json:"items" protobuf:"bytes,2,rep,name=items"`
}
