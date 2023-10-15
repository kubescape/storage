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
	MatchLabels *metav1.LabelSelector `json:"matchLabels"` // The labels which are inside spec.selector in the parent workload.
	Ingress     []NetworkEntry        `json:"ingress"`
	Egress      []NetworkEntry        `json:"egress"`
}

// NetworkEntry represents a single network communication.
type NetworkEntry struct {
	Identifier        string                `json:"identifier"` // A unique identifier for this entry, used for patching.
	Type              CommunicationType     `json:"type"`
	DNS               string                `json:"dns"`
	IPAddress         string                `json:"ipAddress"`
	Ports             []NetworkPort         `json:"ports"`
	PodSelector       *metav1.LabelSelector `json:"podSelector"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector"`
}

type NetworkPort struct {
	Name     string   `json:"name"` // protocol-port
	Protocol Protocol `json:"protocol"`
	Port     uint16   `json:"port"`
}
