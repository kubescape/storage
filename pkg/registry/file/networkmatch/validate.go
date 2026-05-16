package networkmatch

import (
	"fmt"
	"net"
	"strings"
)

// ValidateIPEntry returns an error describing why entry is not a valid
// member of an IPAddresses[] list, or nil if it is valid.
//
// Valid forms:
//   - literal IP    (parsed by net.ParseIP)
//   - CIDR          (parsed by net.ParseCIDR)
//   - the AnyIPSentinel ("*")
//
// This is the admission-time defence; runtime MatchIP also tolerates
// malformed entries (silently skips them) so a bad write doesn't kill
// the whole match.
func ValidateIPEntry(entry string) error {
	if entry == "" {
		return fmt.Errorf("empty IP entry")
	}
	if entry == AnyIPSentinel {
		return nil
	}
	if strings.Contains(entry, "/") {
		if _, _, err := net.ParseCIDR(entry); err != nil {
			return fmt.Errorf("malformed CIDR %q: %w", entry, err)
		}
		return nil
	}
	if net.ParseIP(entry) == nil {
		return fmt.Errorf("malformed IP %q (not a literal, not a CIDR)", entry)
	}
	return nil
}

// ValidateDNSEntry returns an error describing why entry is not a valid
// member of a DNSNames[] list, or nil if it is valid.
//
// Valid forms (spec §5.8):
//   - literal name (with or without trailing dot)
//   - leading "*"   (only as the first label, RFC 4592)
//   - trailing "*"  (only as the last label)
//   - mid "⋯"       (DynamicLabel, anywhere)
//
// Rejected:
//   - "**" anywhere (recursive — reserved)
//   - empty inner labels (e.g. "foo..bar")
//   - "*" in any position other than first or last
//   - lone "*" with no fixed anchor (degenerate single-label pattern)
func ValidateDNSEntry(entry string) error {
	if entry == "" {
		return fmt.Errorf("empty DNS entry")
	}
	canon := strings.TrimSuffix(entry, ".")
	if canon == "" {
		return fmt.Errorf("DNS entry %q is just a trailing dot", entry)
	}
	labels := strings.Split(canon, ".")
	if len(labels) == 1 && labels[0] == DNSWildcardLabel {
		return fmt.Errorf("lone %q is not a valid DNS pattern — needs an anchored suffix", DNSWildcardLabel)
	}
	for i, l := range labels {
		switch {
		case l == "":
			return fmt.Errorf("DNS entry %q has empty label at position %d", entry, i)
		case l == "**":
			return fmt.Errorf("DNS entry %q contains reserved recursive wildcard %q", entry, "**")
		case l == DNSWildcardLabel && i != 0 && i != len(labels)-1:
			return fmt.Errorf(
				"DNS entry %q: bare %q is only allowed as the first label (RFC 4592) "+
					"or last label (project extension); use %q for mid positions",
				entry, DNSWildcardLabel, DNSDynamicLabel)
		}
	}
	return nil
}
