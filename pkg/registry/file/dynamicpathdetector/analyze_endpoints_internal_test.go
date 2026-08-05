package dynamicpathdetector

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAnalyzeURL_IPv6Hosts pins AnalyzeURL's handling of scheme-less input
// whose authority is an IPv6 address. Before this, a bare IPv6 host was
// mis-parsed because "http://" was prepended blindly, and url.Parse then
// split on every colon in the address instead of just the port separator.
// The host itself is discarded downstream (only the port and path make it
// into the ":<port><path>" output), so these cases assert on that, not on
// the parsed host.
func TestAnalyzeURL_IPv6Hosts(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare_loopback", "::1/health", ":/health"},
		{"bare_ipv6_no_port", "2001:db8::1/health", ":/health"},
		{"trailing_hextet_not_a_port", "2001:db8::8080/x", ":/x"},
		{"already_bracketed_with_port", "[2001:db8::1]:8080/x", ":8080/x"},
		{"unbracketed_ipv6_with_trailing_group", "2001:db8::1:8080/x", ":/x"},
		{"ipv4_host_with_port_regression", "example.com:80/users/123", ":80/users/123"},
		{"canonical_form_regression", ":80/users/123", ":80/users/123"},
		{"scheme_pass_through_regression", "http://example.com:80/x", ":80/x"},
		{"ipv4_with_port_regression", "192.168.1.1:8080/path", ":8080/path"},
	}

	analyzer := NewPathAnalyzer(10)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AnalyzeURL(tt.input, analyzer)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got, "AnalyzeURL(%q)", tt.input)
		})
	}
}

// TestSplitEndpointPortAndPath_DefensiveContract pins the inputs that
// AnalyzeURL is supposed to produce (`:<port><path>`) AND the defensive
// behavior for bare-path / empty / no-leading-slash inputs that may
// arrive via ad-hoc lookups or tests. Without the guard, "foo" was
// returned as ("foo", "/") — silently treating an opaque token as a
// port number.
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
