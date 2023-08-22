package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanList is a list of workload configuration scan results.
type WorkloadConfigurationScanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []WorkloadConfigurationScan `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScan is a custom resource that describes a configuration scan result of a workload.
type WorkloadConfigurationScan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec WorkloadConfigurationScanSpec `json:"spec"`
}

type WorkloadConfigurationScanSpec struct {
	Controls       map[string]ScannedControl   `json:"controls"`
	RelatedObjects []WorkloadScanRelatedObject `json:"relatedObjects"`
}

type ScannedControl struct {
	ControlID string               `json:"controlID"`
	Name      string               `json:"name"`
	Severity  ControlSeverity      `json:"severity"`
	Status    ScannedControlStatus `json:"status"`
	Rules     []ScannedControlRule `json:"rules"`
}

type ControlSeverity struct {
	Severity    string  `json:"severity"`
	ScoreFactor float32 `json:"scoreFactor"`
}

type ScannedControlStatus struct {
	Status    string `json:"status"`
	SubStatus string `json:"subStatus"`
	Info      string `json:"info"`
}

type ScannedControlRule struct {
	Name                  string              `json:"name"`
	Status                RuleStatus          `json:"status"`
	ControlConfigurations map[string][]string `json:"controlConfigurations"`
	Paths                 []RulePath          `json:"paths"`
	AppliedIgnoreRules    []string            `json:"appliedIgnoreRules"`
	RelatedResourcesIDs   []string            `json:"relatedResourcesIDs"` // ?
}

type RuleStatus struct {
	Status    string `json:"status"`
	SubStatus string `json:"subStatus"`
}

type RulePath struct {
	FailedPath   string `json:"failedPath"`
	FixPath      string `json:"fixPath"`
	FixPathValue string `json:"fixPathValue"`
	FixCommand   string `json:"fixCommand"`
}

type WorkloadScanRelatedObject struct {
	Namespace  string `json:"namespace"`
	APIGroup   string `json:"apiGroup"`
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanSummaryList is a list of WorkloadConfigurationScan summaries.
type WorkloadConfigurationScanSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []WorkloadConfigurationScanSummary `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanSummary is a summary of a WorkloadConfigurationScan
type WorkloadConfigurationScanSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec WorkloadConfigurationScanSummarySpec `json:"spec"`
}

type WorkloadConfigurationScanSummarySpec struct {
	Severities WorkloadConfigurationScanSeveritiesSummary `json:"severities"`
	Controls   map[string]ScannedControlSummary           `json:"controls"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScopedConfigurationScanSummaryList is a list of ScopedConfigurationScanSummary summaries.
type ScopedConfigurationScanSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ScopedConfigurationScanSummary `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ScopedConfigurationScanSummary is a summary for a group of WorkloadConfigurationScanSummary objects for a given scope (ex. namespace).
type ScopedConfigurationScanSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec ScopedConfigurationScanSummarySpec `json:"spec"`
}

type ScopedConfigurationScanSummarySpec struct {
	Severities                                  WorkloadConfigurationScanSeveritiesSummary   `json:"severities"`
	WorkloadConfigurationScanSummaryIdentifiers []WorkloadConfigurationScanSummaryIdentifier `json:"workloadsRef"`
}

type WorkloadConfigurationScanSeveritiesSummary struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

type ScannedControlSummary struct {
	ControlID string               `json:"controlID"`
	Severity  ControlSeverity      `json:"severity"`
	Status    ScannedControlStatus `json:"status"`
}

// WorkloadConfigurationScanSummaryIdentifier includes information needed to identify a WorkloadConfigurationScanSummary object
type WorkloadConfigurationScanSummaryIdentifier struct {
	Namespace string
	Kind      string
	Name      string
}
