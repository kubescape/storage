package v1alpha1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestRuleOriginPresenceRoundTripDeepCopyAndValidation(t *testing.T) {
	sections := []struct {
		name, target, defaultField string
		get                        func(PolicySpec) *Rule
	}{
		{"exec", `{}`, "", func(s PolicySpec) *Rule { return &s.Exec.Rules[0].Rule }},
		{"file", `{}`, `"default":"deny",`, func(s PolicySpec) *Rule { return &s.File.Rules[0].Rule }},
		{"network", `{}`, "", func(s PolicySpec) *Rule { return &s.Network.Rules[0].Rule }},
		{"privilege", `{"syscall":["unshare"]}`, "", func(s PolicySpec) *Rule { return &s.Privilege.Rules[0].Rule }},
		{"identity", `{"imds":true}`, "", func(s PolicySpec) *Rule { return &s.Identity.Rules[0].Rule }},
	}
	for _, section := range sections {
		for _, origin := range []struct {
			name, json string
			want       *string
		}{
			{name: "omitted"},
			{name: "empty", json: `,"origin":""`, want: stringPointer("")},
			{name: "nonempty", json: `,"origin":"catalog/R1"`, want: stringPointer("catalog/R1")},
		} {
			t.Run(section.name+"/"+origin.name, func(t *testing.T) {
				raw := `{"` + section.name + `":{` + section.defaultField + `"rules":[{"name":"test","action":"deny","target":` + section.target + origin.json + `}]}}`
				var spec PolicySpec
				if err := json.Unmarshal([]byte(raw), &spec); err != nil {
					t.Fatal(err)
				}
				if err := ValidatePolicySpec(spec); err != nil {
					t.Fatal(err)
				}
				if got := section.get(spec).Origin; !reflect.DeepEqual(got, origin.want) {
					t.Fatalf("decoded origin = %v, want %v", got, origin.want)
				}
				encoded, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				var roundTripped PolicySpec
				if err := json.Unmarshal(encoded, &roundTripped); err != nil {
					t.Fatal(err)
				}
				if got := section.get(roundTripped).Origin; !reflect.DeepEqual(got, origin.want) {
					t.Fatalf("round-tripped origin = %v, want %v; JSON: %s", got, origin.want, encoded)
				}
				copy := spec.DeepCopy()
				if got := section.get(*copy).Origin; !reflect.DeepEqual(got, origin.want) {
					t.Fatalf("copied origin = %v, want %v", got, origin.want)
				}
				if origin.want != nil {
					*section.get(*copy).Origin = "changed"
					if got := *section.get(spec).Origin; got != *origin.want {
						t.Fatalf("deep copy aliased origin: %q", got)
					}
				}
			})
		}
	}
}

func stringPointer(value string) *string { return &value }

func TestDefaultOriginPresence(t *testing.T) {
	for _, tc := range []struct {
		name, section, prefix, suffix string
		get                           func(PolicySpec) *string
	}{
		{"exec", "exec", `"default":"deny"`, "", func(s PolicySpec) *string { return s.Exec.DefaultOrigin }},
		{"file", "file", `"default":"deny"`, "", func(s PolicySpec) *string { return s.File.DefaultOrigin }},
		{"privilege", "privilege", `"default":"deny"`, "", func(s PolicySpec) *string { return s.Privilege.DefaultOrigin }},
		{"identity", "identity", `"default":"deny"`, "", func(s PolicySpec) *string { return s.Identity.DefaultOrigin }},
		{"file/read", "file", `"defaultByAccess":{"read":"deny","write":"deny","create":"deny","delete":"deny","rename":"deny","chmod":"deny","execMap":"deny"}`, "read", func(s PolicySpec) *string {
			if s.File.DefaultOriginByAccess == nil {
				return nil
			}
			return s.File.DefaultOriginByAccess.Read
		}},
		{"network/egress", "network", `"default":{"egress":"deny","listen":"deny"}`, "egress", func(s PolicySpec) *string {
			if s.Network.DefaultOrigin == nil {
				return nil
			}
			return s.Network.DefaultOrigin.Egress
		}},
	} {
		for _, origin := range []struct {
			name, value string
			want        *string
		}{
			{name: "omitted"},
			{name: "empty", value: `""`, want: stringPointer("")},
			{name: "nonempty", value: `"catalog/D1"`, want: stringPointer("catalog/D1")},
		} {
			t.Run(tc.name+"/"+origin.name, func(t *testing.T) {
				field := ""
				if origin.want != nil {
					if tc.suffix == "" {
						field = `,"defaultOrigin":` + origin.value
					} else {
						key := "defaultOriginByAccess"
						if tc.section == "network" {
							key = "defaultOrigin"
						}
						field = `,"` + key + `":{"` + tc.suffix + `":` + origin.value + `}`
					}
				}
				raw := `{"` + tc.section + `":{` + tc.prefix + field + `}}`
				var spec PolicySpec
				if err := json.Unmarshal([]byte(raw), &spec); err != nil {
					t.Fatal(err)
				}
				if err := ValidatePolicySpec(spec); err != nil {
					t.Fatal(err)
				}
				if got := tc.get(spec); !reflect.DeepEqual(got, origin.want) {
					t.Fatalf("decoded origin = %v, want %v", got, origin.want)
				}
				encoded, err := json.Marshal(spec)
				if err != nil {
					t.Fatal(err)
				}
				if origin.want == nil && strings.Contains(string(encoded), `"defaultOrigin"`) {
					t.Fatalf("omitted origin appeared: %s", encoded)
				}
				var roundTripped PolicySpec
				if err := json.Unmarshal(encoded, &roundTripped); err != nil {
					t.Fatal(err)
				}
				if got := tc.get(roundTripped); !reflect.DeepEqual(got, origin.want) {
					t.Fatalf("round-tripped origin = %v, want %v; JSON: %s", got, origin.want, encoded)
				}
				copy := spec.DeepCopy()
				if origin.want != nil {
					*tc.get(*copy) = "changed"
					if got := *tc.get(spec); got != *origin.want {
						t.Fatalf("deep copy aliased default origin: %q", got)
					}
				}
			})
		}
	}
}
