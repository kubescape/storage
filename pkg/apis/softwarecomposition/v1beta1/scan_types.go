package v1beta1

import (
	"encoding/json"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanList is a list of workload configuration scan results.
type WorkloadConfigurationScanList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []WorkloadConfigurationScan `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScan is a custom resource that describes a configuration scan result of a workload.
type WorkloadConfigurationScan struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec WorkloadConfigurationScanSpec `json:"spec" protobuf:"bytes,2,req,name=spec"`
}

type WorkloadConfigurationScanSpec struct {
	Controls       map[string]ScannedControl   `json:"controls" protobuf:"bytes,2,rep,name=controls"`
	RelatedObjects []WorkloadScanRelatedObject `json:"relatedObjects" protobuf:"bytes,3,rep,name=relatedObjects"`
}

type ScannedControl struct {
	ControlID string               `json:"controlID" protobuf:"bytes,1,req,name=controlID"`
	Name      string               `json:"name" protobuf:"bytes,2,req,name=name"`
	Severity  ControlSeverity      `json:"severity" protobuf:"bytes,3,req,name=severity"`
	Status    ScannedControlStatus `json:"status" protobuf:"bytes,4,req,name=status"`
	Rules     []ScannedControlRule `json:"rules" protobuf:"bytes,5,rep,name=rules"`
}

type ControlSeverity struct {
	Severity    string  `json:"severity" protobuf:"bytes,1,req,name=severity"`
	ScoreFactor float32 `json:"scoreFactor" protobuf:"bytes,2,req,name=scoreFactor"`
}

type ScannedControlStatus struct {
	Status    string `json:"status" protobuf:"bytes,1,req,name=status"`
	SubStatus string `json:"subStatus" protobuf:"bytes,2,req,name=subStatus"`
	Info      string `json:"info" protobuf:"bytes,3,req,name=info"`
}

type ScannedControlRule struct {
	Name                  string                     `json:"name" protobuf:"bytes,1,req,name=name"`
	Status                RuleStatus                 `json:"status" protobuf:"bytes,2,req,name=status"`
	ControlConfigurations map[string]json.RawMessage `json:"controlConfigurations" protobuf:"bytes,3,rep,name=controlConfigurations"`
	Paths                 []RulePath                 `json:"paths" protobuf:"bytes,4,rep,name=paths"`
	AppliedIgnoreRules    []string                   `json:"appliedIgnoreRules" protobuf:"bytes,5,rep,name=appliedIgnoreRules"`
	RelatedResourcesIDs   []string                   `json:"relatedResourcesIDs" protobuf:"bytes,6,rep,name=relatedResourcesIDs"`
}

type RuleStatus struct {
	Status    string `json:"status" protobuf:"bytes,1,req,name=status"`
	SubStatus string `json:"subStatus" protobuf:"bytes,2,req,name=subStatus"`
}

type RulePath struct {
	FailedPath   string `json:"failedPath" protobuf:"bytes,1,req,name=failedPath"`
	FixPath      string `json:"fixPath" protobuf:"bytes,2,req,name=fixPath"`
	FixPathValue string `json:"fixPathValue" protobuf:"bytes,3,req,name=fixPathValue"`
	FixCommand   string `json:"fixCommand" protobuf:"bytes,4,req,name=fixCommand"`
}

type WorkloadScanRelatedObject struct {
	Namespace  string `json:"namespace" protobuf:"bytes,1,req,name=namespace"`
	APIGroup   string `json:"apiGroup" protobuf:"bytes,2,req,name=apiGroup"`
	APIVersion string `json:"apiVersion" protobuf:"bytes,3,req,name=apiVersion"`
	Kind       string `json:"kind" protobuf:"bytes,4,req,name=kind"`
	Name       string `json:"name" protobuf:"bytes,5,req,name=name"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanSummaryList is a list of WorkloadConfigurationScan summaries.
type WorkloadConfigurationScanSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []WorkloadConfigurationScanSummary `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// WorkloadConfigurationScanSummary is a summary of a WorkloadConfigurationScan
type WorkloadConfigurationScanSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec WorkloadConfigurationScanSummarySpec `json:"spec" protobuf:"bytes,2,req,name=spec"`
}

type WorkloadConfigurationScanSummarySpec struct {
	Severities WorkloadConfigurationScanSeveritiesSummary `json:"severities" protobuf:"bytes,1,req,name=severities"`
	Controls   map[string]ScannedControlSummary           `json:"controls" protobuf:"bytes,2,rep,name=controls"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigurationScanSummaryList is a list of ConfigurationScanSummary summaries.
type ConfigurationScanSummaryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []ConfigurationScanSummary `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConfigurationScanSummary is a summary for a group of WorkloadConfigurationScanSummary objects for a given scope (ex. namespace).
type ConfigurationScanSummary struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec ConfigurationScanSummarySpec `json:"spec" protobuf:"bytes,2,req,name=spec"`
}

type ConfigurationScanSummarySpec struct {
	Severities                                  WorkloadConfigurationScanSeveritiesSummary   `json:"severities" protobuf:"bytes,1,req,name=severities"`
	WorkloadConfigurationScanSummaryIdentifiers []WorkloadConfigurationScanSummaryIdentifier `json:"summaryRef" protobuf:"bytes,2,rep,name=summaryRef"`
}

type WorkloadConfigurationScanSeveritiesSummary struct {
	Critical int64 `json:"critical" protobuf:"bytes,1,req,name=critical"`
	High     int64 `json:"high" protobuf:"bytes,2,req,name=high"`
	Medium   int64 `json:"medium" protobuf:"bytes,3,req,name=medium"`
	Low      int64 `json:"low" protobuf:"bytes,4,req,name=low"`
	Unknown  int64 `json:"unknown" protobuf:"bytes,5,req,name=unknown"`
}

type ScannedControlSummary struct {
	ControlID string               `json:"controlID" protobuf:"bytes,1,req,name=controlID"`
	Severity  ControlSeverity      `json:"severity" protobuf:"bytes,2,req,name=severity"`
	Status    ScannedControlStatus `json:"status" protobuf:"bytes,3,req,name=status"`
}

// WorkloadConfigurationScanSummaryIdentifier includes information needed to identify a WorkloadConfigurationScanSummary object
type WorkloadConfigurationScanSummaryIdentifier struct {
	Namespace string `json:"namespace" protobuf:"bytes,1,req,name=namespace"`
	Kind      string `json:"kind" protobuf:"bytes,2,req,name=kind"`
	Name      string `json:"name" protobuf:"bytes,3,req,name=name"`
}
