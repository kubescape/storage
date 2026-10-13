package crd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	sandbox "github.com/kubescape/storage/pkg/apis/sandbox/v1alpha1"
	apiextensions "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"sigs.k8s.io/yaml"
)

func TestManifestConsistency(t *testing.T) {
	for _, definition := range Definitions() {
		path := filepath.Join("../../../../artifacts/sandboxpolicy", strings.ToLower(definition.Spec.Names.Kind)+".crd.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var actual apiextensions.CustomResourceDefinition
		if err = yaml.UnmarshalStrict(data, &actual); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(definition.Spec, actual.Spec) {
			t.Fatalf("%s stale; run hack/update-sandbox-codegen.sh", path)
		}
		if definition.Name != actual.Name || definition.TypeMeta != actual.TypeMeta {
			t.Fatalf("%s identity mismatch", path)
		}
	}
}
func TestTypedFieldsCoveredBySchema(t *testing.T) {
	definitions := Definitions()
	for i, typ := range []reflect.Type{reflect.TypeOf(sandbox.SandboxPolicy{}), reflect.TypeOf(sandbox.ClusterSandboxPolicy{}), reflect.TypeOf(sandbox.SandboxPolicyClasses{}), reflect.TypeOf(sandbox.SandboxPolicyNodeStatus{})} {
		t.Run(typ.Name(), func(t *testing.T) { assertCoverage(t, typ, *definitions[i].Spec.Versions[0].Schema.OpenAPIV3Schema) })
	}
}
func assertCoverage(t *testing.T, typ reflect.Type, s schema) {
	t.Helper()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(sandbox.FileDefault{}) {
		if s.XPreserveUnknownFields == nil || !*s.XPreserveUnknownFields {
			t.Fatal("file default must preserve its union")
		}
		return
	}
	if typ.Kind() == reflect.Slice {
		assertCoverage(t, typ.Elem(), *s.Items.Schema)
		return
	}
	if typ.Kind() == reflect.Map {
		assertCoverage(t, typ.Elem(), *s.AdditionalProperties.Schema)
		return
	}
	if typ.Kind() != reflect.Struct {
		return
	}
	// Kubernetes built-ins and IntOrString have their own wire representations.
	if !strings.Contains(typ.PkgPath(), "pkg/apis/sandbox/v1alpha1") {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if field.Anonymous && tag == "" {
			assertCoverage(t, field.Type, s)
			continue
		}
		prop, ok := s.Properties[tag]
		if !ok {
			t.Fatalf("schema lost %s.%s (%s)", typ.Name(), field.Name, tag)
		}
		assertCoverage(t, field.Type, prop)
	}
}
func TestExamplesRoundTrip(t *testing.T) {
	for _, example := range []struct {
		name   string
		object any
	}{
		{"sandboxpolicy.example.yaml", &sandbox.SandboxPolicy{}},
		{"clustersandboxpolicy.example.yaml", &sandbox.ClusterSandboxPolicy{}},
		{"sandboxpolicyclasses.example.yaml", &sandbox.SandboxPolicyClasses{}},
		{"sandboxpolicynodestatus.example.yaml", &sandbox.SandboxPolicyNodeStatus{}},
	} {
		t.Run(example.name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("../../../../artifacts/sandboxpolicy", example.name))
			if err != nil {
				t.Fatal(err)
			}
			if err = yaml.UnmarshalStrict(data, example.object); err != nil {
				t.Fatal(err)
			}
			switch p := example.object.(type) {
			case *sandbox.SandboxPolicy:
				err = sandbox.ValidateSandboxPolicySpec(p.Spec)
			case *sandbox.ClusterSandboxPolicy:
				err = sandbox.ValidateClusterSandboxPolicySpec(p.Spec)
			case *sandbox.SandboxPolicyClasses:
				err = sandbox.ValidatePolicyClasses(p.Spec)
			case *sandbox.SandboxPolicyNodeStatus:
				if p.Name != sandbox.NodeStatusName(p.Spec.PolicyRef.UID, p.Spec.NodeRef.UID) {
					t.Fatal("example uses noncanonical report name")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(example.object)
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			jsonData, err := yaml.YAMLToJSON(data)
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(jsonData, &before); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(raw, &after); err != nil {
				t.Fatal(err)
			}
			// Typed objects add standard empty metadata/status. Every authored field
			// must survive unchanged even when such zero-value fields are present.
			assertPreserved(t, before, after)
		})
	}
}
func assertPreserved(t *testing.T, before, after any) {
	t.Helper()
	switch b := before.(type) {
	case map[string]any:
		a, ok := after.(map[string]any)
		if !ok {
			t.Fatalf("lost object: %v", b)
		}
		for key, value := range b {
			actual, ok := a[key]
			if !ok {
				t.Fatalf("lost field %s", key)
			}
			assertPreserved(t, value, actual)
		}
	case []any:
		a, ok := after.([]any)
		if !ok || len(a) != len(b) {
			t.Fatalf("lost list: %v", b)
		}
		for i := range b {
			assertPreserved(t, b[i], a[i])
		}
	default:
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("changed %v into %v", before, after)
		}
	}
}

func TestSelectorApplySemantics(t *testing.T) {
	for _, definition := range Definitions()[:2] {
		selectors := definition.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"].Properties["selector"].Properties
		for name, selector := range selectors {
			if selector.XMapType == nil || *selector.XMapType != "atomic" {
				t.Fatalf("%s.%s must preserve metav1.LabelSelector's atomic apply semantics", definition.Spec.Names.Kind, name)
			}
		}
	}
}
