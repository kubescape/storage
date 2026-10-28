package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	// NamespaceSelector is required; an explicit empty selector matches all namespaces.
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector"`
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

// MaxAncestorDepth is the supported bounded process-ancestry walk.
const MaxAncestorDepth = 16

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

// PrivilegeTarget selects exactly one hook family. Flags qualify syscall only.
type PrivilegeTarget struct {
	PathClass   []string `json:"pathClass,omitempty"`
	Syscall     []string `json:"syscall,omitempty"`
	Flags       []string `json:"flags,omitempty"`
	Capability  []string `json:"capability,omitempty"`
	MountFSType []string `json:"mountFsType,omitempty"`
}

// IdentityTarget selects path reads, IMDS access, or environment reads.
type IdentityTarget struct {
	Path      []string `json:"path,omitempty"`
	PathClass []string `json:"pathClass,omitempty"`
	IMDS      *bool    `json:"imds,omitempty"`
	EnvRead   *bool    `json:"envRead,omitempty"`
}
type ExecSection struct {
	Default Action     `json:"default"`
	Rules   []ExecRule `json:"rules,omitempty"`
}

// FileSection requires exactly one of Default and DefaultByAccess.
type FileSection struct {
	Default         Action              `json:"default,omitempty"`
	DefaultByAccess *FileAccessDefaults `json:"defaultByAccess,omitempty"`
	Rules           []FileRule          `json:"rules,omitempty"`
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

// FileAccessDefaults is complete: every access kind requires allow or deny.
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

// SandboxPolicyStatus contains only aggregate, constant-size policy status.
// Individual node agents write SandboxPolicyNodeStatus, never a shared nodes list.
type SandboxPolicyStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	NodeSummary        *NodeStatusSummary `json:"nodeSummary,omitempty"`
	LastAggregated     *metav1.Time       `json:"lastAggregated,omitempty"`
}

// NodeStatusSummary counts eligible nodes and current-generation node reports.
// Total includes eligible nodes without reports; Reporting counts valid reports.
// Categories may overlap (for example, a stale node may also be degraded). Missing reports are
// not Ready and must be accounted for by a future aggregation controller.
type NodeStatusSummary struct {
	Total     int64 `json:"total"`
	Reporting int64 `json:"reporting"`
	Ready     int64 `json:"ready"`
	Degraded  int64 `json:"degraded"`
	Stale     int64 `json:"stale"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// SandboxPolicyNodeStatus is one bounded report per policy UID / node UID pair.
// For SandboxPolicy it lives in the policy namespace; for ClusterSandboxPolicy
// it lives in the configured agent namespace. Its spec is immutable.
type SandboxPolicyNodeStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SandboxPolicyNodeStatusSpec `json:"spec"`
	Status            *NodeStatus                 `json:"status,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type SandboxPolicyNodeStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxPolicyNodeStatus `json:"items"`
}
type SandboxPolicyNodeStatusSpec struct {
	PolicyRef SandboxPolicyReference `json:"policyRef"`
	NodeRef   NodeReference          `json:"nodeRef"`
}

// SandboxPolicyReference identifies either policy kind by UID, not just name.
// A SandboxPolicy reference always resolves in the report's own namespace;
// ClusterSandboxPolicy references resolve cluster-wide.
type SandboxPolicyReference struct {
	Kind string    `json:"kind"`
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}
type NodeReference struct {
	Name string    `json:"name"`
	UID  types.UID `json:"uid"`
}
type CompiledStatus struct {
	Rules             int64 `json:"rules"`
	FromSources       int64 `json:"fromSources"`
	ClassesResolved   int64 `json:"classesResolved"`
	ClassesUnresolved int64 `json:"classesUnresolved"`
}

// NodeStatus holds a single node's snapshot. Collections and diagnostics are
// bounded by the CRD schema; Counts records full collection sizes, and Truncated
// indicates omitted entries or shortened diagnostic text. This is not an event log.
type NodeStatus struct {
	Tier               string             `json:"tier"`
	ObservedGeneration int64              `json:"observedGeneration"`
	LastReported       metav1.Time        `json:"lastReported"`
	Compiled           *CompiledStatus    `json:"compiled,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	Degraded           []DegradedRule     `json:"degraded,omitempty"`
	AppliesOnRestart   []string           `json:"appliesOnRestart,omitempty"`
	ShadowedAllows     []string           `json:"shadowedAllows,omitempty"`
	RuleHitCounts      map[string]int64   `json:"ruleHitCounts,omitempty"`
	Counts             NodeStatusCounts   `json:"counts"`
	Truncated          bool               `json:"truncated"`
	Stale              bool               `json:"stale"`
	Error              string             `json:"error,omitempty"`
	LastCompiled       *metav1.Time       `json:"lastCompiled,omitempty"`
}

// NodeStatusCounts preserves cardinality even when bounded details are omitted.
type NodeStatusCounts struct {
	Degraded         int64 `json:"degraded"`
	AppliesOnRestart int64 `json:"appliesOnRestart"`
	ShadowedAllows   int64 `json:"shadowedAllows"`
	RuleHitCounts    int64 `json:"ruleHitCounts"`
}
type DegradedRule struct {
	Rule      string   `json:"rule"`
	Requested RuleMode `json:"requested"`
	Effective string   `json:"effective"`
	Reason    string   `json:"reason"`
}
