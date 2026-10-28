package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestSchemeAndRoundTrip(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	codec := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	for _, kind := range []string{"SandboxPolicy", "SandboxPolicyList", "ClusterSandboxPolicy", "ClusterSandboxPolicyList", "SandboxPolicyClasses", "SandboxPolicyClassesList", "SandboxPolicyNodeStatus", "SandboxPolicyNodeStatusList"} {
		gvk := SchemeGroupVersion.WithKind(kind)
		obj, err := scheme.New(gvk)
		if err != nil {
			t.Fatal(err)
		}
		obj.GetObjectKind().SetGroupVersionKind(gvk)
		raw, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		decoded, actual, err := codec.Decode(raw, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if *actual != gvk || reflect.TypeOf(decoded) != reflect.TypeOf(obj) {
			t.Fatalf("wrong kind: %v %T", actual, decoded)
		}
	}
}
func TestExplicitFalseAndDeepCopy(t *testing.T) {
	raw := `{"spec":{"selector":{"podSelector":{}},"containers":["*"],"mode":"audit","classes":{"pathClasses":{"workspace":["/workspace/**"]}},"exec":{"default":"deny","rules":[{"name":"exec","actor":{"fromImage":false,"ancestor":{"path":["/bin/sh"],"depth":2}},"target":{"fromImage":false,"argv":{"stdinIsPipe":false}},"action":"deny"}]},"identity":{"default":"allow","rules":[{"name":"identity","target":{"imds":false,"envRead":false},"action":"deny"}]}},"status":{"nodeSummary":{"total":1,"reporting":1,"ready":1,"degraded":0,"stale":0}}}`
	var policy SandboxPolicy
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"fromImage":false`, `"stdinIsPipe":false`, `"imds":false`, `"envRead":false`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("lost %s", field)
		}
	}
	copied := policy.DeepCopy()
	*copied.Spec.Exec.Rules[0].Actor.FromImage = true
	copied.Spec.Exec.Rules[0].Actor.Ancestor.Path[0] = "changed"
	copied.Spec.Classes.PathClasses["workspace"][0] = "changed"
	copied.Status.NodeSummary.Ready = 0
	if *policy.Spec.Exec.Rules[0].Actor.FromImage || policy.Spec.Exec.Rules[0].Actor.Ancestor.Path[0] != "/bin/sh" || policy.Spec.Classes.PathClasses["workspace"][0] != "/workspace/**" || policy.Status.NodeSummary.Ready != 1 {
		t.Fatal("deep copy aliased nested fields")
	}
	var absent Actor
	if absent.FromImage != nil {
		t.Fatal("absent predicate was constrained")
	}
}
func TestNamespaceSelectorTypes(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"selector":{"podSelector":{},"namespaceSelector":{}}}`))
	decoder.DisallowUnknownFields()
	var spec SandboxPolicySpec
	if err := decoder.Decode(&spec); err == nil {
		t.Fatal("namespaced type accepted namespaceSelector")
	}
	var cluster ClusterSandboxPolicySpec
	if err := json.Unmarshal([]byte(`{"selector":{"podSelector":{},"namespaceSelector":{}}}`), &cluster); err != nil {
		t.Fatal(err)
	}
	if cluster.Selector.NamespaceSelector == nil {
		t.Fatal("cluster selector lost")
	}
}
func TestPolicySemanticValidation(t *testing.T) {
	spec := PolicySpec{Exec: &ExecSection{Default: ActionDeny, Rules: []ExecRule{{Rule: Rule{Name: "unique", Action: ActionAllow}}}}, Identity: &IdentitySection{Default: ActionAllow, Rules: []IdentityRule{{Rule: Rule{Name: "unique", Action: ActionDeny}}}}}
	if err := ValidatePolicySpec(spec); err == nil {
		t.Fatal("cross-section duplicate accepted")
	}
	spec.Identity.Rules[0].Name = "identity"
	spec.Identity.Rules[0].Target.Path = []string{"/run/token"}
	if err := ValidatePolicySpec(spec); err != nil {
		t.Fatal(err)
	}
	for _, port := range []intstr.IntOrString{intstr.FromInt32(1), intstr.FromInt32(65535), intstr.FromString("80-443")} {
		if err := validatePort(port); err != nil {
			t.Fatal(err)
		}
	}
	for _, port := range []intstr.IntOrString{intstr.FromInt32(0), intstr.FromInt32(65536), intstr.FromString("443-80"), intstr.FromString("0-80"), intstr.FromString("1-65536"), intstr.FromString("https"), intstr.FromString("80"), intstr.FromString("-1-80")} {
		if err := validatePort(port); err == nil {
			t.Fatalf("invalid port accepted: %v", port)
		}
	}
	spec.Network = &NetworkSection{Rules: []NetworkRule{{Rule: Rule{Name: "net"}, Target: NetworkTarget{CIDR: []string{"not-cidr"}}}}}
	for _, cidr := range []string{"not-cidr", "010.0.0.0/8"} {
		spec.Network.Rules[0].Target.CIDR = []string{cidr}
		if err := ValidatePolicySpec(spec); err == nil {
			t.Fatalf("malformed CIDR accepted: %s", cidr)
		}
	}
	spec.Network.Rules[0].Target.CIDR = []string{"10.0.0.0/8", "::1/128"}
	if err := ValidatePolicySpec(spec); err != nil {
		t.Fatal(err)
	}
}
func TestAllResourceDeepCopies(t *testing.T) {
	cluster := &ClusterSandboxPolicy{Spec: ClusterSandboxPolicySpec{Selector: ClusterPolicySelector{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"team": "a"}}}}}
	copy := cluster.DeepCopy()
	copy.Spec.Selector.NamespaceSelector.MatchLabels["team"] = "b"
	if cluster.Spec.Selector.NamespaceSelector.MatchLabels["team"] != "a" {
		t.Fatal("namespace selector alias")
	}
	classes := &SandboxPolicyClasses{Spec: PolicyClasses{DestClasses: map[string][]string{"imds": {"169.254.169.254/32"}}}}
	copyClasses := classes.DeepCopy()
	copyClasses.Spec.DestClasses["imds"][0] = "changed"
	if classes.Spec.DestClasses["imds"][0] != "169.254.169.254/32" {
		t.Fatal("class alias")
	}
}

func TestNodeStatusIdentityAndDeepCopy(t *testing.T) {
	report := &SandboxPolicyNodeStatus{
		Spec: SandboxPolicyNodeStatusSpec{
			PolicyRef: SandboxPolicyReference{Kind: "SandboxPolicy", Name: "policy", UID: "policy-uid"},
			NodeRef:   NodeReference{Name: "node-1", UID: "node-uid"},
		},
		Status: &NodeStatus{Tier: "audit-only", RuleHitCounts: map[string]int64{"rule": 3}, Degraded: []DegradedRule{{Rule: "rule", Reason: "no-hook"}}},
	}
	copied := report.DeepCopy()
	copied.Status.RuleHitCounts["rule"] = 10
	copied.Status.Degraded[0].Reason = "changed"
	if report.Status.RuleHitCounts["rule"] != 3 || report.Status.Degraded[0].Reason != "no-hook" {
		t.Fatal("node report deepcopy alias")
	}
	name := NodeStatusName("policy-uid", "node-uid")
	if name != NodeStatusName("policy-uid", "node-uid") || len(name) > 253 {
		t.Fatal("noncanonical report name")
	}
	for _, pair := range [][2]string{{"new-policy-uid", "node-uid"}, {"policy-uid", "new-node-uid"}} {
		if name == NodeStatusName(types.UID(pair[0]), types.UID(pair[1])) {
			t.Fatal("recreated resource reused report identity")
		}
	}
	if NodeStatusName("a", "bc") == NodeStatusName("ab", "c") {
		t.Fatal("ambiguous concatenation")
	}
}

func TestNativeSelectorAndClassValidation(t *testing.T) {
	for _, selector := range []metav1.LabelSelector{
		{MatchLabels: map[string]string{"bad key": "value"}},
		{MatchLabels: map[string]string{"app": "bad value"}},
		{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "bad/key/format", Operator: metav1.LabelSelectorOpExists}}},
	} {
		if err := ValidateSandboxPolicySpec(SandboxPolicySpec{Selector: PodPolicySelector{PodSelector: selector}}); err == nil {
			t.Fatal("invalid pod selector accepted")
		}
		if err := ValidateClusterSandboxPolicySpec(ClusterSandboxPolicySpec{Selector: ClusterPolicySelector{NamespaceSelector: &selector}}); err == nil {
			t.Fatal("invalid namespace selector accepted")
		}
	}
	valid := metav1.LabelSelector{MatchLabels: map[string]string{"example.com/Agent_1": ""}}
	if err := ValidateSandboxPolicySpec(SandboxPolicySpec{Selector: PodPolicySelector{PodSelector: valid}}); err != nil {
		t.Fatal(err)
	}
	for _, classes := range []PolicyClasses{{PathClasses: map[string][]string{"": {}}}, {DestClasses: map[string][]string{"": {}}}} {
		if err := ValidatePolicyClasses(classes); err == nil {
			t.Fatal("unreferenceable class admitted")
		}
		if err := ValidatePolicySpec(PolicySpec{Classes: &classes}); err == nil {
			t.Fatal("inline empty class admitted")
		}
	}
	if err := ValidatePolicyClasses(PolicyClasses{DestClasses: map[string][]string{"modelProvider": {}}}); err != nil {
		t.Fatal("unresolved named class must remain valid configuration", err)
	}
}
