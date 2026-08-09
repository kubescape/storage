package v1beta1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIgnoreRuleProvenanceFields(t *testing.T) {
	rule := IgnoreRule{
		Vulnerability:   "CVE-2021-44228",
		FixState:        "not-fixed",
		SourceKind:      "SecurityException",
		SourceName:      "allow-log4j",
		SourceNamespace: "production",
		Justification:   "vulnerable_code_not_present",
		ImpactStatement: "Vulnerable component is not loaded into the memory",
	}

	t.Run("JSON round-trip preserves provenance fields", func(t *testing.T) {
		raw, err := json.Marshal(rule)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"sourceKind":"SecurityException"`)

		var got IgnoreRule
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Equal(t, rule, got)
	})

	t.Run("DeepCopy preserves provenance fields", func(t *testing.T) {
		clone := rule.DeepCopy()
		assert.Equal(t, rule, *clone)

		clone.SourceName = "mutated"
		assert.Equal(t, "allow-log4j", rule.SourceName, "mutating the clone must not affect the original")
	})
}
