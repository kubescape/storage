package v1alpha1

import (
	"encoding/json"
	"fmt"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"sigs.k8s.io/yaml"
)

func TestFileDefaultsTypedListAndZeroMarshal(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	codec := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	defaults := []string{`{"default":"allow"}`, `{"default":"deny"}`, `{"defaultByAccess":{"read":"allow","write":"deny","create":"deny","delete":"deny","rename":"deny","chmod":"deny","execMap":"allow"}}`}
	for _, file := range defaults {
		raw := fmt.Sprintf(`{"apiVersion":"kubescape.io/v1alpha1","kind":"SandboxPolicyList","items":[{"spec":{"file":{"default":"deny"}}},{"spec":{"file":%s}}]}`, file)
		obj, _, err := codec.Decode([]byte(raw), nil, nil)
		if err != nil {
			t.Fatalf("valid list failed: %v", err)
		}
		for _, p := range obj.(*SandboxPolicyList).Items {
			if err := ValidatePolicySpec(p.Spec.PolicySpec); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Semantic invalidity in a correctly typed scalar must not poison list decoding.
	raw := `{"apiVersion":"kubescape.io/v1alpha1","kind":"SandboxPolicyList","items":[{"spec":{"file":{"default":"deny"}}},{"spec":{"file":{"default":"audit"}}}]}`
	obj, _, err := codec.Decode([]byte(raw), nil, nil)
	if err != nil {
		t.Fatalf("semantic validation poisoned typed list: %v", err)
	}
	if err := ValidatePolicySpec(obj.(*SandboxPolicyList).Items[1].Spec.PolicySpec); err == nil {
		t.Fatal("invalid action accepted")
	}
	if _, err := json.Marshal(FileSection{}); err != nil {
		t.Fatalf("zero-value marshal failed: %v", err)
	}
	if err := ValidatePolicySpec(PolicySpec{File: &FileSection{}}); err == nil {
		t.Fatal("missing default accepted")
	}
	for _, file := range []string{`{"default":"allow","defaultByAccess":{"read":"allow","write":"deny","create":"deny","delete":"deny","rename":"deny","chmod":"deny","execMap":"allow"}}`, `{"defaultByAccess":{"read":"allow"}}`, `{"defaultByAccess":{"read":"audit","write":"deny","create":"deny","delete":"deny","rename":"deny","chmod":"deny","execMap":"allow"}}`} {
		var spec PolicySpec
		if err := json.Unmarshal([]byte(`{"file":`+file+`}`), &spec); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePolicySpec(spec); err == nil {
			t.Fatalf("invalid file default accepted: %s", file)
		}
	}
}

func TestClusterNamespaceSelectorMustBeExplicit(t *testing.T) {
	if err := ValidateClusterSandboxPolicySpec(ClusterSandboxPolicySpec{}); err == nil {
		t.Fatal("omitted namespace selector accepted")
	}
	if err := ValidateClusterSandboxPolicySpec(ClusterSandboxPolicySpec{Selector: ClusterPolicySelector{NamespaceSelector: &metav1.LabelSelector{}}}); err != nil {
		t.Fatal(err)
	}
}

func TestHookTargetsAndAncestorBounds(t *testing.T) {
	for _, tc := range []struct {
		section, target string
		valid           bool
	}{
		{"privilege", `{"syscall":["unshare"],"flags":["CLONE_NEWUSER"]}`, true},
		{"privilege", `{"capability":["SYS_ADMIN"]}`, true},
		{"privilege", `{"mountFsType":["tmpfs"]}`, true},
		{"privilege", `{"pathClass":["hostDevices"]}`, true},
		{"privilege", `{}`, false},
		{"privilege", `{"flags":["CLONE_NEWUSER"]}`, false},
		{"privilege", `{"syscall":["init_module"],"capability":["SYS_MODULE"]}`, false},
		{"identity", `{"path":["/run/token"],"pathClass":["credentialStore"]}`, true},
		{"identity", `{"imds":true}`, true},
		{"identity", `{"envRead":false}`, true},
		{"identity", `{}`, false},
		{"identity", `{"imds":true,"envRead":true}`, false},
		{"identity", `{"path":["/run/token"],"imds":true}`, false},
	} {
		var spec PolicySpec
		raw := fmt.Sprintf(`{"%s":{"default":"deny","rules":[{"name":"test","target":%s,"action":"deny"}]}}`, tc.section, tc.target)
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			t.Fatal(err)
		}
		if err := ValidatePolicySpec(spec); (err == nil) != tc.valid {
			t.Fatalf("%s valid=%t err=%v", raw, tc.valid, err)
		}
	}
	for _, depth := range []int32{0, 1, 16, 17} {
		spec := PolicySpec{Exec: &ExecSection{Rules: []ExecRule{{Rule: Rule{Name: "depth", Actor: &Actor{Ancestor: &Ancestor{Path: []string{"/bin/sh"}, Depth: depth}}}}}}}
		if err := ValidatePolicySpec(spec); (err == nil) != (depth >= 1 && depth <= 16) {
			t.Fatalf("depth %d: %v", depth, err)
		}
	}
}

func TestQuotedOffYAMLRoundTrip(t *testing.T) {
	raw, err := yaml.YAMLToJSON([]byte("mode: \"off\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var spec PolicySpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Mode != ModeOff {
		t.Fatalf("got %q", spec.Mode)
	}
	raw, err = yaml.YAMLToJSON([]byte("mode: off\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &spec); err == nil {
		t.Fatal("unquoted YAML boolean unexpectedly parsed as policy mode")
	}
}

func TestFileDefaultsDeepCopy(t *testing.T) {
	policy := &SandboxPolicy{Spec: SandboxPolicySpec{PolicySpec: PolicySpec{File: &FileSection{DefaultByAccess: &FileAccessDefaults{Read: ActionAllow}}}}}
	copy := policy.DeepCopy()
	copy.Spec.File.DefaultByAccess.Read = ActionDeny
	if policy.Spec.File.DefaultByAccess.Read != ActionAllow {
		t.Fatal("deep copy shares access defaults")
	}
}
