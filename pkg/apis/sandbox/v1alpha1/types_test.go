package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestSchemeAndRoundTrip(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	codec := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	for _, kind := range []string{"SandboxPolicy", "SandboxPolicyList", "ClusterSandboxPolicy", "ClusterSandboxPolicyList", "SandboxPolicyClasses", "SandboxPolicyClassesList"} {
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
func TestFileDefaultRoundTrip(t *testing.T) {
	for _, raw := range []string{`"allow"`, `"deny"`, `{"read":"allow","write":"deny","create":"deny","delete":"deny","rename":"deny","chmod":"deny","execMap":"allow"}`} {
		t.Run(raw, func(t *testing.T) {
			var value FileDefault
			if err := json.Unmarshal([]byte(raw), &value); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			var again FileDefault
			if err = json.Unmarshal(encoded, &again); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(value, again) {
				t.Fatalf("round trip changed default: %s", encoded)
			}
		})
	}
	for _, raw := range []string{`null`, `true`, `1`, `[]`, `"audit"`, `{}`, `{"read":"allow"}`, `{"read":"allow","write":"allow","create":"allow","delete":"allow","rename":"allow","chmod":"allow","execMap":"allow","unknown":"deny"}`} {
		t.Run("invalid-"+raw, func(t *testing.T) {
			original := FileDefault{Action: ActionDeny}
			value := original
			if err := json.Unmarshal([]byte(raw), &value); err == nil {
				t.Fatal("invalid default accepted")
			}
			if !reflect.DeepEqual(value, original) {
				t.Fatal("failed decode mutated last good value")
			}
		})
	}
	if _, err := json.Marshal(FileDefault{Action: ActionAllow, PerAccess: &FileAccessDefaults{}}); err == nil {
		t.Fatal("ambiguous default accepted")
	}
}
func TestExplicitFalseAndDeepCopy(t *testing.T) {
	raw := `{"spec":{"selector":{"podSelector":{}},"containers":["*"],"mode":"audit","classes":{"pathClasses":{"workspace":["/workspace/**"]}},"exec":{"default":"deny","rules":[{"name":"exec","actor":{"fromImage":false,"ancestor":{"path":["/bin/sh"],"depth":2}},"target":{"fromImage":false,"argv":{"stdinIsPipe":false}},"action":"deny"}]},"identity":{"default":"allow","rules":[{"name":"identity","target":{"imds":false,"envRead":false},"action":"deny"}]}},"status":{"nodes":[{"name":"node-1","tier":"audit-only","stale":false,"ruleHitCounts":{"exec":3}}]}}`
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
	copied.Status.Nodes[0].RuleHitCounts["exec"] = 10
	if *policy.Spec.Exec.Rules[0].Actor.FromImage || policy.Spec.Exec.Rules[0].Actor.Ancestor.Path[0] != "/bin/sh" || policy.Spec.Classes.PathClasses["workspace"][0] != "/workspace/**" || policy.Status.Nodes[0].RuleHitCounts["exec"] != 3 {
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
	if err := ValidatePolicySpec(spec); err == nil {
		t.Fatal("malformed CIDR accepted")
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
