package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultMetadataAndExceptionsRoundTrip(t *testing.T) {
	raw := `{"containers":["*"],"mode":"off",
 "exec":{"default":"deny","defaultMode":"enforce","defaultOrigin":"","rules":[{"name":"exec","action":"deny","target":{},"except":[{"actor":{"fromImage":false,"ancestor":{"path":["/bin/sh"],"depth":16}}},{"target":{"pathClass":["unresolved"],"argv":{"stdinIsPipe":false}}}]}]},
 "file":{"defaultByAccess":{"read":"allow","write":"deny","create":"deny","delete":"deny","rename":"deny","chmod":"deny","execMap":"deny"},"defaultModeByAccess":{"read":"audit","write":"enforce"},"defaultOriginByAccess":{"read":"","write":"catalog/F1"},"rules":[{"name":"file","action":"deny","target":{},"except":[{"actor":{"role":["helper"]}},{"target":{"access":["read"],"fromImage":false}}]}]},
 "network":{"default":{"egress":"deny","listen":"deny"},"defaultMode":{"egress":"audit"},"defaultOrigin":{"listen":""},"rules":[{"name":"network","action":"deny","target":{},"except":[{"actor":{"uid":[0]}},{"target":{"cidr":["10.0.0.0/8"],"ports":[443,"8000-9000"],"destClass":["unresolved"]}}]}]},
 "privilege":{"default":"deny","defaultMode":"audit","defaultOrigin":"catalog/P1","rules":[{"name":"privilege","action":"deny","target":{"syscall":["unshare"]},"except":[{"actor":{"container":["sidecar"]}},{"target":{"syscall":["unshare"],"flags":["CLONE_NEWUSER"]}}]}]},
 "identity":{"default":"deny","defaultMode":"enforce","defaultOrigin":"catalog/I1","rules":[{"name":"identity","action":"deny","target":{"imds":true},"except":[{"actor":{"path":["/bin/helper"]}},{"target":{"envRead":false}}]}]}}
 `
	var spec PolicySpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePolicySpec(spec); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	var before, after any
	if err = json.Unmarshal([]byte(raw), &before); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(encoded, &after); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("roundtrip changed fields:\n%s", encoded)
	}
	copy := spec.DeepCopy()
	*copy.Exec.DefaultOrigin = "changed"
	*copy.Exec.Rules[0].Except[0].Actor.FromImage = true
	copy.Exec.Rules[0].Except[0].Actor.Ancestor.Path[0] = "changed"
	copy.Exec.Rules[0].Except[1].Target.PathClass[0] = "changed"
	*copy.Exec.Rules[0].Except[1].Target.Argv.StdinIsPipe = true
	copy.File.DefaultModeByAccess.Read = RuleEnforce
	*copy.File.DefaultOriginByAccess.Read = "changed"
	copy.File.Rules[0].Except[1].Target.Access[0] = "write"
	copy.Network.DefaultMode.Egress = RuleEnforce
	*copy.Network.DefaultOrigin.Listen = "changed"
	copy.Network.Rules[0].Except[1].Target.Ports[0].IntVal = 1
	copy.Privilege.Rules[0].Except[1].Target.Flags[0] = "changed"
	*copy.Identity.Rules[0].Except[1].Target.EnvRead = true
	original, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(encoded) {
		t.Fatal("deep copy aliased metadata or exception predicates")
	}
}

func TestDefaultMetadataValidation(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"old-exec", `{"exec":{"default":"deny"}}`, true},
		{"old-file", `{"file":{"default":"deny"}}`, true},
		{"scalar-file", `{"file":{"default":"allow","defaultMode":"audit","defaultOrigin":""}}`, true},
		{"network-partial", `{"network":{"default":{"egress":"deny","listen":"allow"},"defaultMode":{"listen":"enforce"},"defaultOrigin":{}}}`, true},
		{"exec-off", `{"exec":{"default":"deny","defaultMode":"off"}}`, false},
		{"file-off", `{"file":{"default":"deny","defaultMode":"off"}}`, false},
		{"network-off", `{"network":{"defaultMode":{"egress":"off"}}}`, false},
		{"privilege-off", `{"privilege":{"defaultMode":"off"}}`, false},
		{"identity-off", `{"identity":{"defaultMode":"off"}}`, false},
		{"file-wrong-mode-form", `{"file":{"default":"allow","defaultModeByAccess":{}}}`, false},
		{"file-wrong-origin-form", `{"file":{"default":"allow","defaultOriginByAccess":{}}}`, false},
		{"file-per-access-off", `{"file":{"defaultByAccess":{},"defaultModeByAccess":{"read":"off"}}}`, false},
		{"file-scalar-mode-with-map", `{"file":{"defaultByAccess":{},"defaultMode":"audit"}}`, false},
		{"file-scalar-empty-origin-with-map", `{"file":{"defaultByAccess":{},"defaultOrigin":""}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var spec PolicySpec
			if err := json.Unmarshal([]byte(tc.raw), &spec); err != nil {
				t.Fatal(err)
			}
			if err := ValidatePolicySpec(spec); (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestExceptionSemanticValidation(t *testing.T) {
	targets := map[string]string{"exec": "{}", "file": "{}", "network": "{}", "privilege": `{"syscall":["unshare"]}`, "identity": `{"imds":true}`}
	for section, target := range targets {
		for _, tc := range []struct {
			name, action, exceptions string
			valid                    bool
		}{
			{"actor-only", "deny", `[{"actor":{"fromImage":false}}]`, true},
			{"empty-list", "deny", `[]`, true},
			{"empty-entry", "deny", `[{}]`, false},
			{"empty-actor", "deny", `[{"actor":{}}]`, false},
			{"empty-target", "deny", `[{"target":{}}]`, false},
			{"empty-both", "deny", `[{"actor":{},"target":{}}]`, false},
			{"empty-actor-list", "deny", `[{"actor":{"path":[]}}]`, false},
			{"invalid-depth", "deny", `[{"actor":{"ancestor":{"path":["/bin/sh"],"depth":17}}}]`, false},
			{"empty-ancestor-path", "deny", `[{"actor":{"ancestor":{"path":[],"depth":1}}}]`, false},
			{"allow-entry", "allow", `[{"actor":{"role":["helper"]}}]`, false},
			{"allow-empty-list", "allow", `[]`, false},
		} {
			t.Run(section+"/"+tc.name, func(t *testing.T) {
				defaultValue := `"deny"`
				if section == "network" {
					defaultValue = `{"egress":"deny","listen":"deny"}`
				}
				raw := `{"` + section + `":{"default":` + defaultValue + `,"rules":[{"name":"test","action":"` + tc.action + `","target":` + target + `,"except":` + tc.exceptions + `}]}}`
				var spec PolicySpec
				if err := json.Unmarshal([]byte(raw), &spec); err != nil {
					t.Fatal(err)
				}
				if err := ValidatePolicySpec(spec); (err == nil) != tc.valid {
					t.Fatalf("valid=%v, error=%v", tc.valid, err)
				}
			})
		}
	}
	for _, tc := range []struct{ section, target string }{
		{"exec", `{"argv":{}}`},
		{"network", `{"cidr":["bad"]}`},
		{"network", `{"ports":["90-80"]}`},
		{"privilege", `{"syscall":["unshare"],"capability":["CAP_SYS_ADMIN"]}`},
		{"privilege", `{"capability":["CAP_SYS_ADMIN"],"flags":["CLONE_NEWUSER"]}`},
		{"identity", `{"imds":false,"envRead":false}`},
	} {
		t.Run(tc.section+"/target-"+tc.target, func(t *testing.T) {
			raw := `{"` + tc.section + `":{"default":"deny","rules":[{"name":"test","action":"deny","target":` + targets[tc.section] + `,"except":[{"target":` + tc.target + `}]}]}}`
			if tc.section == "network" {
				raw = strings.Replace(raw, `"default":"deny"`, `"default":{"egress":"deny","listen":"deny"}`, 1)
			}
			var spec PolicySpec
			if err := json.Unmarshal([]byte(raw), &spec); err != nil {
				t.Fatal(err)
			}
			if err := ValidatePolicySpec(spec); err == nil {
				t.Fatal("invalid exception accepted")
			}
		})
	}
}

func TestExceptionExecTargetHasNoEffects(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(`{"target":{"path":["/bin/helper"],"setRole":"privileged"}}`))
	decoder.DisallowUnknownFields()
	var exception ExecException
	if err := decoder.Decode(&exception); err == nil {
		t.Fatal("exception target accepted setRole effect")
	}
}
