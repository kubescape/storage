package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Protocol string
type CommunicationType string

const (
	ProtocolTCP  Protocol = "TCP"
	ProtocolUDP  Protocol = "UDP"
	ProtocolSCTP Protocol = "SCTP"

	CommunicationTypeIngress CommunicationType = "internal"
	CommunicationTypeEgress  CommunicationType = "external"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkNeighborsList is a list of NetworkNeighbors.
type NetworkNeighborsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []NetworkNeighbors `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkNeighbors represents a list of network communications for a specific workload.
type NetworkNeighbors struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec NetworkNeighborsSpec `json:"spec"`
}

type NetworkNeighborsSpec struct {
	metav1.LabelSelector `json:",inline"`
	// +patchMergeKey=identifier
	// +patchStrategy=merge
	Ingress []NetworkNeighbor `json:"ingress" patchStrategy:"merge" patchMergeKey:"identifier"`
	// +patchMergeKey=identifier
	// +patchStrategy=merge
	Egress []NetworkNeighbor `json:"egress" patchStrategy:"merge" patchMergeKey:"identifier"`
}

// NetworkNeighbor represents a single network communication made by this resource.
type NetworkNeighbor struct {
	Identifier string            `json:"identifier"` // A unique identifier for this entry
	Type       CommunicationType `json:"type"`
	DNS        string            `json:"dns"`
	// +patchMergeKey=name
	// +patchStrategy=merge
	Ports             []NetworkPort         `json:"ports" patchStrategy:"merge" patchMergeKey:"name"`
	PodSelector       *metav1.LabelSelector `json:"podSelector"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector"`
	IPAddress         string                `json:"ipAddress"`
}

type NetworkPort struct {
	Name     string   `json:"name"` // protocol-port
	Protocol Protocol `json:"protocol"`
	Port     *int32   `json:"port"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GeneratedNetworkPolicyList is a list of GeneratedNetworkPolicies.
type GeneratedNetworkPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []GeneratedNetworkPolicy
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GeneratedNetworkPolicy represents a generated NetworkPolicy.
type GeneratedNetworkPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec        GeneratedNetworkPolicySpec `json:"spec"`
	PoliciesRef []PolicyRef                `json:"policyRef"`
}

type GeneratedNetworkPolicySpec struct {
	NetworkPolicy `json:",inline"`
}

type PolicyRef struct {
	IPBlock    string `json:"ipBlock"`
	OriginalIP string `json:"originalIP"`
	DNS        string `json:"dns"`
	Name       string `json:"name"`
}
