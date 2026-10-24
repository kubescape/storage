//go:build crdvalidation

package crd

import (
	"context"
	"encoding/json"
	"fmt"
	sandbox "github.com/kubescape/storage/pkg/apis/sandbox/v1alpha1"
	"math"
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
  nodeSummary: {total: 10000, reporting: 9999, ready: 9990, degraded: 9, stale: 1}
  lastAggregated: "2026-09-24T09:12:03Z"
  conditions:
    - {type: Ready, status: "False", lastTransitionTime: "2026-09-24T09:12:03Z", reason: ReportsMissing, message: "1 node has no current report"}
`
	object := decode(t, data)
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
	for _, definition := range Definitions()[:2] {
		if errs := validate(t, definition, object); len(errs) != 0 {
			t.Fatal(errs.ToAggregate())
		}
		if !reflect.DeepEqual(before, object["status"]) {
			t.Fatal("status fields pruned")
		}
		if _, ok := definition.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].Properties["nodes"]; ok {
			t.Fatal("policy still embeds per-node status")
		}
	}
	invalid := decode(t, data)
	invalid["status"].(map[string]any)["nodeSummary"].(map[string]any)["ready"] = int64(10000)
	if errs := validate(t, Definitions()[0], invalid); len(errs) == 0 {
		t.Fatal("ready nodes exceeded reporting count")
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

func nodeReportExample(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../../../artifacts/sandboxpolicy/sandboxpolicynodestatus.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return decode(t, string(data))
}

func TestKubernetesNodeStatusIdentity(t *testing.T) {
	definition := Definitions()[3]
	if definition.Spec.Scope != extensionsv1.NamespaceScoped || definition.Spec.Versions[0].Subresources.Status == nil {
		t.Fatal("node reports must be namespaced with a status subresource")
	}
	original := nodeReportExample(t)
	_, structural, celValidator := validators(t, definition)
	for _, reference := range []string{"policyRef", "nodeRef"} {
		for _, fieldName := range []string{"name", "uid"} {
			t.Run(reference+"-"+fieldName, func(t *testing.T) {
				changed := runtime.DeepCopyJSONValue(original).(map[string]any)
				changed["spec"].(map[string]any)[reference].(map[string]any)[fieldName] = "changed"
				errs, _ := celValidator.Validate(context.Background(), field.NewPath("object"), structural, changed, original, celconfig.RuntimeCELCostBudget)
				if len(errs) == 0 {
					t.Fatal("mutable report identity")
				}
			})
		}
	}
	changed := runtime.DeepCopyJSONValue(original).(map[string]any)
	changed["status"].(map[string]any)["observedGeneration"] = int64(4)
	errs, _ := celValidator.Validate(context.Background(), field.NewPath("object"), structural, changed, original, celconfig.RuntimeCELCostBudget)
	if len(errs) != 0 {
		t.Fatal("status-only update blocked:", errs.ToAggregate())
	}
	cluster := nodeReportExample(t)
	cluster["spec"].(map[string]any)["policyRef"].(map[string]any)["kind"] = "ClusterSandboxPolicy"
	// This is a distinct report creation, not mutation of a namespaced identity.
	if errs := validate(t, definition, cluster); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	missing := nodeReportExample(t)
	delete(missing["spec"].(map[string]any)["policyRef"].(map[string]any), "uid")
	if errs := validate(t, definition, missing); len(errs) == 0 {
		t.Fatal("missing policy UID accepted")
	}
}

func TestKubernetesNodeStatusBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"too-many-degraded", func(s map[string]any) {
			items := []any{}
			for i := 0; i <= sandbox.MaxStatusRuleDetails; i++ {
				items = append(items, map[string]any{"rule": fmt.Sprintf("rule-%d", i), "requested": "enforce", "effective": "audit", "reason": "no-hook"})
			}
			s["degraded"] = items
			s["counts"].(map[string]any)["degraded"] = int64(sandbox.MaxStatusRuleDetails + 1)
			s["truncated"] = true
		}},
		{"too-many-restart", func(s map[string]any) {
			items := []any{}
			for i := 0; i <= sandbox.MaxStatusRuleDetails; i++ {
				items = append(items, fmt.Sprintf("rule-%d", i))
			}
			s["appliesOnRestart"] = items
			s["counts"].(map[string]any)["appliesOnRestart"] = int64(sandbox.MaxStatusRuleDetails + 1)
			s["truncated"] = true
		}},
		{"too-many-shadowed", func(s map[string]any) {
			items := []any{}
			for i := 0; i <= sandbox.MaxStatusRuleDetails; i++ {
				items = append(items, fmt.Sprintf("rule-%d", i))
			}
			s["shadowedAllows"] = items
			s["counts"].(map[string]any)["shadowedAllows"] = int64(sandbox.MaxStatusRuleDetails + 1)
			s["truncated"] = true
		}},
		{"too-many-counts", func(s map[string]any) {
			items := map[string]any{}
			for i := 0; i <= sandbox.MaxStatusRuleDetails; i++ {
				items[fmt.Sprintf("rule-%d", i)] = int64(1)
			}
			s["ruleHitCounts"] = items
			s["counts"].(map[string]any)["ruleHitCounts"] = int64(sandbox.MaxStatusRuleDetails + 1)
			s["truncated"] = true
		}},
		{"long-error", func(s map[string]any) { s["error"] = strings.Repeat("x", sandbox.MaxStatusErrorLength+1) }},
		{"long-map-key", func(s map[string]any) {
			s["ruleHitCounts"] = map[string]any{strings.Repeat("x", sandbox.MaxRuleNameLength+1): int64(1)}
		}},
		{"missing-truncation", func(s map[string]any) { s["counts"].(map[string]any)["degraded"] = int64(200) }},
		{"undercounted-details", func(s map[string]any) { s["counts"].(map[string]any)["degraded"] = int64(0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			object := nodeReportExample(t)
			tc.mutate(object["status"].(map[string]any))
			if errs := validate(t, Definitions()[3], object); len(errs) == 0 {
				t.Fatal("invalid node status admitted")
			}
		})
	}
	object := nodeReportExample(t)
	object["status"].(map[string]any)["counts"].(map[string]any)["degraded"] = int64(200)
	object["status"].(map[string]any)["truncated"] = true
	if errs := validate(t, Definitions()[3], object); len(errs) != 0 {
		t.Fatal("honest truncation rejected:", errs.ToAggregate())
	}
}

func TestNodeStatusMaximumSnapshotSize(t *testing.T) {
	report := nodeReportExample(t)
	status := report["status"].(map[string]any)
	var degraded, restarts, shadowed []any
	hits := map[string]any{}
	for i := 0; i < sandbox.MaxStatusRuleDetails; i++ {
		// JSON control characters require six-byte escapes, exercising worst-case
		// serialization expansion rather than only ordinary ASCII identifiers.
		name := fmt.Sprintf("%04d", i) + strings.Repeat("\x00", sandbox.MaxRuleNameLength-4)
		degraded = append(degraded, map[string]any{"rule": name, "requested": "enforce", "effective": "audit", "reason": strings.Repeat("\x00", sandbox.MaxStatusDiagnosticLength)})
		restarts = append(restarts, name)
		shadowed = append(shadowed, name)
		hits[name] = int64(math.MaxInt64)
	}
	status["degraded"] = degraded
	status["appliesOnRestart"] = restarts
	status["shadowedAllows"] = shadowed
	status["ruleHitCounts"] = hits
	status["counts"] = map[string]any{"degraded": int64(sandbox.MaxStatusRuleDetails), "appliesOnRestart": int64(sandbox.MaxStatusRuleDetails), "shadowedAllows": int64(sandbox.MaxStatusRuleDetails), "ruleHitCounts": int64(sandbox.MaxStatusRuleDetails)}
	status["error"] = strings.Repeat("\x00", sandbox.MaxStatusErrorLength)
	var conditions []any
	for i := 0; i < sandbox.MaxStatusConditions; i++ {
		conditions = append(conditions, map[string]any{"type": fmt.Sprintf("%04d", i) + strings.Repeat("\x00", sandbox.MaxStatusConditionKeyLength-4), "reason": strings.Repeat("\x00", sandbox.MaxStatusConditionKeyLength), "message": strings.Repeat("\x00", sandbox.MaxStatusDiagnosticLength), "status": "False", "lastTransitionTime": "2026-09-24T09:12:03Z"})
	}
	status["conditions"] = conditions
	if errs := validate(t, Definitions()[3], report); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 768*1024 {
		t.Fatalf("bounded node snapshot too large: %d bytes", len(raw))
	}
	t.Logf("maximum-detail escaped node snapshot: %d bytes", len(raw))
}

func TestNodeDetailSetsRejectDuplicateRuleIDs(t *testing.T) {
	for _, name := range []string{"appliesOnRestart", "shadowedAllows"} {
		t.Run(name, func(t *testing.T) {
			report := nodeReportExample(t)
			status := report["status"].(map[string]any)
			status[name] = []any{"same-rule", "same-rule"}
			status["counts"].(map[string]any)[name] = int64(2)
			if errs := validate(t, Definitions()[3], report); len(errs) == 0 {
				t.Fatal("duplicates inflated rule cardinality")
			}
		})
	}
}

func TestAggregateReadyExcludesStale(t *testing.T) {
	for _, definition := range Definitions()[:2] {
		report := decode(t, `spec: {selector: {podSelector: {}}, containers: ["*"], mode: audit}
status: {nodeSummary: {total: 1, reporting: 1, ready: 1, degraded: 0, stale: 1}}
`)
		if errs := validate(t, definition, report); len(errs) == 0 {
			t.Fatal("same reporting node counted both ready and stale")
		}
		summary := report["status"].(map[string]any)["nodeSummary"].(map[string]any)
		summary["ready"] = int64(0)
		summary["degraded"] = int64(1)
		if errs := validate(t, definition, report); len(errs) != 0 {
			t.Fatal("degraded and stale may overlap:", errs.ToAggregate())
		}
	}
}

func TestKubernetesClassNames(t *testing.T) {
	for _, definition := range Definitions()[:3] {
		for _, name := range []string{"pathClasses", "destClasses"} {
			t.Run(definition.Spec.Names.Kind+"-"+name, func(t *testing.T) {
				var object map[string]any
				if definition.Spec.Names.Kind == "SandboxPolicyClasses" {
					object = decode(t, "spec: {}")
				} else {
					object = decode(t, `spec: {selector: {podSelector: {}}, containers: ["*"], mode: audit, classes: {}}`)
				}
				classes := object["spec"].(map[string]any)
				if definition.Spec.Names.Kind != "SandboxPolicyClasses" {
					classes = classes["classes"].(map[string]any)
				}
				classes[name] = map[string]any{"": []any{}}
				if errs := validate(t, definition, object); len(errs) == 0 {
					t.Fatal("empty class name admitted")
				}
				classes[name] = map[string]any{"named-empty-class": []any{}}
				if errs := validate(t, definition, object); len(errs) != 0 {
					t.Fatal("named empty sets must remain valid", errs.ToAggregate())
				}
			})
		}
	}
}

func TestSelectorSyntaxConsumerBoundary(t *testing.T) {
	object := decode(t, `spec: {selector: {podSelector: {matchLabels: {"bad key": "bad value"}}}, containers: ["*"], mode: audit}`)
	// Shape validation is not native label syntax validation for a CRD field.
	// Pin this documented gap and prove the consumer entrypoint rejects it.
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	raw, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	var policy sandbox.SandboxPolicy
	if err = json.Unmarshal(raw, &policy); err != nil {
		t.Fatal(err)
	}
	if err = sandbox.ValidateSandboxPolicySpec(policy.Spec); err == nil {
		t.Fatal("consumer accepted invalid native selector syntax")
	}
}
