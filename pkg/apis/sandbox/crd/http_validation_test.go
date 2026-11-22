//go:build crdvalidation

package crd

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

const httpBase = `apiVersion: kubescape.io/v1alpha1
kind: SandboxPolicy
metadata: {name: test, namespace: agents}
spec:
  selector: {podSelector: {}}
  containers: ["*"]
  mode: audit
`

func readExample(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("../../../../artifacts/sandboxpolicy/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The API server prunes before it validates. The example must come out of
// pruning exactly as written, including its empty passthrough list.
func TestKubernetesHTTPExample(t *testing.T) {
	data := readExample(t, "sandboxpolicy-http.example.yaml")
	object := decode(t, data)
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	if !reflect.DeepEqual(object, decode(t, data)) {
		t.Fatal("pruning changed the example")
	}
	passthrough, ok := object["spec"].(map[string]any)["http"].(map[string]any)["decrypt"].(map[string]any)["passthrough"].([]any)
	if !ok || len(passthrough) != 0 {
		t.Fatalf("empty passthrough list not kept: %v", passthrough)
	}
	cluster := strings.Replace(data, "kind: SandboxPolicy", "kind: ClusterSandboxPolicy", 1)
	cluster = strings.Replace(cluster, "    podSelector:\n", "    namespaceSelector: {}\n    podSelector:\n", 1)
	clusterObject := decode(t, cluster)
	if errs := validate(t, Definitions()[1], clusterObject); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	if !reflect.DeepEqual(clusterObject, decode(t, cluster)) {
		t.Fatal("pruning changed the cluster example")
	}
}

func TestKubernetesHTTPInvalid(t *testing.T) {
	const d = "default: {llm: deny, mcp: deny, other: audit}"
	for _, tc := range []struct{ name, http string }{
		{"except-on-allow", "{" + d + ", rules: [{name: a, target: {}, action: allow, except: [{target: {host: [x]}}]}]}"},
		{"empty-except-on-allow", "{" + d + ", rules: [{name: a, target: {}, action: allow, except: []}]}"},
		{"bad-on-unavailable", "{" + d + ", onUnavailable: maybe}"},
		{"bad-default-value", "{default: {llm: block, mcp: deny, other: audit}}"},
		{"default-missing-other", "{default: {llm: deny, mcp: deny}}"},
		{"missing-default", "{onUnavailable: allow}"},
		{"duplicate-rule-name", "{" + d + ", rules: [{name: a, target: {}, action: deny}, {name: a, target: {}, action: allow}]}"},
		{"rule-without-name", "{" + d + ", rules: [{target: {}, action: deny}]}"},
		{"empty-rule-name", "{" + d + ", rules: [{name: \"\", target: {}, action: deny}]}"},
		{"rule-without-target", "{" + d + ", rules: [{name: a, action: deny}]}"},
		{"bad-action", "{" + d + ", rules: [{name: a, target: {}, action: audit}]}"},
		{"bad-rule-mode", "{" + d + ", rules: [{name: a, target: {}, action: deny, mode: \"off\"}]}"},
		{"exception-with-empty-target", "{" + d + ", rules: [{name: a, target: {}, action: deny, except: [{target: {}}]}]}"},
		{"exception-with-empty-llm", "{" + d + ", rules: [{name: a, target: {}, action: deny, except: [{target: {llm: {}}}]}]}"},
		{"empty-exception", "{" + d + ", rules: [{name: a, target: {}, action: deny, except: [{}]}]}"},
		{"unsupported-method", "{" + d + ", rules: [{name: a, target: {host: [api.github.com], method: [DELETE]}, action: deny}]}"},
		{"unsupported-mcp", "{" + d + ", rules: [{name: a, target: {mcp: {server: [x]}}, action: deny}]}"},
		{"unsupported-actor", "{" + d + ", rules: [{name: a, actor: {uid: [0]}, target: {}, action: deny}]}"},
		{"unsupported-exception-actor", "{" + d + ", rules: [{name: a, target: {}, action: deny, except: [{actor: {uid: [0]}, target: {host: [x]}}]}]}"},
		{"unsupported-exception-method", "{" + d + ", rules: [{name: a, target: {}, action: deny, except: [{target: {host: [x], method: [GET]}}]}]}"},
		{"empty-host-entry", "{" + d + ", decrypt: {exactHosts: [\"\"]}}"},
		{"empty-target-list", "{" + d + ", rules: [{name: a, target: {host: []}, action: deny}]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if errs := validate(t, Definitions()[0], decode(t, httpBase+"  http: "+tc.http+"\n")); len(errs) == 0 {
				t.Fatal("invalid http section accepted")
			}
		})
	}
}

// Read-back compares spec hashes, so the server must not add fields.
func TestKubernetesHTTPNoDefaulting(t *testing.T) {
	data := httpBase + "  http: {default: {llm: allow, mcp: allow, other: allow}}\n"
	object := decode(t, data)
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	if !reflect.DeepEqual(object, decode(t, data)) {
		t.Fatalf("validation changed the object: %v", object)
	}
	for _, s := range httpSection().Properties {
		assertNoDefault(t, s)
	}
}
func assertNoDefault(t *testing.T, s schema) {
	t.Helper()
	if s.Default != nil {
		t.Fatalf("http schema sets a default: %s", s.Default.Raw)
	}
	for _, p := range s.Properties {
		assertNoDefault(t, p)
	}
	if s.Items != nil && s.Items.Schema != nil {
		assertNoDefault(t, *s.Items.Schema)
	}
}

func TestKubernetesPolicyWithoutHTTPUnchanged(t *testing.T) {
	data := readExample(t, "sandboxpolicy.example.yaml")
	object := decode(t, data)
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	if !reflect.DeepEqual(object, decode(t, data)) {
		t.Fatal("an object without http changed")
	}
	if _, ok := object["spec"].(map[string]any)["http"]; ok {
		t.Fatal("http key added")
	}
}

func TestKubernetesHTTPRuleNameMayRepeatKernelRuleName(t *testing.T) {
	object := decode(t, httpBase+`  network: {default: {egress: deny, listen: allow}, rules: [{name: approvedModelProviders/confine, target: {destClass: [modelProvider]}, action: deny}]}
  http: {default: {llm: deny, mcp: deny, other: audit}, rules: [{name: approvedModelProviders/confine, target: {destClass: [modelProvider]}, action: deny}]}
`)
	if errs := validate(t, Definitions()[0], object); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
}

func TestKubernetesNodeStatusHTTP(t *testing.T) {
	definition := Definitions()[3]
	data := readExample(t, "sandboxpolicynodestatus.example.yaml")
	if errs := validate(t, definition, decode(t, data)); len(errs) != 0 {
		t.Fatal(errs.ToAggregate())
	}
	withHTTP := func(http any) map[string]any {
		object := decode(t, data)
		status := object["status"].(map[string]any)
		if http == nil {
			delete(status, "http")
		} else {
			status["http"] = http
		}
		return object
	}
	block := func(mutate func(map[string]any)) map[string]any {
		b := map[string]any{"boundPods": int64(1), "preAttachBypass": int64(0), "gate": "redirecting", "nodeControllerDegraded": false}
		mutate(b)
		return b
	}
	if errs := validate(t, definition, withHTTP(nil)); len(errs) != 0 {
		t.Fatalf("report without http rejected: %v", errs.ToAggregate())
	}
	for _, gate := range []string{"redirecting", "failing_open", "failing_closed", "not_bound"} {
		if errs := validate(t, definition, withHTTP(block(func(b map[string]any) { b["gate"] = gate }))); len(errs) != 0 {
			t.Fatalf("gate %s rejected: %v", gate, errs.ToAggregate())
		}
	}
	names := func(n int) []any {
		list := make([]any, n)
		for i := range list {
			list[i] = fmt.Sprintf("pod-%d", i)
		}
		return list
	}
	if errs := validate(t, definition, withHTTP(block(func(b map[string]any) { b["needRestart"] = names(64) }))); len(errs) != 0 {
		t.Fatalf("64 pod names rejected: %v", errs.ToAggregate())
	}
	for name, mutate := range map[string]func(map[string]any){
		"unknown gate":      func(b map[string]any) { b["gate"] = "open" },
		"missing gate":      func(b map[string]any) { delete(b, "gate") },
		"missing boundPods": func(b map[string]any) { delete(b, "boundPods") },
		"missing degraded":  func(b map[string]any) { delete(b, "nodeControllerDegraded") },
		"negative count":    func(b map[string]any) { b["boundPods"] = int64(-1) },
		"negative bypass":   func(b map[string]any) { b["preAttachBypass"] = int64(-1) },
		"too many names":    func(b map[string]any) { b["needRestart"] = names(65) },
		"duplicate name":    func(b map[string]any) { b["needRestart"] = []any{"a", "a"} },
		"long name":         func(b map[string]any) { b["needRestart"] = []any{strings.Repeat("p", 254)} },
		"empty name":        func(b map[string]any) { b["needRestart"] = []any{""} },
	} {
		t.Run(name, func(t *testing.T) {
			if errs := validate(t, definition, withHTTP(block(mutate))); len(errs) == 0 {
				t.Fatal("invalid http status accepted")
			}
		})
	}
}
