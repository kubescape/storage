package softwarecomposition

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanList is a list of workload configuration scan results.
type WorkloadConfigurationScanList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []WorkloadConfigurationScan
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScan is a custom resource that describes a configuration scan result of a workload.
type WorkloadConfigurationScan struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec WorkloadConfigurationScanSpec
}

type WorkloadConfigurationScanSpec struct {
	Controls       map[string]ScannedControl
	RelatedObjects []WorkloadScanRelatedObject
}

type ScannedControl struct {
	ControlID string
	Name      string
	Severity  ControlSeverity
	Status    ScannedControlStatus
	Rules     []ScannedControlRule
}

type ControlSeverity struct {
	Severity    string
	ScoreFactor float32
}

type ScannedControlStatus struct {
	Status    string
	SubStatus string
	Info      string
}

type ScannedControlRule struct {
	Name                  string
	Status                RuleStatus
	ControlConfigurations map[string][]string
	Paths                 []RulePath
	AppliedIgnoreRules    []string
	RelatedResourcesIDs   []string // ?
}

type RuleStatus struct {
	Status    string
	SubStatus string
}

type RulePath struct {
	FailedPath   string
	FixPath      string
	FixPathValue string
	FixCommand   string
}

type WorkloadScanRelatedObject struct {
	Namespace  string
	APIGroup   string
	APIVersion string
	Kind       string
	Name       string
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanSummaryList is a list of WorkloadConfigurationScan summaries.
type WorkloadConfigurationScanSummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []WorkloadConfigurationScanSummary
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanSummary is a summary of a WorkloadConfigurationScan
type WorkloadConfigurationScanSummary struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec WorkloadConfigurationScanSummarySpec
}

type WorkloadConfigurationScanSummarySpec struct {
	Severities WorkloadConfigurationScanSeveritiesSummary
	Controls   map[string]ScannedControlSummary
}

type WorkloadConfigurationScanSeveritiesSummary struct {
	Critical int
	High     int
	Medium   int
	Low      int
	Unknown  int
}

type ScannedControlSummary struct {
	ControlID string
	Severity  ControlSeverity
	Status    ScannedControlStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScopedConfigurationScanSummaryList is a list of ScopedConfigurationScanSummary summaries.
type ScopedConfigurationScanSummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ScopedConfigurationScanSummary
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScopedConfigurationScanSummary is a summary for a group of WorkloadConfigurationScanSummary objects for a given scope (ex. namespace).
type ScopedConfigurationScanSummary struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec ScopedConfigurationScanSummarySpec
}

type ScopedConfigurationScanSummarySpec struct {
	Severities          WorkloadConfigurationScanSeveritiesSummary
	WorkloadIdentifiers []WorkloadIdentifier
}

// WorkloadIdentifier includes information needed to identify a workload.
type WorkloadIdentifier struct {
	Namespace string
	Kind      string
	Name      string
}
