package networkmatch

import "testing"

// Contract pinning for MatchIP. These tests encode the v0.0.2 IP-matching
// surface from spec §5.7. The fixtures in
// node-agent/tests/resources/network-wildcards/{01..08,15..20}.yaml are
// the user-facing examples; this file is the unit-level contract.

func TestMatchIP_LiteralEquality(t *testing.T) {
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"ipv4-hit", []string{"10.1.2.3"}, "10.1.2.3", true},
		{"ipv4-miss", []string{"10.1.2.3"}, "10.1.2.4", false},
		{"ipv6-hit-canonical", []string{"2001:db8::1"}, "2001:db8::1", true},
		{"ipv6-hit-different-format", []string{"2001:db8::1"}, "2001:0db8:0000:0000:0000:0000:0000:0001", true},
		// IPv4-mapped IPv6 ::ffff:a.b.c.d MUST match its IPv4 form — same on-the-wire
		// destination. net.IP.Equal handles this naturally; documenting it as a contract.
		{"ipv4-mapped-v6-matches-v4", []string{"10.0.0.1"}, "::ffff:10.0.0.1", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchIP(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchIP(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchIP_CIDRMembership(t *testing.T) {
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"ipv4-cidr-hit", []string{"10.0.0.0/8"}, "10.1.2.3", true},
		{"ipv4-cidr-edge-network", []string{"10.0.0.0/8"}, "10.0.0.0", true},
		{"ipv4-cidr-edge-broadcast", []string{"10.0.0.0/8"}, "10.255.255.255", true},
		{"ipv4-cidr-miss", []string{"10.0.0.0/8"}, "11.0.0.1", false},
		{"ipv4-cidr-32-equals-literal", []string{"10.1.2.3/32"}, "10.1.2.3", true},
		{"ipv4-cidr-32-other-miss", []string{"10.1.2.3/32"}, "10.1.2.4", false},
		{"ipv6-cidr-hit", []string{"2001:db8::/32"}, "2001:db8::1", true},
		{"ipv6-cidr-miss", []string{"2001:db8::/32"}, "2001:db9::1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchIP(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchIP(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchIP_AnySentinel(t *testing.T) {
	// The "*" sentinel matches any valid IP. Spec §5.7 row 3.
	cases := []struct {
		name     string
		observed string
		want     bool
	}{
		{"ipv4-any", "1.2.3.4", true},
		{"ipv6-any", "2001:db8::1", true},
		{"loopback-v4", "127.0.0.1", true},
		{"loopback-v6", "::1", true},
		{"empty-still-false", "", false}, // empty observation cannot match anything, even *
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchIP([]string{"*"}, tc.observed); got != tc.want {
				t.Errorf("MatchIP(['*'], %q) = %v, want %v", tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchIP_AnyAsCIDR(t *testing.T) {
	// 0.0.0.0/0 and ::/0 are the RFC-aligned alternatives to "*".
	if !MatchIP([]string{"0.0.0.0/0"}, "1.2.3.4") {
		t.Error("0.0.0.0/0 should match any IPv4")
	}
	if !MatchIP([]string{"::/0"}, "2001:db8::1") {
		t.Error("::/0 should match any IPv6")
	}
	// 0.0.0.0/0 alone does NOT cover IPv6 — RFC distinct address families.
	if MatchIP([]string{"0.0.0.0/0"}, "2001:db8::1") {
		t.Error("0.0.0.0/0 must NOT match IPv6 — distinct address family")
	}
	// ::/0 alone does NOT cover IPv4 (Go's net.IPNet behavior confirms this).
	if MatchIP([]string{"::/0"}, "1.2.3.4") {
		t.Error("::/0 must NOT match IPv4 — distinct address family")
	}
}

func TestMatchIP_RejectsMalformed(t *testing.T) {
	// Malformed entries are skipped (not crashed on); other valid entries
	// in the same list still match. This is the runtime-side defence;
	// admission-time validation should reject them at write time.
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"garbage-only-no-match", []string{"not-an-ip"}, "1.2.3.4", false},
		{"garbage-cidr-skipped", []string{"10.0.0.0/40"}, "10.1.2.3", false},
		{"garbage-skipped-but-valid-still-matches", []string{"not-an-ip", "10.1.2.3"}, "10.1.2.3", true},
		{"empty-profile", nil, "1.2.3.4", false},
		{"empty-string-entry", []string{""}, "1.2.3.4", false},
		{"observed-is-garbage", []string{"10.1.2.3"}, "not-an-ip", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchIP(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchIP(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchIP_ListAcceptIfAnyMatches(t *testing.T) {
	// Mirror of fixture 07: mixed list. Disjunctive — any single entry hit means match.
	profile := []string{"10.1.2.3", "192.168.0.0/16", "*"}
	if !MatchIP(profile, "10.1.2.3") {
		t.Error("literal hit in mixed list must match")
	}
	if !MatchIP(profile, "192.168.5.5") {
		t.Error("CIDR hit in mixed list must match")
	}
	// "*" is in the list, so anything matches via the sentinel.
	if !MatchIP(profile, "8.8.8.8") {
		t.Error("'*' in list must match any valid IP")
	}

	// Without the sentinel, only literal+CIDR coverage holds.
	narrower := []string{"10.1.2.3", "192.168.0.0/16"}
	if MatchIP(narrower, "8.8.8.8") {
		t.Error("non-listed IP must NOT match without '*' sentinel")
	}
	if !MatchIP(narrower, "192.168.5.5") {
		t.Error("CIDR-listed IP must match without '*' sentinel")
	}
}
