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

// NetworkNeighborhoodList is a list of NetworkNeighborhoods.
type NetworkNeighborhoodList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []NetworkNeighborhood `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// NetworkNeighborhood represents a list of network communications for a specific workload.
type NetworkNeighborhood struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec NetworkNeighborhoodSpec `json:"spec" protobuf:"bytes,2,req,name=spec"`
}

type NetworkNeighborhoodSpec struct {
	metav1.LabelSelector `json:",inline" protobuf:"bytes,3,opt,name=labelSelector"`
	Containers           []NetworkNeighborhoodContainer `json:"containers" protobuf:"bytes,4,rep,name=containers"`
	InitContainers       []NetworkNeighborhoodContainer `json:"initContainers" protobuf:"bytes,5,rep,name=initContainers"`
	EphemeralContainers  []NetworkNeighborhoodContainer `json:"ephemeralContainers" protobuf:"bytes,6,rep,name=ephemeralContainers"`
}

type NetworkNeighborhoodContainer struct {
	Name    string            `json:"name" protobuf:"bytes,1,req,name=name"`
	Ingress []NetworkNeighbor `json:"ingress" protobuf:"bytes,2,rep,name=ingress"`
	Egress  []NetworkNeighbor `json:"egress" protobuf:"bytes,3,rep,name=egress"`
}

// NetworkNeighbor represents a single network communication made by this resource.
type NetworkNeighbor struct {
	Identifier string            `json:"identifier" protobuf:"bytes,1,req,name=identifier"` // A unique identifier for this entry
	Type       CommunicationType `json:"type" protobuf:"bytes,2,req,name=type"`
	DNS        string            `json:"dns" protobuf:"bytes,3,req,name=dns"` // DEPRECATED - use DNSNames instead.
	DNSNames   []string          `json:"dnsNames" protobuf:"bytes,4,rep,name=dnsNames"`
	// +patchMergeKey=name
	// +patchStrategy=merge
	Ports             []NetworkPort         `json:"ports" patchStrategy:"merge" patchMergeKey:"name" protobuf:"bytes,5,rep,name=ports"`
	PodSelector       *metav1.LabelSelector `json:"podSelector" protobuf:"bytes,6,req,name=podSelector"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector" protobuf:"bytes,7,req,name=namespaceSelector"`
	IPAddress         string                `json:"ipAddress" protobuf:"bytes,8,req,name=ipAddress"`
}

type NetworkPort struct {
	Name     string   `json:"name" protobuf:"bytes,1,req,name=name"` // protocol-port
	Protocol Protocol `json:"protocol" protobuf:"bytes,2,req,name=protocol"`
	Port     *int32   `json:"port" protobuf:"bytes,3,req,name=port"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GeneratedNetworkPolicyList is a list of GeneratedNetworkPolicies.
type GeneratedNetworkPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []GeneratedNetworkPolicy `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GeneratedNetworkPolicy represents a generated NetworkPolicy.
type GeneratedNetworkPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec        NetworkPolicy `json:"spec" protobuf:"bytes,2,req,name=spec"`
	PoliciesRef []PolicyRef   `json:"policyRef,omitempty" protobuf:"bytes,3,rep,name=policyRef"`
}

type PolicyRef struct {
	IPBlock    string `json:"ipBlock" protobuf:"bytes,1,req,name=ipBlock"`
	OriginalIP string `json:"originalIP" protobuf:"bytes,2,req,name=originalIP"`
	DNS        string `json:"dns" protobuf:"bytes,3,req,name=dns"`
	Name       string `json:"name" protobuf:"bytes,4,req,name=name"`
	Server     string `json:"server" protobuf:"bytes,5,req,name=server"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KnownServerList is a list of KnownServer.
type KnownServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Items []KnownServer `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// KnownServer represents a known server, containing information about its IP addresses and servers. The purpose is to enrich the GeneratedNetworkPolicy CRD
type KnownServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`

	Spec KnownServerSpec `json:"spec" protobuf:"bytes,2,req,name=spec"`
}

type KnownServerSpec []KnownServerEntry

type KnownServerEntry struct {
	IPBlock string `json:"ipBlock" protobuf:"bytes,1,req,name=ipBlock"`
	Server  string `json:"server" protobuf:"bytes,2,req,name=server"`
	Name    string `json:"name" protobuf:"bytes,3,req,name=name"`
}
