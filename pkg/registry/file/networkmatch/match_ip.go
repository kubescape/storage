package networkmatch

import (
	"net"
	"strings"
)

// AnyIPSentinel is the profile entry that matches any valid IP address.
// Equivalent to the union of 0.0.0.0/0 and ::/0. Spec §5.7.
const AnyIPSentinel = "*"

// IPMatcher is the compiled form of an IP profile.
// Callers in the hot path (CEL functions, runtime rules) build one per
// profile and reuse it across every observed event for that profile.
type IPMatcher struct {
	any      bool        // any AnyIPSentinel ("*") entry → match anything
	literals []net.IP    // already-parsed literal IPs (IPv4-canonicalized by net.ParseIP)
	cidrs    []*net.IPNet // pre-compiled CIDRs
}

// CompileIP builds an IPMatcher from a profile entry list.
// Malformed entries are silently dropped (validation is the admission layer's job).
// Returns a usable matcher even on an empty / all-malformed input — Match will return false.
func CompileIP(profileEntries []string) *IPMatcher {
	m := &IPMatcher{}
	for _, entry := range profileEntries {
		if entry == "" {
			continue
		}
		if entry == AnyIPSentinel {
			m.any = true
			continue
		}
		if strings.Contains(entry, "/") {
			_, cidr, err := net.ParseCIDR(entry)
			if err != nil {
				continue
			}
			m.cidrs = append(m.cidrs, cidr)
			continue
		}
		ip := net.ParseIP(entry)
		if ip == nil {
			continue
		}
		m.literals = append(m.literals, ip)
	}
	return m
}

// Match reports whether the observed IP text is admitted by this matcher.
func (m *IPMatcher) Match(observedIP string) bool {
	if observedIP == "" {
		return false
	}
	if m.any {
		// Even with the sentinel, the observation must be a valid IP.
		// Empty / garbage observations always fail (admission requires a real address).
		if net.ParseIP(observedIP) == nil {
			return false
		}
		return true
	}
	parsed := net.ParseIP(observedIP)
	if parsed == nil {
		return false
	}
	for _, lit := range m.literals {
		if lit.Equal(parsed) {
			return true
		}
	}
	for _, cidr := range m.cidrs {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}

// MatchIP is the convenience wrapper that compiles + matches in one call.
// Use this only on cold paths; hot paths SHOULD reuse a cached *IPMatcher
// constructed via CompileIP.
//
// Empty profile or empty observation returns false.
func MatchIP(profileEntries []string, observedIP string) bool {
	if observedIP == "" || len(profileEntries) == 0 {
		return false
	}
	return CompileIP(profileEntries).Match(observedIP)
}
