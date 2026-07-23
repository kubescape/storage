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
// those hosts plus any already-collapsed pass-through CIDRs are replaced by the
// minimal EXACT CIDR cover of exactly those addresses (see coverPrefixes), with
// no block broader than settings.NetworkCIDRFloorBits. The cover never
// over-approximates to a block the workload did not actually reach.
//
// The pass is a fixpoint: an exact cover re-covered is itself, and the "*"
// sentinel / bare IPv6 values are pass-through held verbatim, so a second run —
// whose groups now hold only CIDRs and thus have zero aggregatable hosts —
// leaves everything untouched.
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

// classifyGroupAddresses splits a group's address values into aggregatable host
// addresses (bare IPv4 or IPv6, deduped) and pass-through values held verbatim.
// An entry's value comes from the singular IPAddress when set, otherwise from
// each element of IPAddresses. The "*" sentinel and unparseable values are
// pass-through; already-collapsed CIDRs are pass-through here but the caller
// folds them back into the exact cover. Both address families are aggregated —
// netipx covers IPv4 and IPv6 alike.
func classifyGroupAddresses(entries []softwarecomposition.NetworkNeighbor) ([]netip.Addr, []string) {
	seenHost := map[netip.Addr]struct{}{}
	seenPass := map[string]struct{}{}
	var hosts []netip.Addr
	var passthrough []string

	classify := func(v string) {
		if v == "" {
			return
		}
		if addr, err := netip.ParseAddr(v); err == nil {
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

// coverPrefixes returns the minimal set of CIDR strings that covers EXACTLY the
// given IPv4 host addresses together with the group's already-collapsed
// pass-through CIDRs, capped so no prefix is broader than floorBits. netipx does
// the aggregation — deduplicating, dropping subsumed prefixes and merging
// adjacent siblings into the minimal exact cover in near-linear time — and any
// resulting prefix broader than the floor is then split into floorBits-wide
// children (all fully covered, since the parent lies wholly within the set).
//
// Because the cover is exact, it never over-approximates to a block the workload
// did not actually reach, and re-running on already-collapsed input is a
// fixpoint: no duplicate or nested blocks can accumulate across incremental
// saves. The result is sorted.
func coverPrefixes(hosts []netip.Addr, cidrPass []netip.Prefix, floorBits int) []string {
	if len(hosts) == 0 && len(cidrPass) == 0 {
		return nil
	}
	var b netipx.IPSetBuilder
	for _, h := range hosts {
		b.Add(h)
	}
	for _, p := range cidrPass {
		b.AddPrefix(p)
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
