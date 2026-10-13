//go:build crdvalidation

package crd

import (
	"context"
	"encoding/json"
	sandbox "github.com/kubescape/storage/pkg/apis/sandbox/v1alpha1"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	extensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	definitionvalidation "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/validation"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/listtype"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/pruning"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	kjson "k8s.io/apimachinery/pkg/util/json"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"sigs.k8s.io/yaml"
)

// These tests use Kubernetes' actual definition, structural, OpenAPI and CEL
// validators without a live cluster. The test-only packages are in an existing
// module dependency but are not vendored; run hack/verify-sandbox-crds.sh.
func TestKubernetesDefinitions(t *testing.T) {
	for _, definition := range Definitions() {
		t.Run(definition.Spec.Names.Kind, func(t *testing.T) {
			extensionsv1.SetDefaults_CustomResourceDefinition(&definition)
			definition.Status.StoredVersions = []string{"v1alpha1"}
			var internal apiextensions.CustomResourceDefinition
			if err := extensionsv1.Convert_v1_CustomResourceDefinition_To_apiextensions_CustomResourceDefinition(&definition, &internal, nil); err != nil {
				t.Fatal(err)
			}
			if errs := definitionvalidation.ValidateCustomResourceDefinition(context.Background(), &internal); len(errs) != 0 {
				t.Fatal(errs.ToAggregate())
			}
		})
	}
}
func validators(t *testing.T, definition extensionsv1.CustomResourceDefinition) (validation.SchemaValidator, *structuralschema.Structural, *cel.Validator) {
	t.Helper()
	var internal apiextensions.JSONSchemaProps
	if err := extensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(definition.Spec.Versions[0].Schema.OpenAPIV3Schema, &internal, nil); err != nil {
		t.Fatal(err)
	}
	structural, err := structuralschema.NewStructural(&internal)
	if err != nil {
		t.Fatal(err)
	}
	if errs := structuralschema.ValidateStructural(field.NewPath("schema"), structural); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	openapi, _, err := validation.NewSchemaValidator(&internal)
	if err != nil {
		t.Fatal(err)
	}
	return openapi, structural, cel.NewValidator(structural, true, celconfig.PerCallLimit)
}
func validate(t *testing.T, definition extensionsv1.CustomResourceDefinition, object map[string]any) field.ErrorList {
	t.Helper()
	openapi, structural, celValidator := validators(t, definition)
	// API-server pruning occurs before validation. Negative tests must therefore
	// prove that a forbidden namespaceSelector is rejected, not merely pruned.
	pruning.Prune(object, structural, true)
	errs := validation.ValidateCustomResource(field.NewPath("object"), object, openapi)
	errs = append(errs, listtype.ValidateListSetsAndMaps(field.NewPath("object"), structural, object)...)
	if celValidator != nil {
		celErrs, _ := celValidator.Validate(context.Background(), field.NewPath("object"), structural, object, nil, celconfig.RuntimeCELCostBudget)
		errs = append(errs, celErrs...)
	}
	return errs
}
func decode(t *testing.T, data string) map[string]any {
	t.Helper()
	var obj map[string]any
	raw, err := yaml.YAMLToJSON([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := kjson.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	return obj
}
func TestKubernetesExamples(t *testing.T) {
	for _, definition := range Definitions() {
		name := strings.ToLower(definition.Spec.Names.Kind) + ".example.yaml"
		data, err := os.ReadFile(filepath.Join("../../../../artifacts/sandboxpolicy", name))
		if err != nil {
			t.Fatal(err)
		}
		if errs := validate(t, definition, decode(t, string(data))); len(errs) != 0 {
			t.Fatalf("%s: %v", name, errs.ToAggregate())
		}
	}
}
func TestKubernetesInvalidPolicies(t *testing.T) {
	base := `apiVersion: kubescape.io/v1alpha1
kind: SandboxPolicy
metadata: {name: test, namespace: agents}
spec:
  selector: {podSelector: {}}
  containers: ["*"]
  mode: audit
`
	for _, tc := range []struct{ name, extra string }{
		{"duplicate-section-rules", "  exec: {default: deny, rules: [{name: test, target: {}, action: deny}, {name: test, target: {}, action: allow}]}\n"},
		{"wrong-port-type", "  network: {default: {egress: deny, listen: deny}, rules: [{name: test, target: {ports: [true]}, action: allow}]}\n"},
		{"empty-containers", "  containers: []\n"},
		{"wrong-default-shape", "  network: {default: deny}\n"},
		{"unknown-mode", "  mode: tripwire\n"},
		{"namespace-selector", "  selector: {podSelector: {}, namespaceSelector: {matchLabels: {team: other}}}\n"},
		{"bad-action", "  exec: {default: kill}\n"},
		{"missing-network-default", "  network: {default: {egress: deny}}\n"},
		{"bad-rule-mode", "  exec: {default: deny, rules: [{name: test, target: {}, action: deny, mode: off}]}\n"},
		{"missing-target", "  exec: {default: deny, rules: [{name: test, action: deny}]}\n"},
		{"unknown-syscall", "  privilege: {default: deny, rules: [{name: test, target: {syscall: [made_up]}, action: deny}]}\n"},
		{"negative-uid", "  exec: {default: deny, rules: [{name: test, actor: {uid: [-1]}, target: {}, action: allow}]}\n"},
		{"zero-depth", "  exec: {default: deny, rules: [{name: test, actor: {ancestor: {path: [/bin/sh], depth: 0}}, target: {}, action: allow}]}\n"},
		{"bad-access", "  file: {default: allow, rules: [{name: test, target: {access: [execute]}, action: deny}]}\n"},
		{"bad-selector", "  selector: {podSelector: {matchExpressions: [{key: team, operator: Exists, values: [other]}]}}\n"},
		{"bad-port-zero", "  network: {default: {egress: deny, listen: deny}, rules: [{name: test, target: {ports: [0]}, action: allow}]}\n"},
		{"bad-port-large", "  network: {default: {egress: deny, listen: deny}, rules: [{name: test, target: {ports: [65536]}, action: allow}]}\n"},
		{"named-port", "  network: {default: {egress: deny, listen: deny}, rules: [{name: test, target: {ports: [https]}, action: allow}]}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := validate(t, Definitions()[0], decode(t, base+tc.extra)); len(errs) == 0 {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	cluster := decode(t, strings.Replace(base, "kind: SandboxPolicy", "kind: ClusterSandboxPolicy", 1)+"  selector: {podSelector: {}, namespaceSelector: {matchLabels: {team: other}}}\n")
	if errs := validate(t, Definitions()[1], cluster); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
}
func TestKubernetesFileDefaultPreserved(t *testing.T) {
	// Pin both halves of the intentional schema gap: Kubernetes preserves and
	// admits non-null arbitrary values, but typed consumers reject invalid ones.
	for _, tc := range []struct {
		name, value string
		valid       bool
	}{
		{"allow", "allow", true},
		{"deny", "deny", true},
		{"per-access", "{read: allow, write: deny, create: deny, delete: deny, rename: deny, chmod: deny, execMap: allow}", true},
		{"invalid-scalar", "audit", false},
		{"empty-object", "{}", false},
		{"incomplete-object", "{read: allow}", false},
		{"unknown-object-key", "{read: allow, write: deny, create: deny, delete: deny, rename: deny, chmod: deny, execMap: allow, unknown: deny}", false},
		{"boolean", "true", false},
		{"number", "42", false},
		{"array", "[allow, deny]", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			obj := decode(t, "spec: {selector: {podSelector: {}}, containers: ['*'], mode: audit, file: {default: "+tc.value+"}}")
			before := runtime.DeepCopyJSONValue(obj["spec"].(map[string]any)["file"].(map[string]any)["default"])
			if errs := validate(t, Definitions()[0], obj); len(errs) != 0 {
				t.Fatal(errs.ToAggregate())
			}
			after := obj["spec"].(map[string]any)["file"].(map[string]any)["default"]
			if !reflect.DeepEqual(before, after) {
				t.Fatal("file default pruned")
			}
			raw, err := json.Marshal(after)
			if err != nil {
				t.Fatal(err)
			}
			var typed sandbox.FileDefault
			err = json.Unmarshal(raw, &typed)
			if (err == nil) != tc.valid {
				t.Fatalf("typed consumer validation: valid=%t, error=%v", tc.valid, err)
			}
		})
	}
}

func TestKubernetesStatus(t *testing.T) {
	data := `spec: {selector: {podSelector: {}}, containers: ["*"], mode: audit}
status:
  observedGeneration: 3
  compiled: {rules: 41, fromSources: 17, classesResolved: 6, classesUnresolved: 1}
  conditions:
    - {type: Ready, status: "False", lastTransitionTime: "2026-09-24T09:12:03Z", reason: ClassUnresolved, message: "empty modelProvider class"}
  nodes:
    - name: node-1
      tier: bpf-lsm
      observedGeneration: 3
      conditions:
        - {type: Ready, status: "False", lastTransitionTime: "2026-09-24T09:12:03Z", reason: ClassUnresolved, message: "empty class"}
      degraded: [{rule: no-capabilities, requested: enforce, effective: audit, reason: no-hook}]
      appliesOnRestart: [no-new-namespaces]
      shadowedAllows: [allow-credentials]
      ruleHitCounts: {no-capabilities: 12}
      stale: true
      error: last compile failed
      lastCompiled: "2026-09-24T09:12:03Z"
`
	object := decode(t, data)
	// Round-trip through the typed API as well as server pruning and validation.
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	var policy sandbox.SandboxPolicy
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	reencoded, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var typed map[string]any
	if err = kjson.Unmarshal(reencoded, &typed); err != nil {
		t.Fatal(err)
	}
	assertPreserved(t, object, typed)
	before := runtime.DeepCopyJSONValue(object["status"])
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	if !reflect.DeepEqual(before, object["status"]) {
		t.Fatal("status fields pruned")
	}
	duplicate := decode(t, data)
	status := duplicate["status"].(map[string]any)
	nodes := status["nodes"].([]any)
	status["nodes"] = append(nodes, nodes[0])
	if errs := validate(t, Definitions()[0], duplicate); len(errs) == 0 {
		t.Fatal("duplicate node map-list key accepted")
	}
}

func TestKubernetesEmptyLabelValues(t *testing.T) {
	object := decode(t, `spec:
  selector:
    podSelector:
      matchLabels: {sandbox: ""}
      matchExpressions: [{key: sandbox, operator: In, values: [""]}]
  containers: ["*"]
  mode: audit
`)
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
}
