package file

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"sort"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ipCollapseFieldSep = "\x00"

// collapseIPGroups aggregates NetworkNeighbor entries that differ only by IP
// into a small number of CIDR-bearing entries. Entries are grouped by
// (Type, DNS, NamespaceSelector, PodSelector); within a group whose count of
// aggregatable IPv4 host addresses exceeds settings.NetworkIPGroupThreshold,
// those hosts are replaced by covering CIDR block(s) no broader than
// settings.NetworkCIDRFloorBits.
//
// The pass is a fixpoint (AC10): already-collapsed CIDR values and the "*"
// sentinel / IPv6 values are treated as pass-through and are never re-parsed as
// host IPs or re-tightened, and collapsed output carries a deterministic
// Identifier, so a second run — whose groups now hold only CIDRs and thus have
// zero aggregatable hosts — leaves everything untouched.
func collapseIPGroups(entries []softwarecomposition.NetworkNeighbor, settings dynamicpathdetector.CollapseSettings) []softwarecomposition.NetworkNeighbor {
	if entries == nil {
		return nil
	}

	threshold := settings.NetworkIPGroupThreshold
	if threshold <= 0 {
		threshold = dynamicpathdetector.NetworkIPGroupThreshold
	}
	floorBits := settings.NetworkCIDRFloorBits
	if floorBits <= 0 || floorBits > 32 {
		floorBits = dynamicpathdetector.NetworkCIDRFloorBits
	}

	type group struct {
		key       string
		entries   []softwarecomposition.NetworkNeighbor
		repType   softwarecomposition.CommunicationType
		repDNS    string
		repNsSel  *metav1.LabelSelector
		repPodSel *metav1.LabelSelector
	}

	order := make([]string, 0)
	groups := make(map[string]*group)
	for _, e := range entries {
		key := neighborGroupKey(e)
		g, ok := groups[key]
		if !ok {
			g = &group{
				key:       key,
				repType:   e.Type,
				repDNS:    e.DNS,
				repNsSel:  e.NamespaceSelector,
				repPodSel: e.PodSelector,
			}
			groups[key] = g
			order = append(order, key)
		}
		g.entries = append(g.entries, e)
	}

	out := make([]softwarecomposition.NetworkNeighbor, 0, len(entries))
	for _, key := range order {
		g := groups[key]

		hosts, passthrough := classifyGroupAddresses(g.entries)
		if len(hosts) <= threshold {
			out = append(out, g.entries...)
			continue
		}

		cidrs := aggregateHosts(hosts, floorBits)
		// Merge the freshly aggregated CIDR(s) with the group's already-collapsed
		// pass-through CIDRs into a minimal set. Without this, incremental
		// learning re-collapses newly observed hosts to a CIDR that duplicates or
		// nests inside a block already held from an earlier save, producing garbage
		// like [52.216.0.0/26, 52.216.0.0/26, 52.216.0.0/27].
		values := minimizeCIDRs(append(cidrs, passthrough...))

		var dnsNames []string
		var ports []softwarecomposition.NetworkPort
		for _, e := range g.entries {
			dnsNames = append(dnsNames, e.DNSNames...)
			ports = append(ports, e.Ports...)
		}
		dnsNames = DeflateSortString(dnsNames)
		ports = DeflateStringer(ports)

		for _, v := range values {
			out = append(out, softwarecomposition.NetworkNeighbor{
				Identifier:        collapsedIdentifier(g.repType, g.repDNS, g.repNsSel, g.repPodSel, []string{v}),
				Type:              g.repType,
				DNS:               g.repDNS,
				DNSNames:          append([]string(nil), dnsNames...),
				Ports:             append([]softwarecomposition.NetworkPort(nil), ports...),
				PodSelector:       g.repPodSel,
				NamespaceSelector: g.repNsSel,
				IPAddress:         "",
				IPAddresses:       []string{v},
			})
		}
	}
	return out
}

// classifyGroupAddresses splits a group's address values into aggregatable IPv4
// host addresses (deduped) and pass-through values held verbatim. An entry's
// value comes from the singular IPAddress when set, otherwise from each element
// of IPAddresses. CIDRs, the "*" sentinel, IPv6 and unparseable values are
// pass-through and are never fed to aggregation, which is what makes the pass a
// fixpoint on already-collapsed input.
func classifyGroupAddresses(entries []softwarecomposition.NetworkNeighbor) ([]netip.Addr, []string) {
	seenHost := map[netip.Addr]struct{}{}
	seenPass := map[string]struct{}{}
	var hosts []netip.Addr
	var passthrough []string

	classify := func(v string) {
		if v == "" {
			return
		}
		if addr, err := netip.ParseAddr(v); err == nil && addr.Is4() {
			if _, ok := seenHost[addr]; !ok {
				seenHost[addr] = struct{}{}
				hosts = append(hosts, addr)
			}
			return
		}
		if _, ok := seenPass[v]; !ok {
			seenPass[v] = struct{}{}
			passthrough = append(passthrough, v)
		}
	}

	for _, e := range entries {
		if e.IPAddress != "" {
			classify(e.IPAddress)
			continue
		}
		for _, v := range e.IPAddresses {
			classify(v)
		}
	}
	return hosts, passthrough
}

// aggregateHosts returns the CIDR block(s) covering the given IPv4 hosts. If the
// hosts share a common prefix at least as long as floorBits it is emitted as a
// single block; otherwise each host is bucketed into a floorBits-length prefix
// so no emitted block is ever broader than the floor.
func aggregateHosts(hosts []netip.Addr, floorBits int) []string {
	if len(hosts) == 0 {
		return nil
	}
	if commonLen := commonPrefixLen(hosts); commonLen >= floorBits {
		return []string{netip.PrefixFrom(hosts[0], commonLen).Masked().String()}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, addr := range hosts {
		cidr := netip.PrefixFrom(addr, floorBits).Masked().String()
		if _, ok := seen[cidr]; !ok {
			seen[cidr] = struct{}{}
			out = append(out, cidr)
		}
	}
	return out
}

// minimizeCIDRs reduces a set of address values to the smallest equivalent set:
// CIDR values are deduplicated and any prefix wholly contained in a broader
// prefix of the set is dropped. This keeps incremental re-collapsing a fixpoint
// on the CIDR set — freshly aggregated blocks that duplicate or nest inside a
// group's already-collapsed pass-through CIDRs are absorbed rather than
// accumulated. Non-CIDR values (the "*" sentinel, IPv6, unparseable) are held
// verbatim and deduplicated. The result is sorted.
func minimizeCIDRs(values []string) []string {
	seenPfx := map[string]struct{}{}
	seenOther := map[string]struct{}{}
	var prefixes []netip.Prefix
	var others []string
	for _, v := range values {
		if p, err := netip.ParsePrefix(v); err == nil {
			m := p.Masked()
			key := m.String()
			if _, ok := seenPfx[key]; ok {
				continue
			}
			seenPfx[key] = struct{}{}
			prefixes = append(prefixes, m)
			continue
		}
		if _, ok := seenOther[v]; ok {
			continue
		}
		seenOther[v] = struct{}{}
		others = append(others, v)
	}

	out := make([]string, 0, len(prefixes)+len(others))
	for i, a := range prefixes {
		contained := false
		for j, b := range prefixes {
			if i == j {
				continue
			}
			// A broader prefix (fewer bits) that covers a's network address
			// subsumes a; drop a. Equal-width prefixes never subsume each other
			// and identical prefixes are already deduplicated above.
			if b.Bits() < a.Bits() && b.Contains(a.Addr()) {
				contained = true
				break
			}
		}
		if !contained {
			out = append(out, a.String())
		}
	}
	out = append(out, others...)
	sort.Strings(out)
	return out
}

// commonPrefixLen returns the number of leading bits shared by every address in
// the set. All addresses are assumed to be IPv4.
func commonPrefixLen(addrs []netip.Addr) int {
	base := addrs[0].As4()
	common := 32
	for _, addr := range addrs[1:] {
		b := addr.As4()
		n := 0
		for i := 0; i < 4 && n < common; i++ {
			x := base[i] ^ b[i]
			if x == 0 {
				n += 8
				continue
			}
			for bit := 7; bit >= 0; bit-- {
				if x&(1<<uint(bit)) != 0 {
					break
				}
				n++
			}
			break
		}
		if n < common {
			common = n
		}
	}
	return common
}

func neighborGroupKey(n softwarecomposition.NetworkNeighbor) string {
	return strings.Join([]string{
		string(n.Type),
		n.DNS,
		metav1.FormatLabelSelector(n.NamespaceSelector),
		metav1.FormatLabelSelector(n.PodSelector),
	}, ipCollapseFieldSep)
}

// collapsedIdentifier derives a stable Identifier from the group key plus the
// entry's own sorted CIDR list, so a later identifier-merge pass recognizes and
// re-merges the same collapsed entry across saves instead of duplicating it.
func collapsedIdentifier(t softwarecomposition.CommunicationType, dns string, nsSel, podSel *metav1.LabelSelector, cidrs []string) string {
	sorted := append([]string(nil), cidrs...)
	sort.Strings(sorted)
	fields := []string{
		string(t),
		dns,
		metav1.FormatLabelSelector(nsSel),
		metav1.FormatLabelSelector(podSel),
		strings.Join(sorted, ","),
	}
	sum := sha256.Sum256([]byte(strings.Join(fields, ipCollapseFieldSep)))
	return hex.EncodeToString(sum[:])
}
