package dynamicpathdetectortests

import (
	"testing"

	dp "github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

// Black-box adversarial probes against CompareDynamic, the path wildcard matcher
// used by opens / endpoints / embedded exec-path tokens. Complements the
// differential parity gate (TestCompareDynamic_MemoiseGoldenAcceptance) and the
// ReDoS cases with security-boundary bypass attempts: a concrete path crafted to
// slip past a profile pattern (false-negative direction). Expected values are
// derived independently from the segment contract (`⋯` = exactly one segment,
// `*` = zero-or-more segments, literals matched whole, anchored at both ends).
func TestCompareDynamic_Adversarial_BoundaryBypass(t *testing.T) {
	cases := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		// Prefix-boundary: a trailing-* pattern must match CHILDREN, not a
		// sibling whose first segment merely shares a prefix.
		{"trailing_star_matches_child", "/etc/*", "/etc/ssh/sshd_config", true},
		{"trailing_star_rejects_prefix_sibling", "/etc/*", "/etcfoo/x", false},
		{"trailing_star_rejects_hyphen_sibling", "/etc/*", "/etc-evil/passwd", false},

		// Segment-boundary: a literal segment must match whole segments only —
		// no substring/prefix creep.
		{"literal_exact", "/var/log", "/var/log", true},
		{"literal_rejects_longer_segment", "/var/log", "/var/logging", false},
		{"literal_rejects_plural", "/var/log", "/var/logs", false},
		{"literal_anchored_no_child", "/var/log", "/var/log/app.log", false},

		// `⋯` is EXACTLY one segment — not zero, not many.
		{"ellipsis_one_segment", "/a/" + dp.DynamicIdentifier + "/c", "/a/b/c", true},
		{"ellipsis_rejects_two_segments", "/a/" + dp.DynamicIdentifier + "/c", "/a/b/x/c", false},
		{"ellipsis_rejects_zero_segments", "/a/" + dp.DynamicIdentifier + "/c", "/a/c", false},

		// mid `*` is zero-or-more, but the pattern stays anchored at the tail.
		{"mid_star_zero", "/a/" + dp.WildcardIdentifier + "/c", "/a/c", true},
		{"mid_star_many", "/a/" + dp.WildcardIdentifier + "/c", "/a/x/y/c", true},
		{"mid_star_rejects_unanchored_tail", "/a/" + dp.WildcardIdentifier + "/c", "/a/x/c/d", false},

		// Leading `*` must not let an attacker drop the required suffix.
		{"leading_star_matches", "/" + dp.WildcardIdentifier + "/secret", "/svc/secret", true},
		{"leading_star_rejects_wrong_suffix", "/" + dp.WildcardIdentifier + "/secret", "/svc/public", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := dp.CompareDynamic(c.pattern, c.path); got != c.want {
				t.Errorf("CompareDynamic(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
			}
		})
	}
}
