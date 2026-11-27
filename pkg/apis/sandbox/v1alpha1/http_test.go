package v1alpha1

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

const exampleDir = "../../../../artifacts/sandboxpolicy/"

func loadPolicy(t *testing.T, name string) (SandboxPolicy, map[string]any) {
	t.Helper()
	data, err := os.ReadFile(exampleDir + name)
	if err != nil {
		t.Fatal(err)
	}
	var policy SandboxPolicy
	if err = yaml.UnmarshalStrict(data, &policy); err != nil {
		t.Fatal(err)
	}
	var authored map[string]any
	if err = yaml.Unmarshal(data, &authored); err != nil {
		t.Fatal(err)
	}
	return policy, authored
}

func TestHTTPSectionRoundTrip(t *testing.T) {
	policy, authored := loadPolicy(t, "sandboxpolicy-http.example.yaml")
	if err := ValidateSandboxPolicySpec(policy.Spec); err != nil {
		t.Fatal(err)
	}
	h := policy.Spec.HTTP
	if h == nil || len(h.Rules) != 3 {
		t.Fatalf("http section not decoded: %+v", h)
	}
	if got := h.Rules[0].Except[0].Target.LLM.Model; !reflect.DeepEqual(got, []string{"claude-*"}) {
		t.Fatalf("exception model = %v", got)
	}
	if h.Rules[2].Origin == nil || *h.Rules[2].Origin != "http/X1" {
		t.Fatalf("origin = %v", h.Rules[2].Origin)
	}
	if h.Default != (HTTPDefault{LLM: HTTPDefaultDeny, MCP: HTTPDefaultDeny, Other: HTTPDefaultAudit}) || h.OnUnavailable != HTTPUnavailableAllow {
		t.Fatalf("defaults = %+v, onUnavailable = %q", h.Default, h.OnUnavailable)
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err = json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	// A typed round trip drops an empty list; the API server keeps it. Every
	// other authored field must come back exactly as written.
	want := authored["spec"].(map[string]any)["http"].(map[string]any)
	decrypt := want["decrypt"].(map[string]any)
	if list, ok := decrypt["passthrough"].([]any); !ok || len(list) != 0 {
		t.Fatalf("example must author passthrough: [], got %v", decrypt["passthrough"])
	}
	delete(decrypt, "passthrough")
	if got := after["spec"].(map[string]any)["http"]; !reflect.DeepEqual(want, got) {
		t.Fatalf("http section changed in a round trip:\nwant %v\ngot  %v", want, got)
	}
}

func validHTTP() *HTTPSection {
	return &HTTPSection{
		Default: HTTPDefault{LLM: HTTPDefaultDeny, MCP: HTTPDefaultAllow, Other: HTTPDefaultAudit},
		Rules: []HTTPRule{
			{Name: "confine", Action: ActionDeny, Target: HTTPTarget{DestClass: []string{"modelProvider"}},
				Except: []HTTPException{{Target: &HTTPTarget{LLM: &HTTPLLMTarget{Provider: []string{"anthropic"}}}}}},
			{Name: "names", Action: ActionAllow, Mode: RuleAudit, Target: HTTPTarget{Host: []string{"api.anthropic.com"}}},
		},
	}
}

func TestHTTPValidation(t *testing.T) {
	if err := ValidatePolicySpec(PolicySpec{HTTP: validHTTP()}); err != nil {
		t.Fatalf("valid section rejected: %v", err)
	}
	for _, posture := range []HTTPUnavailable{HTTPUnavailableAllow, HTTPUnavailableDeny} {
		s := validHTTP()
		s.OnUnavailable = posture
		if err := ValidatePolicySpec(PolicySpec{HTTP: s}); err != nil {
			t.Fatalf("onUnavailable %q rejected: %v", posture, err)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*HTTPSection)
		want   string
	}{
		{"except on allow", func(s *HTTPSection) {
			s.Rules[1].Except = []HTTPException{{Target: &HTTPTarget{Host: []string{"x"}}}}
		}, "except is only valid on deny rules"},
		{"empty except on allow", func(s *HTTPSection) { s.Rules[1].Except = []HTTPException{} }, "except is only valid on deny rules"},
		{"onUnavailable", func(s *HTTPSection) { s.OnUnavailable = "maybe" }, "onUnavailable must be allow or deny"},
		{"default llm", func(s *HTTPSection) { s.Default.LLM = "block" }, "default.llm must be allow, deny or audit"},
		{"default mcp empty", func(s *HTTPSection) { s.Default.MCP = "" }, "default.mcp must be allow, deny or audit"},
		{"default other empty", func(s *HTTPSection) { s.Default.Other = "" }, "default.other must be allow, deny or audit"},
		{"duplicate name", func(s *HTTPSection) { s.Rules[1].Name = "confine" }, "must be unique in the http section"},
		{"empty name", func(s *HTTPSection) { s.Rules[1].Name = "" }, "rule name must not be empty"},
		{"long name", func(s *HTTPSection) { s.Rules[1].Name = strings.Repeat("n", MaxRuleNameLength+1) }, "at most 256"},
		{"action", func(s *HTTPSection) { s.Rules[1].Action = "audit" }, "action must be allow or deny"},
		{"rule mode", func(s *HTTPSection) { s.Rules[1].Mode = "off" }, "mode must be audit or enforce"},
		{"rule without target predicate", func(s *HTTPSection) { s.Rules[1].Target = HTTPTarget{} }, "rule target requires at least one predicate"},
		{"rule with empty llm target", func(s *HTTPSection) { s.Rules[1].Target = HTTPTarget{LLM: &HTTPLLMTarget{}} }, "rule target requires at least one predicate"},
		{"empty exact host", func(s *HTTPSection) { s.Decrypt = &HTTPDecrypt{ExactHosts: []string{"api.anthropic.com", ""}} }, "decrypt.exactHosts entries must not be empty"},
		{"empty host pattern", func(s *HTTPSection) { s.Decrypt = &HTTPDecrypt{HostPatterns: []string{""}} }, "decrypt.hostPatterns entries must not be empty"},
		{"empty passthrough host", func(s *HTTPSection) { s.Decrypt = &HTTPDecrypt{Passthrough: []string{""}} }, "decrypt.passthrough entries must not be empty"},
		{"empty target host", func(s *HTTPSection) { s.Rules[1].Target.Host = []string{"api.anthropic.com", ""} }, "target host entries must not be empty"},
		{"empty llm model", func(s *HTTPSection) { s.Rules[0].Except[0].Target.LLM.Model = []string{""} }, "llm model entries must not be empty"},
		{"exception without target", func(s *HTTPSection) { s.Rules[0].Except = []HTTPException{{}} }, "exception requires at least one target predicate"},
		{"exception with empty target", func(s *HTTPSection) { s.Rules[0].Except = []HTTPException{{Target: &HTTPTarget{}}} }, "exception requires at least one target predicate"},
		{"exception with empty llm", func(s *HTTPSection) {
			s.Rules[0].Except = []HTTPException{{Target: &HTTPTarget{LLM: &HTTPLLMTarget{}}}}
		}, "exception requires at least one target predicate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := validHTTP()
			tc.mutate(s)
			err := ValidatePolicySpec(PolicySpec{HTTP: s})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// A rule name is unique across every section of a policy, the http section
// included. The compiled network half of a policy rule is named
// approvedModelProviders/confine, so the http half starts with "http-".
func TestHTTPRuleNameMustBeUniqueAcrossPolicy(t *testing.T) {
	network := func(name string) *NetworkSection {
		return &NetworkSection{Default: NetworkDefault{Egress: ActionDeny, Listen: ActionAllow},
			Rules: []NetworkRule{{Rule: Rule{Name: name, Action: ActionDeny}, Target: NetworkTarget{DestClass: []string{"modelProvider"}}}}}
	}
	httpWith := func(name string) *HTTPSection {
		s := validHTTP()
		s.Rules[0].Name = name
		return s
	}
	err := ValidatePolicySpec(PolicySpec{Network: network("approvedModelProviders/confine"), HTTP: httpWith("approvedModelProviders/confine")})
	if err == nil || !strings.Contains(err.Error(), "must be unique across the policy") {
		t.Fatalf("same name in network and http accepted or wrong error: %v", err)
	}
	// Also across the other kernel sections.
	exec := &ExecSection{Default: ActionDeny, Rules: []ExecRule{{Rule: Rule{Name: "shared", Action: ActionAllow}}}}
	if err = ValidatePolicySpec(PolicySpec{Exec: exec, HTTP: httpWith("shared")}); err == nil || !strings.Contains(err.Error(), "must be unique across the policy") {
		t.Fatalf("same name in exec and http accepted or wrong error: %v", err)
	}
	if err = ValidatePolicySpec(PolicySpec{Network: network("approvedModelProviders/confine"), HTTP: httpWith("approvedModelProviders/http-confine")}); err != nil {
		t.Fatalf("http- name rejected: %v", err)
	}
}

func TestPolicyWithoutHTTPUnchanged(t *testing.T) {
	policy, _ := loadPolicy(t, "sandboxpolicy.example.yaml")
	if policy.Spec.HTTP != nil {
		t.Fatal("example without http decoded an http section")
	}
	raw, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	var after map[string]any
	if err = json.Unmarshal(raw, &after); err != nil {
		t.Fatal(err)
	}
	if _, ok := after["spec"].(map[string]any)["http"]; ok {
		t.Fatal("marshal added an http key")
	}
}

func TestHTTPDeepCopy(t *testing.T) {
	policy, _ := loadPolicy(t, "sandboxpolicy-http.example.yaml")
	original, err := json.Marshal(policy.Spec.HTTP)
	if err != nil {
		t.Fatal(err)
	}
	c := policy.DeepCopy().Spec.HTTP
	c.Decrypt.ExactHosts[0] = "changed"
	c.Decrypt.HostPatterns[0] = "changed"
	c.Rules[0].Target.DestClass[0] = "changed"
	c.Rules[0].Except[0].Target.LLM.Provider[0] = "changed"
	c.Rules[0].Except[0].Target.LLM.Region[0] = "changed"
	c.Rules[0].Except[0].Target.LLM.Model[0] = "changed"
	c.Rules[2].Target.Host[0] = "changed"
	*c.Rules[2].Origin = "changed"
	after, err := json.Marshal(policy.Spec.HTTP)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(after) {
		t.Fatal("deep copy shares memory with the original")
	}
}
