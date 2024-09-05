package softwarecomposition

import (
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NetworkPolicy describes what network traffic is allowed for a set of Pods
type NetworkPolicy struct {
	Kind       string
	APIVersion string

	metav1.ObjectMeta

	// spec represents the specification of the desired behavior for this NetworkPolicy.

	Spec NetworkPolicySpec
}

// PolicyType string describes the NetworkPolicy type
// This type is beta-level in 1.8
// +enum
type PolicyType string

const (
	// PolicyTypeIngress is a NetworkPolicy that affects ingress traffic on selected pods
	PolicyTypeIngress PolicyType = "Ingress"
	// PolicyTypeEgress is a NetworkPolicy that affects egress traffic on selected pods
	PolicyTypeEgress PolicyType = "Egress"
)

// NetworkPolicySpec provides the specification of a NetworkPolicy
type NetworkPolicySpec struct {
	PodSelector metav1.LabelSelector
	Ingress     []NetworkPolicyIngressRule

	Egress []NetworkPolicyEgressRule

	PolicyTypes []PolicyType
}

// NetworkPolicyIngressRule describes a particular set of traffic that is allowed to the pods
// matched by a NetworkPolicySpec's podSelector. The traffic must match both ports and from.
type NetworkPolicyIngressRule struct {
	Ports []NetworkPolicyPort

	From []NetworkPolicyPeer
}

// NetworkPolicyEgressRule describes a particular set of traffic that is allowed out of pods
// matched by a NetworkPolicySpec's podSelector. The traffic must match both ports and to.
// This type is beta-level in 1.8
type NetworkPolicyEgressRule struct {
	Ports []NetworkPolicyPort

	To []NetworkPolicyPeer
}

// NetworkPolicyPort describes a port to allow traffic on
type NetworkPolicyPort struct {
	Protocol *v1.Protocol

	Port *int32

	EndPort *int32
}

type Type int64

// IPBlock describes a particular CIDR (Ex. "192.168.1.0/24","2001:db8::/64") that is allowed
// to the pods matched by a NetworkPolicySpec's podSelector. The except entry describes CIDRs
// that should not be included within this rule.
type IPBlock struct {
	// cidr is a string representing the IPBlock
	// Valid examples are "192.168.1.0/24" or "2001:db8::/64"
	CIDR string

	Except []string
}

// NetworkPolicyPeer describes a peer to allow traffic to/from. Only certain combinations of
// fields are allowed
type NetworkPolicyPeer struct {
	PodSelector *metav1.LabelSelector

	// namespaceSelector selects namespaces using cluster-scoped labels. This field follows
	// standard label selector semantics; if present but empty, it selects all namespaces.
	//
	// If podSelector is also set, then the NetworkPolicyPeer as a whole selects
	// the pods matching podSelector in the namespaces selected by namespaceSelector.
	// Otherwise it selects all pods in the namespaces selected by namespaceSelector.

	NamespaceSelector *metav1.LabelSelector

	// ipBlock defines policy on a particular IPBlock. If this field is set then
	// neither of the other fields can be.

	IPBlock *IPBlock
}

// NetworkPolicyConditionType is the type for status conditions on
// a NetworkPolicy. This type should be used with the
// NetworkPolicyStatus.Conditions field.
type NetworkPolicyConditionType string

const (
	// NetworkPolicyConditionStatusAccepted represents status of a Network Policy that could be properly parsed by
	// the Network Policy provider and will be implemented in the cluster
	NetworkPolicyConditionStatusAccepted NetworkPolicyConditionType = "Accepted"

	// NetworkPolicyConditionStatusPartialFailure represents status of a Network Policy that could be partially
	// parsed by the Network Policy provider and may not be completely implemented due to a lack of a feature or some
	// other condition
	NetworkPolicyConditionStatusPartialFailure NetworkPolicyConditionType = "PartialFailure"

	// NetworkPolicyConditionStatusFailure represents status of a Network Policy that could not be parsed by the
	// Network Policy provider and will not be implemented in the cluster
	NetworkPolicyConditionStatusFailure NetworkPolicyConditionType = "Failure"
)

// NetworkPolicyConditionReason defines the set of reasons that explain why a
// particular NetworkPolicy condition type has been raised.
type NetworkPolicyConditionReason string

const (
	// NetworkPolicyConditionReasonFeatureNotSupported represents a reason where the Network Policy may not have been
	// implemented in the cluster due to a lack of some feature not supported by the Network Policy provider
	NetworkPolicyConditionReasonFeatureNotSupported NetworkPolicyConditionReason = "FeatureNotSupported"
)

// NetworkPolicyStatus describes the current state of the NetworkPolicy.
type NetworkPolicyStatus struct {
	// conditions holds an array of metav1.Condition that describe the state of the NetworkPolicy.
	// Current service state

	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition
}

// NetworkPolicyList is a list of NetworkPolicy objects.
type NetworkPolicyList struct {
	metav1.TypeMeta

	// Standard list metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata

	metav1.ListMeta

	// items is a list of schema objects.
	Items []NetworkPolicy
}

// Ingress is a collection of rules that allow inbound connections to reach the
// endpoints defined by a backend. An Ingress can be configured to give services
// externally-reachable urls, load balance traffic, terminate SSL, offer name
// based virtual hosting etc.
type Ingress struct {
	metav1.TypeMeta

	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata

	metav1.ObjectMeta

	// spec is the desired state of the Ingress.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status

	Spec IngressSpec

	// status is the current state of the Ingress.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status

	Status IngressStatus
}

// IngressList is a collection of Ingress.
type IngressList struct {
	metav1.TypeMeta

	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata

	metav1.ListMeta

	// items is the list of Ingress.
	Items []Ingress
}

// IngressSpec describes the Ingress the user wishes to exist.
type IngressSpec struct {
	// ingressClassName is the name of an IngressClass cluster resource. Ingress
	// controller implementations use this field to know whether they should be
	// serving this Ingress resource, by a transitive connection
	// (controller -> IngressClass -> Ingress resource). Although the
	// `kubernetes.io/ingress.class` annotation (simple constant name) was never
	// formally defined, it was widely supported by Ingress controllers to create
	// a direct binding between Ingress controller and Ingress resources. Newly
	// created Ingress resources should prefer using the field. However, even
	// though the annotation is officially deprecated, for backwards compatibility
	// reasons, ingress controllers should still honor that annotation if present.

	IngressClassName *string

	// defaultBackend is the backend that should handle requests that don't
	// match any rule. If Rules are not specified, DefaultBackend must be specified.
	// If DefaultBackend is not set, the handling of requests that do not match any
	// of the rules will be up to the Ingress controller.

	DefaultBackend *IngressBackend

	// tls represents the TLS configuration. Currently the Ingress only supports a
	// single TLS port, 443. If multiple members of this list specify different hosts,
	// they will be multiplexed on the same port according to the hostname specified
	// through the SNI TLS extension, if the ingress controller fulfilling the
	// ingress supports SNI.
	// +listType=atomic

	TLS []IngressTLS

	// rules is a list of host rules used to configure the Ingress. If unspecified,
	// or no rule matches, all traffic is sent to the default backend.
	// +listType=atomic

	Rules []IngressRule
}

// IngressTLS describes the transport layer security associated with an ingress.
type IngressTLS struct {
	// hosts is a list of hosts included in the TLS certificate. The values in
	// this list must match the name/s used in the tlsSecret. Defaults to the
	// wildcard host setting for the loadbalancer controller fulfilling this
	// Ingress, if left unspecified.
	// +listType=atomic

	Hosts []string

	// secretName is the name of the secret used to terminate TLS traffic on
	// port 443. Field is left optional to allow TLS routing based on SNI
	// hostname alone. If the SNI host in a listener conflicts with the "Host"
	// header field used by an IngressRule, the SNI host is used for termination
	// and value of the "Host" header is used for routing.

	SecretName string
}

// IngressStatus describe the current state of the Ingress.
type IngressStatus struct {
	// loadBalancer contains the current status of the load-balancer.

	LoadBalancer IngressLoadBalancerStatus
}

// IngressLoadBalancerStatus represents the status of a load-balancer.
type IngressLoadBalancerStatus struct {
	// ingress is a list containing ingress points for the load-balancer.

	Ingress []IngressLoadBalancerIngress
}

// IngressLoadBalancerIngress represents the status of a load-balancer ingress point.
type IngressLoadBalancerIngress struct {
	// ip is set for load-balancer ingress points that are IP based.

	IP string

	// hostname is set for load-balancer ingress points that are DNS based.

	Hostname string

	// ports provides information about the ports exposed by this LoadBalancer.
	// +listType=atomic

	Ports []IngressPortStatus
}

// IngressPortStatus represents the error condition of a service port
type IngressPortStatus struct {
	// port is the port number of the ingress port.
	Port int32

	// protocol is the protocol of the ingress port.
	// The supported values are: "TCP", "UDP", "SCTP"
	Protocol v1.Protocol

	// error is to record the problem with the service port
	// The format of the error shall comply with the following rules:
	// - built-in error values shall be specified in this file and those shall use
	//   CamelCase names
	// - cloud provider specific error values must have names that comply with the
	//   format foo.example.com/CamelCase.
	// ---
	// The regex it matches is (dns1123SubdomainFmt/)?(qualifiedNameFmt)

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*/)?(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])$`
	// +kubebuilder:validation:MaxLength=316
	Error *string
}

// IngressRule represents the rules mapping the paths under a specified host to
// the related backend services. Incoming requests are first evaluated for a host
// match, then routed to the backend associated with the matching IngressRuleValue.
type IngressRule struct {
	// host is the fully qualified domain name of a network host, as defined by RFC 3986.
	// Note the following deviations from the "host" part of the
	// URI as defined in RFC 3986:
	// 1. IPs are not allowed. Currently an IngressRuleValue can only apply to
	//    the IP in the Spec of the parent Ingress.
	// 2. The `:` delimiter is not respected because ports are not allowed.
	//	  Currently the port of an Ingress is implicitly :80 for http and
	//	  :443 for https.
	// Both these may change in the future.
	// Incoming requests are matched against the host before the
	// IngressRuleValue. If the host is unspecified, the Ingress routes all
	// traffic based on the specified IngressRuleValue.
	//
	// host can be "precise" which is a domain name without the terminating dot of
	// a network host (e.g. "foo.bar.com") or "wildcard", which is a domain name
	// prefixed with a single wildcard label (e.g. "*.foo.com").
	// The wildcard character '*' must appear by itself as the first DNS label and
	// matches only a single label. You cannot have a wildcard label by itself (e.g. Host == "*").
	// Requests will be matched against the Host field in the following way:
	// 1. If host is precise, the request matches this rule if the http host header is equal to Host.
	// 2. If host is a wildcard, then the request matches this rule if the http host header
	// is to equal to the suffix (removing the first label) of the wildcard rule.

	Host string
	// IngressRuleValue represents a rule to route requests for this IngressRule.
	// If unspecified, the rule defaults to a http catch-all. Whether that sends
	// just traffic matching the host to the default backend or all traffic to the
	// default backend, is left to the controller fulfilling the Ingress. Http is
	// currently the only supported IngressRuleValue.

	IngressRuleValue
}

// IngressRuleValue represents a rule to apply against incoming requests. If the
// rule is satisfied, the request is routed to the specified backend. Currently
// mixing different types of rules in a single Ingress is disallowed, so exactly
// one of the following must be set.
type IngressRuleValue struct {
	HTTP *HTTPIngressRuleValue
}

// HTTPIngressRuleValue is a list of http selectors pointing to backends.
// In the example: http://<host>/<path>?<searchpart> -> backend where
// where parts of the url correspond to RFC 3986, this resource will be used
// to match against everything after the last '/' and before the first '?'
// or '#'.
type HTTPIngressRuleValue struct {
	// paths is a collection of paths that map requests to backends.
	// +listType=atomic
	Paths []HTTPIngressPath
}

// PathType represents the type of path referred to by a HTTPIngressPath.
// +enum
type PathType string

const (
	// PathTypeExact matches the URL path exactly and with case sensitivity.
	PathTypeExact = PathType("Exact")

	// PathTypePrefix matches based on a URL path prefix split by '/'. Matching
	// is case sensitive and done on a path element by element basis. A path
	// element refers to the list of labels in the path split by the '/'
	// separator. A request is a match for path p if every p is an element-wise
	// prefix of p of the request path. Note that if the last element of the
	// path is a substring of the last element in request path, it is not a
	// match (e.g. /foo/bar matches /foo/bar/baz, but does not match
	// /foo/barbaz). If multiple matching paths exist in an Ingress spec, the
	// longest matching path is given priority.
	// Examples:
	// - /foo/bar does not match requests to /foo/barbaz
	// - /foo/bar matches request to /foo/bar and /foo/bar/baz
	// - /foo and /foo/ both match requests to /foo and /foo/. If both paths are
	//   present in an Ingress spec, the longest matching path (/foo/) is given
	//   priority.
	PathTypePrefix = PathType("Prefix")

	// PathTypeImplementationSpecific matching is up to the IngressClass.
	// Implementations can treat this as a separate PathType or treat it
	// identically to Prefix or Exact path types.
	PathTypeImplementationSpecific = PathType("ImplementationSpecific")
)

// HTTPIngressPath associates a path with a backend. Incoming urls matching the
// path are forwarded to the backend.
type HTTPIngressPath struct {
	// path is matched against the path of an incoming request. Currently it can
	// contain characters disallowed from the conventional "path" part of a URL
	// as defined by RFC 3986. Paths must begin with a '/' and must be present
	// when using PathType with value "Exact" or "Prefix".

	Path string

	// pathType determines the interpretation of the path matching. PathType can
	// be one of the following values:
	// * Exact: Matches the URL path exactly.
	// * Prefix: Matches based on a URL path prefix split by '/'. Matching is
	//   done on a path element by element basis. A path element refers is the
	//   list of labels in the path split by the '/' separator. A request is a
	//   match for path p if every p is an element-wise prefix of p of the
	//   request path. Note that if the last element of the path is a substring
	//   of the last element in request path, it is not a match (e.g. /foo/bar
	//   matches /foo/bar/baz, but does not match /foo/barbaz).
	// * ImplementationSpecific: Interpretation of the Path matching is up to
	//   the IngressClass. Implementations can treat this as a separate PathType
	//   or treat it identically to Prefix or Exact path types.
	// Implementations are required to support all path types.
	PathType *PathType

	// backend defines the referenced service endpoint to which the traffic
	// will be forwarded to.
	Backend IngressBackend
}

// IngressBackend describes all endpoints for a given service and port.
type IngressBackend struct {
	// service references a service as a backend.
	// This is a mutually exclusive setting with "Resource".

	Service *IngressServiceBackend

	// resource is an ObjectRef to another Kubernetes resource in the namespace
	// of the Ingress object. If resource is specified, a service.Name and
	// service.Port must not be specified.
	// This is a mutually exclusive setting with "Service".

	Resource *v1.TypedLocalObjectReference
}

// IngressServiceBackend references a Kubernetes Service as a Backend.
type IngressServiceBackend struct {
	// name is the referenced service. The service must exist in
	// the same namespace as the Ingress object.
	Name string

	// port of the referenced service. A port name or port number
	// is required for a IngressServiceBackend.
	Port ServiceBackendPort
}

// ServiceBackendPort is the service port being referenced.
type ServiceBackendPort struct {
	// name is the name of the port on the Service.
	// This is a mutually exclusive setting with "Number".

	Name string

	// number is the numerical port number (e.g. 80) on the Service.
	// This is a mutually exclusive setting with "Name".

	Number int32
}

// IngressClass represents the class of the Ingress, referenced by the Ingress
// Spec. The `ingressclass.kubernetes.io/is-default-class` annotation can be
// used to indicate that an IngressClass should be considered default. When a
// single IngressClass resource has this annotation set to true, new Ingress
// resources without a class specified will be assigned this default class.
type IngressClass struct {
	metav1.TypeMeta

	// Standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata

	metav1.ObjectMeta

	// spec is the desired state of the IngressClass.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status

	Spec IngressClassSpec
}

// IngressClassSpec provides information about the class of an Ingress.
type IngressClassSpec struct {
	// controller refers to the name of the controller that should handle this
	// class. This allows for different "flavors" that are controlled by the
	// same controller. For example, you may have different parameters for the
	// same implementing controller. This should be specified as a
	// domain-prefixed path no more than 250 characters in length, e.g.
	// "acme.io/ingress-controller". This field is immutable.
	Controller string

	// parameters is a link to a custom resource containing additional
	// configuration for the controller. This is optional if the controller does
	// not require extra parameters.

	Parameters *IngressClassParametersReference
}

const (
	// IngressClassParametersReferenceScopeNamespace indicates that the
	// referenced Parameters resource is namespace-scoped.
	IngressClassParametersReferenceScopeNamespace = "Namespace"
	// IngressClassParametersReferenceScopeCluster indicates that the
	// referenced Parameters resource is cluster-scoped.
	IngressClassParametersReferenceScopeCluster = "Cluster"
)

// IngressClassParametersReference identifies an API object. This can be used
// to specify a cluster or namespace-scoped resource.
type IngressClassParametersReference struct {
	// apiGroup is the group for the resource being referenced. If APIGroup is
	// not specified, the specified Kind must be in the core API group. For any
	// other third-party types, APIGroup is required.

	APIGroup *string

	// kind is the type of resource being referenced.
	Kind string

	// name is the name of resource being referenced.
	Name string

	// scope represents if this refers to a cluster or namespace scoped resource.
	// This may be set to "Cluster" (default) or "Namespace".

	Scope *string

	// namespace is the namespace of the resource being referenced. This field is
	// required when scope is set to "Namespace" and must be unset when scope is set to
	// "Cluster".

	Namespace *string
}

// IngressClassList is a collection of IngressClasses.
type IngressClassList struct {
	metav1.TypeMeta

	// Standard list metadata.

	metav1.ListMeta

	// items is the list of IngressClasses.
	Items []IngressClass
}
