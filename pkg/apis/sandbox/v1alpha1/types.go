package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Action is a verdict. Matching denies win over allows; then section defaults apply.
type Action string

const (
	ActionAllow Action = "allow"
	ActionDeny  Action = "deny"
)

// PolicyMode controls a policy. Off loads nothing; rules cannot override off.
type PolicyMode string

const (
	ModeAudit   PolicyMode = "audit"
	ModeEnforce PolicyMode = "enforce"
	ModeOff     PolicyMode = "off"
)

// RuleMode optionally overrides an active policy's mode. Tripwire is a status
// tier, not an authored mode. An omitted mode inherits the policy mode.
type RuleMode string

const (
	RuleAudit   RuleMode = "audit"
	RuleEnforce RuleMode = "enforce"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// SandboxPolicy selects pods only in its own namespace.
type SandboxPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SandboxPolicySpec   `json:"spec"`
	Status            SandboxPolicyStatus `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SandboxPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxPolicy `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// ClusterSandboxPolicy is restricted to cluster administrators by deployment RBAC.
type ClusterSandboxPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterSandboxPolicySpec `json:"spec"`
	Status            SandboxPolicyStatus      `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type ClusterSandboxPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterSandboxPolicy `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// SandboxPolicyClasses provides cluster-scoped named path and destination sets.
type SandboxPolicyClasses struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              PolicyClasses `json:"spec"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SandboxPolicyClassesList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxPolicyClasses `json:"items"`
}

type SandboxPolicySpec struct {
	Selector   PodPolicySelector `json:"selector"`
	PolicySpec `json:",inline"`
}
type ClusterSandboxPolicySpec struct {
	Selector   ClusterPolicySelector `json:"selector"`
	PolicySpec `json:",inline"`
}
type PodPolicySelector struct {
	PodSelector metav1.LabelSelector `json:"podSelector"`
}
type ClusterPolicySelector struct {
	PodSelector metav1.LabelSelector `json:"podSelector"`
	// An omitted namespaceSelector matches all namespaces for a cluster policy.
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}

// PolicySpec is shared content without selectors, so the namespaced API cannot
// accidentally acquire cross-namespace selectors through a shared spec type.
type PolicySpec struct {
	// Names of regular, init or ephemeral containers. "*" matches regular containers only.
	Containers []string          `json:"containers"`
	Mode       PolicyMode        `json:"mode"`
	RootRole   string            `json:"rootRole,omitempty"`
	Sources    []PolicySource    `json:"sources,omitempty"`
	Classes    *PolicyClasses    `json:"classes,omitempty"`
	Exec       *ExecSection      `json:"exec,omitempty"`
	File       *FileSection      `json:"file,omitempty"`
	Network    *NetworkSection   `json:"network,omitempty"`
	Privilege  *PrivilegeSection `json:"privilege,omitempty"`
	Identity   *IdentitySection  `json:"identity,omitempty"`
}
type PolicySource struct {
	// ContainerProfile is a read-only import in the selected pod's namespace.
	ContainerProfile ContainerProfileReference `json:"containerProfile"`
	Include          []string                  `json:"include"`
}
type ContainerProfileReference struct {
	Name string `json:"name"`
}
type PolicyClasses struct {
	PathClasses map[string][]string `json:"pathClasses,omitempty"`
	// Destination entries are CIDRs or DNS names. Empty classes are unresolved, not match-none.
	DestClasses map[string][]string `json:"destClasses,omitempty"`
}

// Actor predicates are ANDed; entries within each list are ORed. Pointer bools
// distinguish absence (unconstrained) from an explicit false predicate.
type Actor struct {
	Path      []string  `json:"path,omitempty"`
	FromImage *bool     `json:"fromImage,omitempty"`
	Ancestor  *Ancestor `json:"ancestor,omitempty"`
	Role      []string  `json:"role,omitempty"`
	UID       []int64   `json:"uid,omitempty"`
	Container []string  `json:"container,omitempty"`
}
type Ancestor struct {
	Path  []string `json:"path"`
	Depth int32    `json:"depth"`
}
type Rule struct {
	// Name is unique across every section in a policy.
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Actor       *Actor   `json:"actor,omitempty"`
	Action      Action   `json:"action"`
	Mode        RuleMode `json:"mode,omitempty"`
	Origin      string   `json:"origin,omitempty"`
}
type PathTarget struct {
	Path      []string `json:"path,omitempty"`
	PathClass []string `json:"pathClass,omitempty"`
	FromImage *bool    `json:"fromImage,omitempty"`
	FSType    []string `json:"fsType,omitempty"`
}
type ExecRule struct {
	Rule   `json:",inline"`
	Target ExecTarget `json:"target"`
}
type FileRule struct {
	Rule   `json:",inline"`
	Target FileTarget `json:"target"`
}
type NetworkRule struct {
	Rule   `json:",inline"`
	Target NetworkTarget `json:"target"`
}
type PrivilegeRule struct {
	Rule   `json:",inline"`
	Target PrivilegeTarget `json:"target"`
}
type IdentityRule struct {
	Rule   `json:",inline"`
	Target IdentityTarget `json:"target"`
}
type ExecTarget struct {
	PathTarget `json:",inline"`
	Argv       *ArgvPredicate `json:"argv,omitempty"`
	// SetRole tags an allowed exec and its descendants. It is not a match predicate.
	SetRole string `json:"setRole,omitempty"`
}
type ArgvPredicate struct {
	Contains    []string `json:"contains,omitempty"`
	Prefix      []string `json:"prefix,omitempty"`
	StdinIsPipe *bool    `json:"stdinIsPipe,omitempty"`
}
type FileTarget struct {
	PathTarget `json:",inline"`
	Access     []string `json:"access,omitempty"`
}
type NetworkTarget struct {
	CIDR []string `json:"cidr,omitempty"`
	// Ports accepts integer ports or inclusive "a-b" ranges, not named ports.
	Ports     []intstr.IntOrString `json:"ports,omitempty"`
	Proto     []string             `json:"proto,omitempty"`
	DNSName   []string             `json:"dnsName,omitempty"`
	DestClass []string             `json:"destClass,omitempty"`
	Direction string               `json:"direction,omitempty"`
}
type PrivilegeTarget struct {
	PathTarget  `json:",inline"`
	Syscall     []string `json:"syscall,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	Capability  []string `json:"capability,omitempty"`
	MountFSType []string `json:"mountFsType,omitempty"`
}
type IdentityTarget struct {
	PathTarget `json:",inline"`
	IMDS       *bool `json:"imds,omitempty"`
	EnvRead    *bool `json:"envRead,omitempty"`
}
type ExecSection struct {
	Default Action     `json:"default"`
	Rules   []ExecRule `json:"rules,omitempty"`
}
type FileSection struct {
	Default FileDefault `json:"default"`
	Rules   []FileRule  `json:"rules,omitempty"`
}
type NetworkSection struct {
	Default NetworkDefault `json:"default"`
	Rules   []NetworkRule  `json:"rules,omitempty"`
}
type PrivilegeSection struct {
	Default Action          `json:"default"`
	Rules   []PrivilegeRule `json:"rules,omitempty"`
}
type IdentitySection struct {
	Default Action         `json:"default"`
	Rules   []IdentityRule `json:"rules,omitempty"`
}

// FileDefault is either an Action scalar or per-access defaults. Exactly one
// representation must be set. The object form must specify every access kind,
// avoiding an undefined fallback for omitted kinds.
type FileDefault struct {
	Action    Action              `json:"-"`
	PerAccess *FileAccessDefaults `json:"-"`
}
type FileAccessDefaults struct {
	Read    Action `json:"read"`
	Write   Action `json:"write"`
	Create  Action `json:"create"`
	Delete  Action `json:"delete"`
	Rename  Action `json:"rename"`
	Chmod   Action `json:"chmod"`
	ExecMap Action `json:"execMap"`
}
type NetworkDefault struct {
	Egress Action `json:"egress"`
	Listen Action `json:"listen"`
}

// SandboxPolicyStatus is written by future consumers through the status subresource.
type SandboxPolicyStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Compiled           *CompiledStatus    `json:"compiled,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	// Nodes is a map-list keyed by name, allowing independent per-node patches.
	Nodes []NodeStatus `json:"nodes,omitempty"`
}
type CompiledStatus struct {
	Rules             int64 `json:"rules"`
	FromSources       int64 `json:"fromSources"`
	ClassesResolved   int64 `json:"classesResolved"`
	ClassesUnresolved int64 `json:"classesUnresolved"`
}
type NodeStatus struct {
	Name               string             `json:"name"`
	Tier               string             `json:"tier"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	Degraded           []DegradedRule     `json:"degraded,omitempty"`
	AppliesOnRestart   []string           `json:"appliesOnRestart,omitempty"`
	ShadowedAllows     []string           `json:"shadowedAllows,omitempty"`
	RuleHitCounts      map[string]int64   `json:"ruleHitCounts,omitempty"`
	Stale              bool               `json:"stale"`
	Error              string             `json:"error,omitempty"`
	LastCompiled       *metav1.Time       `json:"lastCompiled,omitempty"`
}
type DegradedRule struct {
	Rule      string   `json:"rule"`
	Requested RuleMode `json:"requested"`
	Effective string   `json:"effective"`
	Reason    string   `json:"reason"`
}
