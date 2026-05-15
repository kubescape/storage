package networkmatch

import "strings"

// DNS wildcard tokens. These mirror the path/argv tokens in
// dynamicpathdetector but apply with DNS-label semantics.
const (
	// DNSDynamicLabel is U+22EF — matches exactly one DNS label in
	// the middle of a pattern (mirror of dynamicpathdetector.DynamicIdentifier).
	DNSDynamicLabel = "⋯"

	// DNSWildcardLabel is "*" — matches exactly one label when it's the
	// LEADING label (RFC 4592), or one or more labels when it's the
	// TRAILING label (project extension, spec §5.8 row 3).
	DNSWildcardLabel = "*"
)

// DNSMatcher is the compiled form of a DNS profile.
// Each entry compiles into one dnsPattern struct.
type DNSMatcher struct {
	patterns []dnsPattern
}

type dnsPattern struct {
	labels         []string // labels in declaration order, lowercased, trailing dot stripped
	hasLeadingStar bool     // labels[0] == "*"
	hasTrailingStar bool    // labels[len-1] == "*"
	valid          bool     // false if pattern was malformed (e.g. "**" or empty label)
}

// CompileDNS builds a DNSMatcher from profile entries.
// Malformed entries (empty, "**", empty inner labels) are silently skipped.
func CompileDNS(profileEntries []string) *DNSMatcher {
	m := &DNSMatcher{}
	for _, entry := range profileEntries {
		p := compileDNSPattern(entry)
		if !p.valid {
			continue
		}
		m.patterns = append(m.patterns, p)
	}
	return m
}

// Match reports whether the observed DNS name is admitted by this matcher.
func (m *DNSMatcher) Match(observed string) bool {
	if observed == "" {
		return false
	}
	obsLabels, ok := splitDNS(observed)
	if !ok {
		return false
	}
	for i := range m.patterns {
		if matchDNSPattern(&m.patterns[i], obsLabels) {
			return true
		}
	}
	return false
}

// MatchDNS is the convenience wrapper. Hot paths SHOULD reuse a
// compiled *DNSMatcher built once via CompileDNS.
func MatchDNS(profileEntries []string, observed string) bool {
	if observed == "" || len(profileEntries) == 0 {
		return false
	}
	return CompileDNS(profileEntries).Match(observed)
}

// splitDNS canonicalizes a DNS name (lowercases, strips trailing dot)
// and splits on "." into labels. Returns (labels, valid).
// An empty inner label (e.g. "foo..bar") returns valid=false.
func splitDNS(name string) ([]string, bool) {
	canon := strings.ToLower(strings.TrimSuffix(name, "."))
	if canon == "" {
		return nil, false
	}
	labels := strings.Split(canon, ".")
	for _, l := range labels {
		if l == "" {
			return nil, false
		}
	}
	return labels, true
}

// compileDNSPattern parses one profile entry into a dnsPattern.
// Sets valid=false on malformed input (which the caller silently skips).
func compileDNSPattern(entry string) dnsPattern {
	if entry == "" {
		return dnsPattern{}
	}
	canon := strings.ToLower(strings.TrimSuffix(entry, "."))
	if canon == "" {
		return dnsPattern{}
	}
	labels := strings.Split(canon, ".")
	for _, l := range labels {
		switch {
		case l == "":
			// foo..bar — empty inner label is malformed.
			return dnsPattern{}
		case l == "**":
			// Reserved/recursive — explicitly rejected per spec §5.8.
			return dnsPattern{}
		}
	}
	p := dnsPattern{labels: labels, valid: true}
	if len(labels) > 0 {
		if labels[0] == DNSWildcardLabel {
			p.hasLeadingStar = true
		}
		if labels[len(labels)-1] == DNSWildcardLabel {
			p.hasTrailingStar = true
		}
	}
	// "*" alone (single-label pattern) is degenerate. Treat as
	// leading-star with one label semantics — but since there's no suffix
	// to match against, it's only useful matching single-label observations.
	// Spec §5.8 doesn't bless this; reject for safety.
	if len(labels) == 1 && p.hasLeadingStar {
		return dnsPattern{}
	}
	return p
}

// matchDNSPattern evaluates one compiled pattern against observed labels.
//
// Algorithm: walk pattern labels left-to-right against observed labels,
// applying token semantics:
//
//   leading "*"  (only at index 0): consumes EXACTLY ONE observed label  (RFC 4592)
//   "⋯"          (any position):    consumes EXACTLY ONE observed label
//   trailing "*" (only at last):    consumes ONE OR MORE observed labels (§5.8)
//   literal:                        byte-equality (already lowercased)
//
// Mid-position "*" tokens (i.e. "*" not at index 0 and not at last index)
// are treated as DynamicLabel-equivalent (one label) — but spec restricts
// declaration to leading/trailing only; admission validates the position.
func matchDNSPattern(p *dnsPattern, obs []string) bool {
	plabels := p.labels
	pi := 0
	oi := 0
	plen := len(plabels)
	olen := len(obs)

	for pi < plen {
		tok := plabels[pi]
		isLast := pi == plen-1
		isFirst := pi == 0

		// Trailing "*" — consume one or more remaining labels.
		// Pattern ends here, so observed must have at least one label left
		// AND we exit the loop after.
		if tok == DNSWildcardLabel && isLast && !isFirst {
			return olen-oi >= 1
		}

		// Leading "*" — consume exactly one label.
		if tok == DNSWildcardLabel && isFirst {
			if oi >= olen {
				return false
			}
			oi++
			pi++
			continue
		}

		// "⋯" — consume exactly one label (any position).
		if tok == DNSDynamicLabel {
			if oi >= olen {
				return false
			}
			oi++
			pi++
			continue
		}

		// Mid-position "*" (declaration-illegal but defensive): treat as one label.
		if tok == DNSWildcardLabel {
			if oi >= olen {
				return false
			}
			oi++
			pi++
			continue
		}

		// Literal label — byte equality.
		if oi >= olen || obs[oi] != tok {
			return false
		}
		oi++
		pi++
	}

	// Pattern fully consumed — observed must also be fully consumed
	// (anchored match — DNS patterns are FQDN-anchored).
	return oi == olen
}
