package softwarecomposition

import (
	"encoding/json"

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
	ControlConfigurations map[string]json.RawMessage
	Paths                 []RulePath
	AppliedIgnoreRules    []string
	RelatedResourcesIDs   []string
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
	Critical int64
	High     int64
	Medium   int64
	Low      int64
	Unknown  int64
}

type ScannedControlSummary struct {
	ControlID string
	Severity  ControlSeverity
	Status    ScannedControlStatus
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigurationScanSummaryList is a list of ConfigurationScanSummary summaries.
type ConfigurationScanSummaryList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []ConfigurationScanSummary
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigurationScanSummary is a summary for a group of WorkloadConfigurationScanSummary objects for a given scope (ex. namespace).
type ConfigurationScanSummary struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec ConfigurationScanSummarySpec
}

type ConfigurationScanSummarySpec struct {
	Severities                                  WorkloadConfigurationScanSeveritiesSummary
	WorkloadConfigurationScanSummaryIdentifiers []WorkloadConfigurationScanSummaryIdentifier
}

// WorkloadConfigurationScanSummaryIdentifier includes information needed to identify a WorkloadConfigurationScanSummary object
type WorkloadConfigurationScanSummaryIdentifier struct {
	Namespace string
	Kind      string
	Name      string
}
