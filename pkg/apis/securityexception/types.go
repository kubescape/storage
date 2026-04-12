package securityexception

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecurityException defines a namespaced exception for vulnerability and posture findings.
type SecurityException struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec SecurityExceptionSpec
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SecurityExceptionList is a list of SecurityException resources.
type SecurityExceptionList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []SecurityException
}

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterSecurityException defines a cluster-scoped exception for vulnerability and posture findings.
type ClusterSecurityException struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec SecurityExceptionSpec
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ClusterSecurityExceptionList is a list of ClusterSecurityException resources.
type ClusterSecurityExceptionList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ClusterSecurityException
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
	Author          string
	Reason          string
	ExpiresAt       *metav1.Time
	Match           ExceptionMatch
	Vulnerabilities []VulnerabilityException
	Posture         []PostureException
}

// ExceptionMatch defines which workloads the exception applies to.
type ExceptionMatch struct {
	NamespaceSelector *metav1.LabelSelector
	ObjectSelector    *metav1.LabelSelector
	Resources         []ResourceMatch
	Images            []string
}

// ResourceMatch identifies a workload by kind and optional name.
type ResourceMatch struct {
	APIGroup string
	Kind     string
	Name     string
}

// VulnerabilityException defines an exception for a specific CVE.
type VulnerabilityException struct {
	Vulnerability   VulnerabilityRef
	Status          VulnerabilityStatus
	Justification   string
	ImpactStatement string
	ExpiredOnFix    bool
}

// VulnerabilityRef identifies a vulnerability by CVE ID.
type VulnerabilityRef struct {
	ID      string
	Aliases []string
}

// PostureException defines an exception for a posture control.
type PostureException struct {
	ControlID     string
	FrameworkName string
	Action        PostureAction
}
