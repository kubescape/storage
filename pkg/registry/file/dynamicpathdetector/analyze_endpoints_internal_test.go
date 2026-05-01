package dynamicpathdetector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSplitEndpointPortAndPath_DefensiveContract pins the inputs that
// AnalyzeURL is supposed to produce (`:<port><path>`) AND the defensive
// behavior for bare-path / empty / no-leading-slash inputs that may
// arrive via ad-hoc lookups or tests. Without the guard, "foo" was
// returned as ("foo", "/") — silently treating an opaque token as a
// port number. Flagged on upstream PR #316 by CodeRabbit (C7).
func TestSplitEndpointPortAndPath_DefensiveContract(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantPort string
		wantPath string
	}{
		// Canonical AnalyzeURL output.
		{"empty", "", "", "/"},
		{"port_only", ":80", "80", "/"},
		{"port_with_root_path", ":80/", "80", "/"},
		{"port_with_path", ":80/health", "80", "/health"},
		{"wildcard_port", ":0", "0", "/"},
		{"wildcard_port_with_path", ":0/api/users", "0", "/api/users"},
		{"port_with_deep_path", ":443/v1/items/42", "443", "/v1/items/42"},

		// Defensive — bare paths arriving without the `:` prefix.
		{"bare_path", "/health", "", "/health"},
		{"bare_root", "/", "", "/"},
		{"bare_deep_path", "/v1/items/42", "", "/v1/items/42"},

		// Defensive — opaque token without a leading slash. Previous
		// behavior silently returned ("foo", "/") which would be
		// indistinguishable from port="foo". The guard normalises this
		// to ("", "/foo").
		{"opaque_token", "foo", "", "/foo"},
		{"opaque_with_dot", "host.example.com", "", "/host.example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPort, gotPath := splitEndpointPortAndPath(tt.input)
			assert.Equal(t, tt.wantPort, gotPort,
				"splitEndpointPortAndPath(%q) port = %q, want %q",
				tt.input, gotPort, tt.wantPort)
			assert.Equal(t, tt.wantPath, gotPath,
				"splitEndpointPortAndPath(%q) path = %q, want %q",
				tt.input, gotPath, tt.wantPath)
		})
	}
}
