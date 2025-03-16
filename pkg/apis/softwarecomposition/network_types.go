package softwarecomposition

import (
	"net"

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

// NetworkNeighborhoodList is a list of NetworkNeighborhoods.
type NetworkNeighborhoodList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []NetworkNeighborhood
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkNeighborhood represents a list of network communications for a specific workload.
type NetworkNeighborhood struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec NetworkNeighborhoodSpec
}

type NetworkNeighborhoodSpec struct {
	metav1.LabelSelector // The labels which are inside spec.selector in the parent workload.
	Containers           []NetworkNeighborhoodContainer
	InitContainers       []NetworkNeighborhoodContainer
	EphemeralContainers  []NetworkNeighborhoodContainer
}

type NetworkNeighborhoodContainer struct {
	Name    string
	Ingress []NetworkNeighbor
	Egress  []NetworkNeighbor
}

// NetworkNeighbor represents a single network communication made by this resource.
type NetworkNeighbor struct {
	Identifier        string
	Type              CommunicationType
	DNS               string // DEPRECATED - use DNSNames instead.
	DNSNames          []string
	Ports             []NetworkPort
	PodSelector       *metav1.LabelSelector
	NamespaceSelector *metav1.LabelSelector
	IPAddress         string
}

type NetworkPort struct {
	// Name is an artificial identifier of the network port. We use it for merging keys with Strategic Merge Patch.
	// Format is `{protocol}-{port}`.
	//
	// Example: tcp-6881
	Name     string // protocol-port
	Protocol Protocol
	Port     *int32
}

func (p NetworkPort) String() string {
	return p.Name
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GeneratedNetworkPolicyList is a list of GeneratedNetworkPolicies.
type GeneratedNetworkPolicyList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []GeneratedNetworkPolicy
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GeneratedNetworkPolicy represents a generated NetworkPolicy.
type GeneratedNetworkPolicy struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec        NetworkPolicy
	PoliciesRef []PolicyRef
}

type PolicyRef struct {
	IPBlock    string
	OriginalIP string
	DNS        string
	Name       string
	Server     string
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KnownServerList is a list of KnownServer.
type KnownServerList struct {
	metav1.TypeMeta
	metav1.ListMeta

	Items []KnownServer
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KnownServer represents a known server, containing information about its IP addresses and servers. The purpose is to enrich the GeneratedNetworkPolicy CRD
type KnownServer struct {
	metav1.TypeMeta
	metav1.ObjectMeta

	Spec KnownServerSpec
}

type KnownServerSpec []KnownServerEntry

type KnownServerEntry struct {
	IPBlock string
	Server  string
	Name    string
}

type IKnownServerEntry interface {
	GetIPBlock() string
	GetName() string
	GetServer() string
}

type IKnownServersFinder interface {
	Contains(ip net.IP) ([]IKnownServerEntry, bool)
}
