package v1beta1_test

import (
	"encoding/json"
	"testing"

	api "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	apply "github.com/kubescape/storage/pkg/generated/applyconfiguration/softwarecomposition/v1beta1"
	"github.com/stretchr/testify/require"
)

func TestIgnoreRuleApplyFieldPresence(t *testing.T) {
	fields := []struct {
		name string
		set  func(*apply.IgnoreRuleApplyConfiguration, string) *apply.IgnoreRuleApplyConfiguration
	}{
		{"sourceKind", (*apply.IgnoreRuleApplyConfiguration).WithSourceKind},
		{"sourceName", (*apply.IgnoreRuleApplyConfiguration).WithSourceName},
		{"sourceNamespace", (*apply.IgnoreRuleApplyConfiguration).WithSourceNamespace},
		{"justification", (*apply.IgnoreRuleApplyConfiguration).WithJustification},
		{"impactStatement", (*apply.IgnoreRuleApplyConfiguration).WithImpactStatement},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			builder := apply.IgnoreRule()
			data, err := json.Marshal(builder)
			require.NoError(t, err)
			require.JSONEq(t, `{}`, string(data))

			for _, value := range []string{"first", "replacement", ""} {
				require.Same(t, builder, field.set(builder, value))
				data, err = json.Marshal(builder)
				require.NoError(t, err)
				var got map[string]string
				require.NoError(t, json.Unmarshal(data, &got))
				require.Equal(t, map[string]string{field.name: value}, got)
			}
		})
	}
}

func TestIgnoreRuleApplyNestedRoundTrip(t *testing.T) {
	want := api.IgnoreRule{
		Vulnerability:   "CVE-2021-44228",
		FixState:        "not-fixed",
		Package:         &api.IgnoreRulePackage{Name: "log4j", Version: "2.14.0"},
		SourceKind:      "SecurityException",
		SourceName:      "allow-log4j",
		SourceNamespace: "production",
		Justification:   "vulnerable_code_not_present",
		ImpactStatement: "Vulnerable component is not loaded into memory",
	}
	builder := apply.IgnoreRule().
		WithVulnerability(want.Vulnerability).
		WithFixState(want.FixState).
		WithPackage(apply.IgnoreRulePackage().WithName(want.Package.Name).WithVersion(want.Package.Version)).
		WithSourceKind(want.SourceKind).
		WithSourceName(want.SourceName).
		WithSourceNamespace(want.SourceNamespace).
		WithJustification(want.Justification).
		WithImpactStatement(want.ImpactStatement)

	data, err := json.Marshal(builder)
	require.NoError(t, err)
	var got api.IgnoreRule
	require.NoError(t, json.Unmarshal(data, &got))
	require.Equal(t, want, got)

	data, err = json.Marshal(apply.IgnoredMatch().WithAppliedIgnoreRules(builder))
	require.NoError(t, err)
	var match api.IgnoredMatch
	require.NoError(t, json.Unmarshal(data, &match))
	require.Equal(t, []api.IgnoreRule{want}, match.AppliedIgnoreRules)
}
