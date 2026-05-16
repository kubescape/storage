package networkmatch

import "testing"

// Contract pinning for MatchDNS / CompileDNS. Encodes spec §5.8.
// User-facing fixtures: node-agent/tests/resources/network-wildcards/{09..14,17,18}.yaml

func TestMatchDNS_LiteralEquality(t *testing.T) {
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"hit", []string{"api.stripe.com."}, "api.stripe.com.", true},
		{"miss-different-tld", []string{"api.stripe.com."}, "api.stripe.org.", false},
		{"miss-extra-label", []string{"api.stripe.com."}, "v1.api.stripe.com.", false},
		{"miss-too-short", []string{"api.stripe.com."}, "stripe.com.", false},
		{"case-insensitive", []string{"API.Stripe.com."}, "api.stripe.com.", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchDNS(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchDNS_TrailingDotNormalisation(t *testing.T) {
	// Trailing dot is the FQDN canonical form. Profile entries SHOULD have it,
	// observed names from runtime SHOULD have it, but the matcher MUST be
	// resilient to either form on either side.
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"both-with-dot", []string{"api.stripe.com."}, "api.stripe.com.", true},
		{"profile-no-dot", []string{"api.stripe.com"}, "api.stripe.com.", true},
		{"observed-no-dot", []string{"api.stripe.com."}, "api.stripe.com", true},
		{"neither-dot", []string{"api.stripe.com"}, "api.stripe.com", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchDNS(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchDNS_LeadingWildcard_RFC4592(t *testing.T) {
	// "*.example.com." matches EXACTLY ONE label before the suffix.
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"hit-one-label", []string{"*.example.com."}, "api.example.com.", true},
		{"miss-zero-labels", []string{"*.example.com."}, "example.com.", false},
		{"miss-two-labels", []string{"*.example.com."}, "v1.api.example.com.", false},
		{"miss-different-suffix", []string{"*.example.com."}, "api.example.org.", false},
		{"hit-with-numeric-label", []string{"*.example.com."}, "v1.example.com.", true},
		{"hit-with-hyphen", []string{"*.example.com."}, "my-app.example.com.", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchDNS(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchDNS_MidEllipsis(t *testing.T) {
	// "<a>.⋯.<b>" — DynamicIdentifier matches EXACTLY ONE label in the middle.
	// This is the user's specific case for kubernetes service FQDNs.
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"hit-one-label-mid", []string{"kubernetes.⋯.svc.cluster.local."}, "kubernetes.default.svc.cluster.local.", true},
		{"hit-different-ns", []string{"kubernetes.⋯.svc.cluster.local."}, "kubernetes.kube-system.svc.cluster.local.", true},
		{"miss-zero-labels-mid", []string{"kubernetes.⋯.svc.cluster.local."}, "kubernetes.svc.cluster.local.", false},
		{"miss-two-labels-mid", []string{"kubernetes.⋯.svc.cluster.local."}, "kubernetes.foo.bar.svc.cluster.local.", false},
		{"miss-different-prefix", []string{"kubernetes.⋯.svc.cluster.local."}, "redis.default.svc.cluster.local.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchDNS(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchDNS_TrailingStar(t *testing.T) {
	// "<prefix>.*" — trailing * matches ONE OR MORE labels (never zero).
	// This is the project-specific extension (not RFC 4592 — that only
	// covers leading *).
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"hit-one-label", []string{"internal.*"}, "internal.foo.", true},
		{"hit-many-labels", []string{"internal.*"}, "internal.foo.bar.baz.", true},
		{"miss-zero-labels", []string{"internal.*"}, "internal.", false},
		{"miss-different-prefix", []string{"internal.*"}, "external.foo.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchDNS(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchDNS_ListAcceptIfAnyMatches(t *testing.T) {
	// Disjunction across the entry list. Mirror of fixture 17.
	profile := []string{
		"api.stripe.com.",
		"*.stripe.com.",
		"api.partner.io.",
	}
	cases := []struct {
		observed string
		want     bool
	}{
		{"api.stripe.com.", true},        // literal hit
		{"webhooks.stripe.com.", true},   // *.stripe.com.
		{"v1.api.stripe.com.", false},    // two labels deep, *.stripe.com. only allows one
		{"api.partner.io.", true},        // literal hit
		{"api.example.com.", false},      // not in any entry
	}
	for _, tc := range cases {
		t.Run(tc.observed, func(t *testing.T) {
			if got := MatchDNS(profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(profile, %q) = %v, want %v", tc.observed, got, tc.want)
			}
		})
	}
}

func TestMatchDNS_RejectsMalformed(t *testing.T) {
	cases := []struct {
		name     string
		profile  []string
		observed string
		want     bool
	}{
		{"empty-profile", nil, "api.stripe.com.", false},
		{"empty-observation", []string{"api.stripe.com."}, "", false},
		{"empty-string-entry-skipped", []string{""}, "api.stripe.com.", false},
		{"recursive-double-star-rejected", []string{"**"}, "anything.com.", false},
		{"empty-label-in-pattern-not-recognised", []string{"foo..bar."}, "foo.bar.", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchDNS(tc.profile, tc.observed); got != tc.want {
				t.Errorf("MatchDNS(%v, %q) = %v, want %v", tc.profile, tc.observed, got, tc.want)
			}
		})
	}
}

func TestCompiledDNSMatcher_Reuse(t *testing.T) {
	// Compiled-form contract: build once, match many.
	m := CompileDNS([]string{"*.stripe.com.", "api.partner.io."})
	if !m.Match("webhooks.stripe.com.") {
		t.Error("compiled matcher missed *.stripe.com. hit")
	}
	if m.Match("v1.api.stripe.com.") {
		t.Error("compiled matcher should NOT match two-label-deep against *.stripe.com.")
	}
	if !m.Match("api.partner.io.") {
		t.Error("compiled matcher missed literal hit")
	}
}
