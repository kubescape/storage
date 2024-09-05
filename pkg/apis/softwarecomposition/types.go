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

package softwarecomposition

import (
	"strings"

	"github.com/containers/common/pkg/seccomp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

// VulnerabilityCounters describes a counter of vulnerabilities.
//
// Intended to store relevant and total vulnerabilities in the future.
type VulnerabilityCounters struct {
	All      int64
	Relevant int64
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
	Architectures       []string
	Containers          []ApplicationProfileContainer
	InitContainers      []ApplicationProfileContainer
	EphemeralContainers []ApplicationProfileContainer
}

type ApplicationProfileContainer struct {
	Name           string
	Capabilities   []string
	Execs          []ExecCalls
	Opens          []OpenCalls
	Syscalls       []string
	SeccompProfile SingleSeccompProfile
}

type ExecCalls struct {
	Path string
	Args []string
	Envs []string
}

const sep = "␟"

func (e ExecCalls) String() string {
	s := strings.Builder{}
	s.WriteString(e.Path)
	for _, arg := range e.Args {
		s.WriteString(sep)
		s.WriteString(arg)
	}
	// FIXME should we sort the envs?
	for _, env := range e.Envs {
		s.WriteString(sep)
		s.WriteString(env)
	}
	return s.String()
}

type OpenCalls struct {
	Path  string
	Flags []string
}

func (e OpenCalls) String() string {
	s := strings.Builder{}
	s.WriteString(e.Path)
	// FIXME should we sort the flags?
	for _, flag := range e.Flags {
		s.WriteString(sep)
		s.WriteString(flag)
	}
	return s.String()
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
	ID string

	// Hashes is a map of hashes to identify the component using cryptographic
	// hashes.
	Hashes map[Algorithm]Hash

	// Identifiers is a list of software identifiers that describe the component.
	Identifiers map[IdentifierType]string

	// Supplier is an optional machine-readable identifier for the supplier of
	// the component. Valid examples include email address or IRIs.
	Supplier string
}

type Product struct {
	Component
	Subcomponents []Subcomponent
}

type Subcomponent struct {
	Component
}

type VexVulnerability struct {
	//  ID is an IRI to reference the vulnerability in the statement.
	ID string

	// Name is the main vulnerability identifier.
	Name string

	// Description is a short free form text description of the vulnerability.
	Description string

	// Aliases is a list of other vulnerability identifier strings that
	// locate the vulnerability in other tracking systems.
	Aliases []string
}

type Statement struct {
	// ID is an optional identifier for the statement. It takes an IRI and must
	// be unique for each statement in the document.
	ID string

	// [vul_id] SHOULD use existing and well known identifiers, for example:
	// CVE, the Global Security Database (GSD), or a supplier’s vulnerability
	// tracking system. It is expected that vulnerability identification systems
	// are external to and maintained separately from VEX.
	//
	// [vul_id] MAY be URIs or URLs.
	// [vul_id] MAY be arbitrary and MAY be created by the VEX statement [author].
	Vulnerability VexVulnerability

	// Timestamp is the time at which the information expressed in the Statement
	// was known to be true.
	Timestamp string

	// LastUpdated records the time when the statement last had a modification
	LastUpdated string

	// Product
	// Product details MUST specify what Status applies to.
	// Product details MUST include [product_id] and MAY include [subcomponent_id].
	Products []Product

	// A VEX statement MUST provide Status of the vulnerabilities with respect to the
	// products and components listed in the statement. Status MUST be one of the
	// Status const values, some of which have further options and requirements.
	Status Status

	// [status_notes] MAY convey information about how [status] was determined
	// and MAY reference other VEX information.
	StatusNotes string

	// For ”not_affected” status, a VEX statement MUST include a status Justification
	// that further explains the status.
	Justification Justification

	// For ”not_affected” status, a VEX statement MAY include an ImpactStatement
	// that contains a description why the vulnerability cannot be exploited.
	ImpactStatement string

	// For "affected" status, a VEX statement MUST include an ActionStatement that
	// SHOULD describe actions to remediate or mitigate [vul_id].
	ActionStatement          string
	ActionStatementTimestamp string
}

type VEX struct {
	Metadata
	Statements []Statement
}

type Metadata struct {
	// Context is the URL pointing to the jsonld context definition
	Context string

	// ID is the identifying string for the VEX document. This should be unique per
	// document.
	ID string

	// Author is the identifier for the author of the VEX statement, ideally a common
	// name, may be a URI. [author] is an individual or organization. [author]
	// identity SHOULD be cryptographically associated with the signature of the VEX
	// statement or document or transport.
	Author string

	// AuthorRole describes the role of the document Author.
	AuthorRole string

	// Timestamp defines the time at which the document was issued.
	Timestamp string

	// LastUpdated marks the time when the document had its last update. When the
	// document changes both version and this field should be updated.
	LastUpdated string

	// Version is the document version. It must be incremented when any content
	// within the VEX document changes, including any VEX statements included within
	// the VEX document.
	Version int64

	// Tooling expresses how the VEX document and contained VEX statements were
	// generated. It's optional. It may specify tools or automated processes used in
	// the document or statement generation.
	Tooling string

	// Supplier is an optional field.
	Supplier string
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

// SBOMSyftSpec is the specification of a Syft SBOM
type SBOMSyftSpec struct {
	Metadata SPDXMeta
	Syft     SyftDocument
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyft is a custom resource that describes an SBOM in the Syft format.
type SBOMSyft struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SBOMSyftSpec
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
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SBOMSyftSpec
	Status SBOMSyftStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SBOMSyftFilteredList is a list of SBOMSyftFiltered objects.
type SBOMSyftFilteredList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SBOMSyftFiltered
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SeccompProfile struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec   SeccompProfileSpec
	Status SeccompProfileStatus
}

type SeccompProfileSpec struct {
	Containers          []SingleSeccompProfile
	InitContainers      []SingleSeccompProfile
	EphemeralContainers []SingleSeccompProfile
}

type SingleSeccompProfile struct {
	Name string
	Path string
	Spec SingleSeccompProfileSpec
}

type SeccompProfileStatus struct {
	Containers map[string]SingleSeccompProfileStatus
}

type SingleSeccompProfileSpec struct {
	// Common spec fields for all profiles.
	SpecBase

	// BaseProfileName is the name of base profile (in the same namespace) that
	// will be unioned into this profile. Base profiles can be references as
	// remote OCI artifacts as well when prefixed with `oci://`.
	BaseProfileName string

	// Properties from containers/common/pkg/seccomp.Seccomp type

	// the default action for seccomp
	DefaultAction seccomp.Action
	// the architecture used for system calls
	Architectures []Arch
	// path of UNIX domain socket to contact a seccomp agent for SCMP_ACT_NOTIFY
	ListenerPath string
	// opaque data to pass to the seccomp agent
	ListenerMetadata string
	// match a syscall in seccomp. While this property is OPTIONAL, some values
	// of defaultAction are not useful without syscalls entries. For example,
	// if defaultAction is SCMP_ACT_KILL and syscalls is empty or unset, the
	// kernel will kill the container process on its first syscall
	Syscalls []*Syscall

	// Additional properties from OCI runtime spec

	// list of flags to use with seccomp(2)
	Flags []Flag
}

type Arch string

type Flag string

// Syscall defines a syscall in seccomp.
type Syscall struct {
	// the names of the syscalls
	Names []string
	// the action for seccomp rules
	Action seccomp.Action
	// the errno return code to use. Some actions like SCMP_ACT_ERRNO and
	// SCMP_ACT_TRACE allow to specify the errno code to return
	ErrnoRet uint64
	// the specific syscall in seccomp
	Args []*Arg
}

// Arg defines the specific syscall in seccomp.
type Arg struct {
	// the index for syscall arguments in seccomp
	Index uint64
	// the value for syscall arguments in seccomp
	Value uint64
	// the value for syscall arguments in seccomp
	ValueTwo uint64
	// the operator for syscall arguments in seccomp
	Op seccomp.Operator
}

type SpecBase struct {
	Disabled bool
}

type SingleSeccompProfileStatus struct {
	StatusBase
	Path            string
	ActiveWorkloads []string
	// The path that should be provided to the `securityContext.seccompProfile.localhostProfile`
	// field of a Pod or container spec
	LocalhostProfile string
}

type StatusBase struct {
	ConditionedStatus
	Status ProfileState
}

type ConditionedStatus struct {
	// Conditions of the resource.
	// +optional
	Conditions []Condition
}

type Condition struct {
	// Type of this condition. At most one of each condition type may apply to
	// a resource at any point in time.
	Type ConditionType

	// Status of this condition; is it currently True, False, or Unknown?
	Status corev1.ConditionStatus

	// LastTransitionTime is the last time this condition transitioned from one
	// status to another.
	LastTransitionTime metav1.Time

	// A Reason for this condition's last transition from one status to another.
	Reason ConditionReason

	// A Message containing details about this condition's last transition from
	// one status to another, if any.
	// +optional
	Message string
}

type ConditionType string

type ConditionReason string

type ProfileState string

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SeccompProfileList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SeccompProfile
}
