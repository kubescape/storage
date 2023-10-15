package softwarecomposition

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
	metav1.TypeMeta
	metav1.ListMeta

	Items []NetworkNeighbors
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkNeighbors represents a list of network communications for a specific workload.
type NetworkNeighbors struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec NetworkNeighborsSpec
}

type NetworkNeighborsSpec struct {
	MatchLabels *metav1.LabelSelector // The labels which are inside spec.selector in the parent workload.
	Ingress     []NetworkEntry
	Egress      []NetworkEntry
}

// NetworkEntry represents a single network communication.
type NetworkEntry struct {
	Identifier        string // A unique identifier for this entry, used for patching.
	Type              CommunicationType
	DNS               string
	IPAddress         string
	Ports             []NetworkPort
	PodSelector       *metav1.LabelSelector
	NamespaceSelector *metav1.LabelSelector
}

type NetworkPort struct {
	Name     string // protocol-port
	Protocol Protocol
	Port     uint16
}
