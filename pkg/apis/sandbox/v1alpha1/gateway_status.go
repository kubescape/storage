package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// MaxStatusRejectedPatterns bounds the rejected host patterns one node reports.
const MaxStatusRejectedPatterns = 64

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// SandboxGatewayNodeStatus is the state of the sandbox gateway on one node. It
// is cluster-scoped and named after the node, and has no spec. Its single
// writer, the gateway's node controller, creates the object and then updates
// the status subresource.
type SandboxGatewayNodeStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Status            *GatewayNodeStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SandboxGatewayNodeStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxGatewayNodeStatus `json:"items"`
}

// GatewayNodeStatus is a snapshot, not an event log. False and zero are facts
// about the node, so every field except the two optional ones is written.
type GatewayNodeStatus struct {
	Envoy      GatewayEnvoyStatus     `json:"envoy"`
	Processor  GatewayProcessorStatus `json:"processor"`
	Overloaded bool                   `json:"overloaded"`
	Hosts      GatewayHostCounts      `json:"hosts"`
	// RejectedPatterns are host patterns the node refused to decrypt.
	RejectedPatterns []string `json:"rejectedPatterns,omitempty"`
	// PassedThroughOverCap counts names left undecrypted because the node's cap was reached.
	PassedThroughOverCap int64 `json:"passedThroughOverCap"`
	Evictions            int64 `json:"evictions"`
	// DegradedDecisions counts decisions taken without a current policy.
	DegradedDecisions int64 `json:"degradedDecisions"`
	// DecryptValidUntil is the earliest expiry among the certificates the node decrypts with.
	DecryptValidUntil *metav1.Time `json:"decryptValidUntil,omitempty"`
	ObservedAt        metav1.Time  `json:"observedAt"`
}

type GatewayEnvoyStatus struct {
	Up      bool   `json:"up"`
	Version string `json:"version,omitempty"`
}

// GatewayEvaluatorState says whether the processor has a rule evaluator.
// Without one it passes every request.
type GatewayEvaluatorState string

const (
	GatewayEvaluatorAvailable   GatewayEvaluatorState = "available"
	GatewayEvaluatorUnavailable GatewayEvaluatorState = "unavailable"
)

type GatewayProcessorStatus struct {
	Up        bool                  `json:"up"`
	Evaluator GatewayEvaluatorState `json:"evaluator"`
}

// GatewayHostCounts counts decrypted names against the node's cap.
type GatewayHostCounts struct {
	Exact   int64 `json:"exact"`
	Learned int64 `json:"learned"`
	Cap     int64 `json:"cap"`
}
