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
	"encoding/json"

	"github.com/containers/common/pkg/seccomp"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/consts"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ToolMeta describes metadata about a tool that generated an artifact
type ToolMeta struct {
	Name    string `json:"name" protobuf:"bytes,1,req,name=name"`
	Version string `json:"version" protobuf:"bytes,2,req,name=version"`
}

// ReportMeta describes metadata about a report
type ReportMeta struct {
	CreatedAt metav1.Time `json:"createdAt" protobuf:"bytes,1,req,name=createdAt"`
}

// SPDXMeta describes metadata about an SPDX-formatted SBOM
type SPDXMeta struct {
	Tool   ToolMeta   `json:"tool" protobuf:"bytes,1,req,name=tool"`
	Report ReportMeta `json:"report" protobuf:"bytes,2,req,name=report"`
}

// VulnerabilityManifestReportMeta holds metadata about the specific report
// tied to a vulnerability manifest
type VulnerabilityManifestReportMeta struct {
	CreatedAt metav1.Time `json:"createdAt" protobuf:"bytes,1,req,name=createdAt"`
}

// VulnerabilityManifestToolMeta describes data about the tool used to generate
// the vulnerability manifest’s report
type VulnerabilityManifestToolMeta struct {
	Name            string `json:"name" protobuf:"bytes,1,req,name=name"`
	Version         string `json:"version" protobuf:"bytes,2,req,name=version"`
	DatabaseVersion string `json:"databaseVersion" protobuf:"bytes,3,req,name=databaseVersion"`
}

// VulnerabilityManifestMeta holds metadata about a vulnerability manifest
type VulnerabilityManifestMeta struct {
	WithRelevancy bool                            `json:"withRelevancy" protobuf:"bytes,1,req,name=withRelevancy"`
	Tool          VulnerabilityManifestToolMeta   `json:"tool" protobuf:"bytes,2,req,name=tool"`
	Report        VulnerabilityManifestReportMeta `json:"report" protobuf:"bytes,3,req,name=report"`
}

type VulnerabilityManifestSpec struct {
	Metadata VulnerabilityManifestMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Payload  GrypeDocument             `json:"payload,omitempty" protobuf:"bytes,2,opt,name=payload"`
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
	All      int64 `json:"all" protobuf:"bytes,1,req,name=all"`
	Relevant int64 `json:"relevant,omitempty" protobuf:"bytes,2,opt,name=relevant"`
}

// SeveritySummary is a summary of all vulnerabilities included in vulnerability manifest
type SeveritySummary struct {
	Critical   VulnerabilityCounters `json:"critical,omitempty" protobuf:"bytes,1,opt,name=critical"`
	High       VulnerabilityCounters `json:"high,omitempty" protobuf:"bytes,2,opt,name=high"`
	Medium     VulnerabilityCounters `json:"medium,omitempty" protobuf:"bytes,3,opt,name=medium"`
	Low        VulnerabilityCounters `json:"low,omitempty" protobuf:"bytes,4,opt,name=low"`
	Negligible VulnerabilityCounters `json:"negligible,omitempty" protobuf:"bytes,5,opt,name=negligible"`
	Unknown    VulnerabilityCounters `json:"unknown,omitempty" protobuf:"bytes,6,opt,name=unknown"`
}

type VulnerabilitiesObjScope struct {
	Namespace string `json:"namespace" protobuf:"bytes,1,req,name=namespace"`
	Name      string `json:"name" protobuf:"bytes,2,req,name=name"`
	Kind      string `json:"kind" protobuf:"bytes,3,req,name=kind"`
}

type VulnerabilitiesComponents struct {
	ImageVulnerabilitiesObj    VulnerabilitiesObjScope `json:"all" protobuf:"bytes,1,req,name=all"`
	WorkloadVulnerabilitiesObj VulnerabilitiesObjScope `json:"relevant,omitempty" protobuf:"bytes,2,opt,name=relevant"`
}

type VulnerabilityManifestSummarySpec struct {
	Severities      SeveritySummary           `json:"severities" protobuf:"bytes,1,req,name=severities"`
	Vulnerabilities VulnerabilitiesComponents `json:"vulnerabilitiesRef" protobuf:"bytes,2,req,name=vulnerabilitiesRef"`
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

type VulnerabilitySummarySpec struct {
	Severities                 SeveritySummary           `json:"severities" protobuf:"bytes,1,req,name=severities"`
	WorkloadVulnerabilitiesObj []VulnerabilitiesObjScope `json:"vulnerabilitiesRef" protobuf:"bytes,2,rep,name=vulnerabilitiesRef"`
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

// VulnerabilitySummaryList is a list of VulnerabilitySummaries.
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
	Architectures []string `json:"architectures" protobuf:"bytes,1,rep,name=architectures"`
	// +patchMergeKey=name
	// +patchStrategy=merge
	Containers []ApplicationProfileContainer `json:"containers,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,2,rep,name=containers"`
	// +patchMergeKey=name
	// +patchStrategy=merge
	InitContainers []ApplicationProfileContainer `json:"initContainers,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,3,rep,name=initContainers"`
	// +patchMergeKey=name
	// +patchStrategy=merge
	EphemeralContainers []ApplicationProfileContainer `json:"ephemeralContainers,omitempty" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,4,rep,name=ephemeralContainers"`
}

type ApplicationProfileContainer struct {
	Name         string   `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	Capabilities []string `json:"capabilities" protobuf:"bytes,2,rep,name=capabilities"`
	// +patchMergeKey=path
	// +patchStrategy=merge
	Execs []ExecCalls `json:"execs" patchStrategy:"merge" patchMergeKey:"path" protobuf:"bytes,3,rep,name=execs"`
	// +patchMergeKey=path
	// +patchStrategy=merge
	Opens          []OpenCalls          `json:"opens" patchStrategy:"merge" patchMergeKey:"path" protobuf:"bytes,4,rep,name=opens"`
	Syscalls       []string             `json:"syscalls" protobuf:"bytes,5,rep,name=syscalls"`
	SeccompProfile SingleSeccompProfile `json:"seccompProfile,omitempty" protobuf:"bytes,6,opt,name=seccompProfile"`
	// +patchStrategy=merge
	// +patchMergeKey=endpoint
	Endpoints []HTTPEndpoint `json:"endpoints" patchStrategy:"merge" patchMergeKey:"endpoint" protobuf:"bytes,7,rep,name=endpoints"`
	ImageID   string         `json:"imageID" protobuf:"bytes,8,opt,name=imageID"`
	ImageTag  string         `json:"imageTag" protobuf:"bytes,9,opt,name=imageTag"`
	// +patchStrategy=merge
	// +patchMergeKey=ruleId
	PolicyByRuleId       map[string]RulePolicy `json:"rulePolicies" protobuf:"bytes,10,rep,name=rulePolicies" patchStrategy:"merge" patchMergeKey:"ruleId"`
	IdentifiedCallStacks []IdentifiedCallStack `json:"identifiedCallStacks" protobuf:"bytes,11,rep,name=identifiedCallStacks"`
}

type ExecCalls struct {
	Path string   `json:"path,omitempty" protobuf:"bytes,1,opt,name=path"`
	Args []string `json:"args,omitempty" protobuf:"bytes,2,opt,name=args"`
	Envs []string `json:"envs,omitempty" protobuf:"bytes,3,opt,name=envs"`
}

type OpenCalls struct {
	Path  string   `json:"path" yaml:"path" protobuf:"bytes,1,req,name=path"`
	Flags []string `json:"flags" yaml:"flags" protobuf:"bytes,2,rep,name=flags"`
}

type CallID string

type IdentifiedCallStack struct {
	CallID    CallID    `json:"callID" protobuf:"bytes,1,opt,name=callID"`
	CallStack CallStack `json:"callStack" protobuf:"bytes,2,opt,name=callStack"`
}

type StackFrame struct {
	FileID string `json:"fileID" protobuf:"bytes,1,opt,name=fileID"`
	Lineno string `json:"lineno" protobuf:"bytes,2,opt,name=lineno"`
}

type CallStackNode struct {
	Children []CallStackNode `json:"children" protobuf:"bytes,1,rep,name=children"`
	Parent   *CallStackNode  `json:"parent" protobuf:"bytes,2,opt,name=parent"`
	Frame    *StackFrame     `json:"frame" protobuf:"bytes,3,opt,name=frame"`
}

type CallStack struct {
	Root *CallStackNode `json:"root" protobuf:"bytes,1,opt,name=root"`
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

type ApplicationActivity struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   ApplicationActivitySpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status ApplicationActivityStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type ApplicationActivitySpec struct {
	Syscalls []string `json:"syscalls,omitempty" protobuf:"bytes,1,rep,name=syscalls"`
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
	ID string `json:"@id,omitempty" protobuf:"bytes,1,opt,name=id"`

	// Hashes is a map of hashes to identify the component using cryptographic
	// hashes.
	Hashes map[Algorithm]Hash `json:"hashes,omitempty" protobuf:"bytes,2,opt,name=hashes"`

	// Identifiers is a list of software identifiers that describe the component.
	Identifiers map[IdentifierType]string `json:"identifiers,omitempty" protobuf:"bytes,3,opt,name=identifiers"`

	// Supplier is an optional machine-readable identifier for the supplier of
	// the component. Valid examples include email address or IRIs.
	Supplier string `json:"supplier,omitempty" protobuf:"bytes,4,opt,name=supplier"`
}

type Product struct {
	Component     `protobuf:"bytes,1,opt,name=component"`
	Subcomponents []Subcomponent `json:"subcomponents,omitempty" protobuf:"bytes,2,opt,name=subcomponents"`
}

type Subcomponent struct {
	Component `protobuf:"bytes,1,opt,name=component"`
}

type VexVulnerability struct {
	//  ID is an IRI to reference the vulnerability in the statement.
	ID string `json:"@id,omitempty" protobuf:"bytes,1,opt,name=id"`

	// Name is the main vulnerability identifier.
	Name string `json:"name,omitempty" protobuf:"bytes,2,opt,name=name"`

	// Description is a short free form text description of the vulnerability.
	Description string `json:"description,omitempty" protobuf:"bytes,3,opt,name=description"`

	// Aliases is a list of other vulnerability identifier strings that
	// locate the vulnerability in other tracking systems.
	Aliases []string `json:"aliases,omitempty" protobuf:"bytes,4,opt,name=aliases"`
}

type Statement struct {
	// ID is an optional identifier for the statement. It takes an IRI and must
	// be unique for each statement in the document.
	ID string `json:"@id,omitempty" protobuf:"bytes,1,opt,name=id"`

	// [vul_id] SHOULD use existing and well known identifiers, for example:
	// CVE, the Global Security Database (GSD), or a supplier’s vulnerability
	// tracking system. It is expected that vulnerability identification systems
	// are external to and maintained separately from VEX.
	//
	// [vul_id] MAY be URIs or URLs.
	// [vul_id] MAY be arbitrary and MAY be created by the VEX statement [author].
	Vulnerability VexVulnerability `json:"vulnerability,omitempty" protobuf:"bytes,2,opt,name=vulnerability"`

	// Timestamp is the time at which the information expressed in the Statement
	// was known to be true.
	Timestamp string `json:"timestamp,omitempty" protobuf:"bytes,3,opt,name=timestamp"`

	// LastUpdated records the time when the statement last had a modification
	LastUpdated string `json:"last_updated,omitempty" protobuf:"bytes,4,opt,name=last_updated"`

	// Product
	// Product details MUST specify what Status applies to.
	// Product details MUST include [product_id] and MAY include [subcomponent_id].
	Products []Product `json:"products,omitempty" protobuf:"bytes,5,opt,name=products"`

	// A VEX statement MUST provide Status of the vulnerabilities with respect to the
	// products and components listed in the statement. Status MUST be one of the
	// Status const values, some of which have further options and requirements.
	Status Status `json:"status" protobuf:"bytes,6,req,name=status"`

	// [status_notes] MAY convey information about how [status] was determined
	// and MAY reference other VEX information.
	StatusNotes string `json:"status_notes,omitempty" protobuf:"bytes,7,opt,name=status_notes"`

	// For ”not_affected” status, a VEX statement MUST include a status Justification
	// that further explains the status.
	Justification Justification `json:"justification,omitempty" protobuf:"bytes,8,opt,name=justification"`

	// For ”not_affected” status, a VEX statement MAY include an ImpactStatement
	// that contains a description why the vulnerability cannot be exploited.
	ImpactStatement string `json:"impact_statement,omitempty" protobuf:"bytes,9,opt,name=impact_statement"`

	// For "affected" status, a VEX statement MUST include an ActionStatement that
	// SHOULD describe actions to remediate or mitigate [vul_id].
	ActionStatement          string `json:"action_statement,omitempty" protobuf:"bytes,10,opt,name=action_statement"`
	ActionStatementTimestamp string `json:"action_statement_timestamp,omitempty" protobuf:"bytes,11,opt,name=action_statement_timestamp"`
}

type VEX struct {
	Metadata   `protobuf:"bytes,1,opt,name=metadata"`
	Statements []Statement `json:"statements" protobuf:"bytes,2,rep,name=statements"`
}

type Metadata struct {
	// Context is the URL pointing to the jsonld context definition
	Context string `json:"@context" protobuf:"bytes,1,req,name=context"`

	// ID is the identifying string for the VEX document. This should be unique per
	// document.
	ID string `json:"@id" protobuf:"bytes,2,req,name=id"`

	// Author is the identifier for the author of the VEX statement, ideally a common
	// name, may be a URI. [author] is an individual or organization. [author]
	// identity SHOULD be cryptographically associated with the signature of the VEX
	// statement or document or transport.
	Author string `json:"author" protobuf:"bytes,3,req,name=author"`

	// AuthorRole describes the role of the document Author.
	AuthorRole string `json:"role,omitempty" protobuf:"bytes,4,opt,name=role"`

	// Timestamp defines the time at which the document was issued.
	Timestamp string `json:"timestamp" protobuf:"bytes,5,req,name=timestamp"`

	// LastUpdated marks the time when the document had its last update. When the
	// document changes both version and this field should be updated.
	LastUpdated string `json:"last_updated,omitempty" protobuf:"bytes,6,opt,name=last_updated"`

	// Version is the document version. It must be incremented when any content
	// within the VEX document changes, including any VEX statements included within
	// the VEX document.
	Version int64 `json:"version" protobuf:"bytes,7,req,name=version"`

	// Tooling expresses how the VEX document and contained VEX statements were
	// generated. It's optional. It may specify tools or automated processes used in
	// the document or statement generation.
	Tooling string `json:"tooling,omitempty" protobuf:"bytes,8,opt,name=tooling"`

	// Supplier is an optional field.
	Supplier string `json:"supplier,omitempty" protobuf:"bytes,9,opt,name=supplier"`
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

// SBOMSyftSpec is the specification of a Syft SBOM
type SBOMSyftSpec struct {
	Metadata SPDXMeta     `json:"metadata" protobuf:"bytes,1,req,name=metadata"`
	Syft     SyftDocument `json:"syft,omitempty" protobuf:"bytes,2,opt,name=syft"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyft is a custom resource that describes an SBOM in the Syft format.
type SBOMSyft struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SBOMSyftSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SBOMSyftStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftList is a list of SBOMSyft objects.
type SBOMSyftList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SBOMSyft `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftFiltered is a custom resource that describes a filtered SBOM in the Syft format.
//
// Being filtered means that the SBOM contains only the relevant vulnerable materials.
type SBOMSyftFiltered struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SBOMSyftSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SBOMSyftStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftFilteredList is a list of SBOMSyftFiltered objects.
type SBOMSyftFilteredList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SBOMSyftFiltered `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SeccompProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec   SeccompProfileSpec   `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
	Status SeccompProfileStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

type SeccompProfileSpec struct {
	Containers          []SingleSeccompProfile `json:"containers,omitempty" protobuf:"bytes,1,rep,name=containers"`
	InitContainers      []SingleSeccompProfile `json:"initContainers,omitempty" protobuf:"bytes,2,rep,name=initContainers"`
	EphemeralContainers []SingleSeccompProfile `json:"ephemeralContainers,omitempty" protobuf:"bytes,3,rep,name=ephemeralContainers"`
}

type SingleSeccompProfile struct {
	Name string                   `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	Path string                   `json:"path,omitempty" protobuf:"bytes,2,opt,name=path"`
	Spec SingleSeccompProfileSpec `json:"spec,omitempty" protobuf:"bytes,3,opt,name=spec"`
}

type SeccompProfileStatus struct {
	Containers map[string]SingleSeccompProfileStatus `json:"containers,omitempty" protobuf:"bytes,1,rep,name=containers"`
}

type SingleSeccompProfileSpec struct {
	// Common spec fields for all profiles.
	SpecBase `json:",inline" protobuf:"bytes,1,opt,name=specBase"`

	// BaseProfileName is the name of base profile (in the same namespace) that
	// will be unioned into this profile. Base profiles can be references as
	// remote OCI artifacts as well when prefixed with `oci://`.
	BaseProfileName string `json:"baseProfileName,omitempty" protobuf:"bytes,2,opt,name=baseProfileName"`

	// Properties from containers/common/pkg/seccomp.Seccomp type

	// the default action for seccomp
	DefaultAction seccomp.Action `json:"defaultAction" protobuf:"bytes,3,opt,name=defaultAction"`
	// the architecture used for system calls
	Architectures []Arch `json:"architectures,omitempty" protobuf:"bytes,4,rep,name=architectures"`
	// path of UNIX domain socket to contact a seccomp agent for SCMP_ACT_NOTIFY
	ListenerPath string `json:"listenerPath,omitempty" protobuf:"bytes,5,opt,name=listenerPath"`
	// opaque data to pass to the seccomp agent
	ListenerMetadata string `json:"listenerMetadata,omitempty" protobuf:"bytes,6,opt,name=listenerMetadata"`
	// match a syscall in seccomp. While this property is OPTIONAL, some values
	// of defaultAction are not useful without syscalls entries. For example,
	// if defaultAction is SCMP_ACT_KILL and syscalls is empty or unset, the
	// kernel will kill the container process on its first syscall
	Syscalls []*Syscall `json:"syscalls,omitempty" protobuf:"bytes,7,rep,name=syscalls"`

	// Additional properties from OCI runtime spec

	// list of flags to use with seccomp(2)
	Flags []Flag `json:"flags,omitempty" protobuf:"bytes,8,rep,name=flags"`
}

type Arch string

type Flag string

// Syscall defines a syscall in seccomp.
type Syscall struct {
	// the names of the syscalls
	Names []string `json:"names" protobuf:"bytes,1,rep,name=names"`
	// the action for seccomp rules
	Action seccomp.Action `json:"action" protobuf:"bytes,2,opt,name=action"`
	// the errno return code to use. Some actions like SCMP_ACT_ERRNO and
	// SCMP_ACT_TRACE allow to specify the errno code to return
	ErrnoRet uint64 `json:"errnoRet,omitempty" protobuf:"bytes,3,opt,name=errnoRet"`
	// the specific syscall in seccomp
	Args []*Arg `json:"args,omitempty" protobuf:"bytes,4,rep,name=args"`
}

type RulePolicy struct {
	// +patchStrategy=merge
	// +listType=atomic
	AllowedProcesses []string `json:"processAllowed,omitempty" protobuf:"bytes,1,rep,name=processAllowed" patchStrategy:"merge"`
	AllowedContainer bool     `json:"containerAllowed,omitempty" protobuf:"bytes,2,opt,name=containerAllowed"`
}

type HTTPEndpoint struct {
	Endpoint  string                  `json:"endpoint,omitempty" protobuf:"bytes,1,opt,name=endpoint"`
	Methods   []string                `json:"methods,omitempty" protobuf:"bytes,2,opt,name=methods"`
	Internal  bool                    `json:"internal" protobuf:"bytes,3,opt,name=internal"`
	Direction consts.NetworkDirection `json:"direction,omitempty" protobuf:"bytes,4,opt,name=direction"`
	Headers   json.RawMessage         `json:"headers,omitempty" protobuf:"bytes,5,opt,name=headers"`
}

func (e *HTTPEndpoint) GetHeaders() (map[string][]string, error) {
	headers := make(map[string][]string)

	// Unmarshal the JSON into the map
	err := json.Unmarshal([]byte(e.Headers), &headers)
	if err != nil {
		return nil, err
	}
	return headers, nil
}

// Arg defines the specific syscall in seccomp.
type Arg struct {
	// the index for syscall arguments in seccomp
	Index uint64 `json:"index" protobuf:"bytes,1,opt,name=index"`
	// the value for syscall arguments in seccomp
	Value uint64 `json:"value,omitempty" protobuf:"bytes,2,opt,name=value"`
	// the value for syscall arguments in seccomp
	ValueTwo uint64 `json:"valueTwo,omitempty" protobuf:"bytes,3,opt,name=valueTwo"`
	// the operator for syscall arguments in seccomp
	Op seccomp.Operator `json:"op" protobuf:"bytes,4,opt,name=op"`
}

type SpecBase struct {
	Disabled bool `json:"disabled,omitempty" protobuf:"bytes,1,opt,name=disabled"`
}

type SingleSeccompProfileStatus struct {
	StatusBase      `json:",inline" protobuf:"bytes,1,opt,name=statusBase"`
	Path            string   `json:"path,omitempty" protobuf:"bytes,2,opt,name=path"`
	ActiveWorkloads []string `json:"activeWorkloads,omitempty" protobuf:"bytes,3,opt,name=activeWorkloads"`
	// The path that should be provided to the `securityContext.seccompProfile.localhostProfile`
	// field of a Pod or container spec
	LocalhostProfile string `json:"localhostProfile,omitempty" protobuf:"bytes,4,opt,name=localhostProfile"`
}

type StatusBase struct {
	ConditionedStatus `json:",inline" protobuf:"bytes,1,opt,name=conditionedStatus"`
	Status            ProfileState `json:"status,omitempty" protobuf:"bytes,2,opt,name=status"`
}

type ConditionedStatus struct {
	// Conditions of the resource.
	// +optional
	Conditions []Condition `json:"conditions,omitempty" protobuf:"bytes,1,rep,name=conditions"`
}

type Condition struct {
	// Type of this condition. At most one of each condition type may apply to
	// a resource at any point in time.
	Type ConditionType `json:"type" protobuf:"bytes,1,req,name=type"`

	// Status of this condition; is it currently True, False, or Unknown?
	Status corev1.ConditionStatus `json:"status" protobuf:"bytes,2,req,name=status"`

	// LastTransitionTime is the last time this condition transitioned from one
	// status to another.
	LastTransitionTime metav1.Time `json:"lastTransitionTime" protobuf:"bytes,3,req,name=lastTransitionTime"`

	// A Reason for this condition's last transition from one status to another.
	Reason ConditionReason `json:"reason" protobuf:"bytes,4,req,name=reason"`

	// A Message containing details about this condition's last transition from
	// one status to another, if any.
	// +optional
	Message string `json:"message,omitempty" protobuf:"bytes,5,opt,name=message"`
}

type ConditionType string

type ConditionReason string

type ProfileState string

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SeccompProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SeccompProfile `json:"items" protobuf:"bytes,2,rep,name=items"`
}
