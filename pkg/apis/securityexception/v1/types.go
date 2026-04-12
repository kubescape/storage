package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecurityException defines a namespaced exception for vulnerability and posture findings.
type SecurityException struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec SecurityExceptionSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecurityExceptionList is a list of SecurityException resources.
type SecurityExceptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []SecurityException `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterSecurityException defines a cluster-scoped exception for vulnerability and posture findings.
type ClusterSecurityException struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec SecurityExceptionSpec `json:"spec,omitempty" protobuf:"bytes,2,opt,name=spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterSecurityExceptionList is a list of ClusterSecurityException resources.
type ClusterSecurityExceptionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ClusterSecurityException `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// VulnerabilityStatus is the VEX status of a vulnerability exception.
type VulnerabilityStatus string

const (
	VulnerabilityStatusNotAffected        VulnerabilityStatus = "not_affected"
	VulnerabilityStatusFixed              VulnerabilityStatus = "fixed"
	VulnerabilityStatusUnderInvestigation VulnerabilityStatus = "under_investigation"
)

// PostureAction is the action to take for a posture exception.
type PostureAction string

const (
	PostureActionIgnore    PostureAction = "ignore"
	PostureActionAlertOnly PostureAction = "alert_only"
)

// SecurityExceptionSpec defines the desired state of a SecurityException.
type SecurityExceptionSpec struct {
	// Author is an optional identifier for who created this exception.
	Author string `json:"author,omitempty" protobuf:"bytes,1,opt,name=author"`
	// Reason explains why this exception exists.
	Reason string `json:"reason,omitempty" protobuf:"bytes,2,opt,name=reason"`
	// ExpiresAt is an optional expiry time after which the exception is ignored.
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty" protobuf:"bytes,3,opt,name=expiresAt"`
	// Match defines which workloads the exception applies to.
	Match ExceptionMatch `json:"match,omitempty" protobuf:"bytes,4,opt,name=match"`
	// Vulnerabilities lists vulnerability exceptions (CVE-based).
	// +listType=atomic
	Vulnerabilities []VulnerabilityException `json:"vulnerabilities,omitempty" protobuf:"bytes,5,rep,name=vulnerabilities"`
	// Posture lists posture/compliance control exceptions.
	// +listType=atomic
	Posture []PostureException `json:"posture,omitempty" protobuf:"bytes,6,rep,name=posture"`
}

// ExceptionMatch defines which workloads the exception applies to.
type ExceptionMatch struct {
	// NamespaceSelector selects namespaces by label. Only meaningful on ClusterSecurityException.
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty" protobuf:"bytes,1,opt,name=namespaceSelector"`
	// ObjectSelector selects workloads by their labels.
	ObjectSelector *metav1.LabelSelector `json:"objectSelector,omitempty" protobuf:"bytes,2,opt,name=objectSelector"`
	// Resources is an explicit list of workloads by kind/name.
	// +listType=atomic
	Resources []ResourceMatch `json:"resources,omitempty" protobuf:"bytes,3,rep,name=resources"`
	// Images is a list of glob patterns matched against container image references.
	// +listType=atomic
	Images []string `json:"images,omitempty" protobuf:"bytes,4,rep,name=images"`
}

// ResourceMatch identifies a workload by kind and optional name.
type ResourceMatch struct {
	// APIGroup is the API group (e.g., "apps", "" for core). Optional.
	APIGroup string `json:"apiGroup,omitempty" protobuf:"bytes,1,opt,name=apiGroup"`
	// Kind is the resource kind (e.g., "Deployment"). Required.
	Kind string `json:"kind" protobuf:"bytes,2,opt,name=kind"`
	// Name is the exact resource name. Optional — omit to match all of this kind.
	Name string `json:"name,omitempty" protobuf:"bytes,3,opt,name=name"`
}

// VulnerabilityException defines an exception for a specific CVE.
type VulnerabilityException struct {
	// Vulnerability identifies the CVE.
	Vulnerability VulnerabilityRef `json:"vulnerability" protobuf:"bytes,1,opt,name=vulnerability"`
	// Status is the VEX status: not_affected, fixed, under_investigation.
	Status VulnerabilityStatus `json:"status" protobuf:"bytes,2,opt,name=status"`
	// Justification is the VEX justification when status is not_affected.
	Justification string `json:"justification,omitempty" protobuf:"bytes,3,opt,name=justification"`
	// ImpactStatement is a freeform explanation of the exception.
	ImpactStatement string `json:"impactStatement,omitempty" protobuf:"bytes,4,opt,name=impactStatement"`
	// ExpiredOnFix when true skips this exception when a fix is available.
	ExpiredOnFix bool `json:"expiredOnFix,omitempty" protobuf:"varint,5,opt,name=expiredOnFix"`
}

// VulnerabilityRef identifies a vulnerability by CVE ID.
type VulnerabilityRef struct {
	// ID is the CVE identifier (e.g., "CVE-2021-44228").
	ID string `json:"id" protobuf:"bytes,1,opt,name=id"`
	// Aliases are alternative identifiers (e.g., GHSA IDs).
	// +listType=atomic
	Aliases []string `json:"aliases,omitempty" protobuf:"bytes,2,rep,name=aliases"`
}

// PostureException defines an exception for a posture control.
type PostureException struct {
	// ControlID is the control identifier (e.g., "C-0034").
	ControlID string `json:"controlID" protobuf:"bytes,1,opt,name=controlID"`
	// FrameworkName is the framework the control belongs to (e.g., "NSA").
	FrameworkName string `json:"frameworkName,omitempty" protobuf:"bytes,2,opt,name=frameworkName"`
	// Action is the exception action: ignore or alert_only.
	Action PostureAction `json:"action" protobuf:"bytes,3,opt,name=action"`
}
