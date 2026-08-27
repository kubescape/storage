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

// NetworkNeighbor represents a single network communication made by this resource.
type NetworkNeighbor struct {
	Identifier        string
	Type              CommunicationType
	DNS               string // DEPRECATED - use DNSNames instead.
	DNSNames          []string
	Ports             []NetworkPort
	PodSelector       *metav1.LabelSelector
	NamespaceSelector *metav1.LabelSelector
	IPAddress         string // DEPRECATED - use IPAddresses instead.
	// IPAddresses is the v0.0.2 list-form replacement for IPAddress.
	// Each entry MAY be a literal IP, a CIDR (a.b.c.d/n), or the "*" sentinel.
	// See pkg/registry/file/networkmatch for matcher semantics.
	IPAddresses []string
	// ServiceRefNamespace/ServiceRefName reference a single Service; the
	// resolver expands it to that Service's ClusterIP(s) + endpoint IPs.
	ServiceRefNamespace string
	ServiceRefName      string
	// ServiceSelector selects Services by label, resolved like ServiceRef.
	ServiceSelector *metav1.LabelSelector
	// Entity is a reserved peer identity not backed by a Service ("host").
	Entity string
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
