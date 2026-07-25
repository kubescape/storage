package file

import (
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"sort"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"go4.org/netipx"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ipCollapseFieldSep = "\x00"

// collapseIPGroups aggregates NetworkNeighbor entries that differ only by IP
// into a small number of CIDR-bearing entries. Entries are grouped by
// (Type, DNS, NamespaceSelector, PodSelector); within a group whose count of
// aggregatable IPv4 host addresses exceeds settings.NetworkIPGroupThreshold,
// those hosts plus any already-collapsed pass-through CIDRs are replaced by
// covering CIDR blocks no broader than settings.NetworkCIDRFloorBits (see
// coverPrefixes). Output size is bounded by the number of distinct floor-length
// networks the workload actually reached, not by the host count: scattered hosts
// collapse to one block per floor network rather than one /32 apiece.
//
// The pass is a fixpoint: collapsed blocks re-fed through the same aggregation
// converge to themselves, and the "*" sentinel / bare IPv6 values are
// pass-through held verbatim, so a second run — whose groups now hold only CIDRs
// and thus have zero aggregatable hosts — leaves everything untouched.
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

		// Split pass-through into already-collapsed CIDRs (folded into the cover)
		// and non-CIDR sentinels ("*", bare IPv6, unparseable) held verbatim.
		var cidrPass []netip.Prefix
		var sentinels []string
		for _, v := range passthrough {
			if p, err := netip.ParsePrefix(v); err == nil {
				cidrPass = append(cidrPass, p.Masked())
			} else {
				sentinels = append(sentinels, v)
			}
		}

		// Exact minimal CIDR cover of the hosts plus already-held CIDRs, capped at
		// the floor. Because it is an exact cover, incremental re-collapsing is a
		// fixpoint and never accumulates duplicate or nested blocks — the bug that
		// produced [52.216.0.0/26, 52.216.0.0/26, 52.216.0.0/27].
		values := coverPrefixes(hosts, cidrPass, floorBits)
		values = append(values, sentinels...)
		sort.Strings(values)

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
// pass-through (already-collapsed CIDRs are folded back into the cover by the
// caller; the rest are held verbatim). Only IPv4 hosts are aggregated: policy
// generation (buildIPAddressesPeers) skips non-IPv4 entries, so collapsing IPv6
// hosts into a CIDR would silently drop them — and any ports — from the derived
// NetworkPolicy. IPv6 is therefore kept as individual pass-through entries.
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

// coverPrefixes returns the set of CIDR strings covering the given IPv4 host
// addresses together with the group's already-collapsed pass-through CIDRs, with
// no block broader than floorBits and a bounded entry count.
//
// It works in two stages. First the raw hosts are aggregated to the floor
// (aggregateHostsToFloor): a group of hosts sharing a common prefix at least as
// long as the floor collapses to that single tight block — kept tighter than the
// floor when the traffic really is that tight, e.g. a fully-observed /26 — while
// scattered hosts are bucketed into their floor-length networks so the output is
// bounded by the number of distinct floor networks reached, not the host count.
// Second, those host blocks are folded together with the already-held CIDRs
// through netipx, which deduplicates, drops subsumed prefixes and merges adjacent
// siblings into a canonical set. That fold is what fixes incremental-learning
// garbage like [52.216.0.0/26, 52.216.0.0/26, 52.216.0.0/27] — the duplicate
// deduplicated and the nested /27 absorbed — and makes re-collapsing a fixpoint.
//
// Any IPv4 block still broader than the floor after the fold (a held block from a
// coarser prior floor, or floor networks that merged into a shorter parent) is
// split back into floorBits-wide children. IPv6 has no floor and is emitted as
// covered. The result is sorted.
func coverPrefixes(hosts []netip.Addr, cidrPass []netip.Prefix, floorBits int) []string {
	if len(hosts) == 0 && len(cidrPass) == 0 {
		return nil
	}
	var b netipx.IPSetBuilder
	for _, p := range aggregateHostsToFloor(hosts, floorBits) {
		b.AddPrefix(p)
	}
	for _, p := range cidrPass {
		b.AddPrefix(p.Masked())
	}
	set, err := b.IPSet()
	if err != nil || set == nil {
		return nil
	}

	var out []string
	for _, p := range set.Prefixes() {
		// The floor is an IPv4 breadth cap; IPv6 covers are emitted as-is (a /24
		// floor is meaningless for v6, whose covers are already narrow).
		if p.Addr().Is4() && p.Bits() < floorBits {
			out = append(out, splitToFloor(p, floorBits)...)
			continue
		}
		out = append(out, p.String())
	}
	sort.Strings(out)
	return out
}

// aggregateHostsToFloor collapses raw host addresses into CIDR blocks no broader
// than floorBits. IPv4 hosts sharing a common prefix at least as long as the
// floor collapse to that single common block (kept tighter than the floor when
// the traffic is genuinely that tight, e.g. a fully-observed /26); otherwise each
// host is bucketed into its floor-length network, so scattered traffic yields at
// most one block per distinct floor network. IPv6 hosts — which the caller keeps
// out of aggregation, but which are handled defensively here — are emitted as
// individual host prefixes and never widened by the IPv4 floor.
func aggregateHostsToFloor(hosts []netip.Addr, floorBits int) []netip.Prefix {
	var v4 []netip.Addr
	var out []netip.Prefix
	for _, h := range hosts {
		if h.Is4() {
			v4 = append(v4, h)
		} else {
			out = append(out, netip.PrefixFrom(h, h.BitLen()).Masked())
		}
	}
	if len(v4) == 0 {
		return out
	}
	if commonLen := commonPrefixLen(v4); commonLen >= floorBits {
		return append(out, netip.PrefixFrom(v4[0], commonLen).Masked())
	}
	seen := make(map[netip.Prefix]struct{}, len(v4))
	for _, h := range v4 {
		p := netip.PrefixFrom(h, floorBits).Masked()
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
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

// splitToFloor divides a prefix broader than floorBits into its floorBits-wide
// children. If the fan-out would exceed 2^NetworkMaxCIDRSplitBits blocks the
// prefix is returned unsplit, trading a strictly-honored floor for a bounded
// entry count.
func splitToFloor(p netip.Prefix, floorBits int) []string {
	shift := floorBits - p.Bits()
	if shift <= 0 {
		return []string{p.String()}
	}
	if shift > dynamicpathdetector.NetworkMaxCIDRSplitBits {
		return []string{p.String()}
	}
	count := 1 << shift
	out := make([]string, 0, count)
	child := netip.PrefixFrom(p.Addr(), floorBits).Masked()
	for i := 0; i < count; i++ {
		out = append(out, child.String())
		next := netipx.RangeOfPrefix(child).To().Next()
		if !next.IsValid() {
			break
		}
		child = netip.PrefixFrom(next, floorBits).Masked()
	}
	return out
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
