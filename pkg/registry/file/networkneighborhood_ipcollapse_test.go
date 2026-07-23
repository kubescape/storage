package file

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testSettings() dynamicpathdetector.CollapseSettings {
	return dynamicpathdetector.CollapseSettings{
		NetworkIPGroupThreshold: 50,
		NetworkCIDRFloorBits:    16,
	}
}

func hostNeighbor(ip string) softwarecomposition.NetworkNeighbor {
	return softwarecomposition.NetworkNeighbor{
		Type:      softwarecomposition.CommunicationTypeEgress,
		DNS:       "example.com",
		DNSNames:  []string{"example.com"},
		IPAddress: ip,
	}
}

func TestCollapseIPGroups_BelowThresholdUntouched(t *testing.T) {
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 10; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("10.0.0.%d", i)))
	}

	out := collapseIPGroups(in, testSettings())

	assert.Equal(t, in, out)
	for _, e := range out {
		assert.NotEmpty(t, e.IPAddress)
		assert.Empty(t, e.IPAddresses)
	}
}

func TestCollapseIPGroups_AboveThresholdSingleCoveringCIDR(t *testing.T) {
	// A fully-observed /24 (all 256 hosts) exact-covers to exactly one /24 block.
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 256; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("10.1.5.%d", i)))
	}

	out := collapseIPGroups(in, testSettings())

	require.Len(t, out, 1)
	assert.Equal(t, []string{"10.1.5.0/24"}, out[0].IPAddresses)
	assert.Empty(t, out[0].IPAddress)
}

func TestCollapseIPGroups_ScatteredHostsStayGranularAndCappedAtFloor(t *testing.T) {
	// 60 lone hosts, each in its own /16 -> exact cover keeps them granular (one
	// /32 apiece, since none are adjacent); no emitted block is broader than the
	// floor, and the cover never over-approximates to a covering block.
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 60; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("%d.%d.0.1", 10+i, i)))
	}

	out := collapseIPGroups(in, testSettings())

	assert.Greater(t, len(out), 1)
	for _, e := range out {
		require.Len(t, e.IPAddresses, 1)
		p, err := netip.ParsePrefix(e.IPAddresses[0])
		require.NoError(t, err)
		assert.GreaterOrEqual(t, p.Bits(), 16, "no emitted block may be broader than the floor")
	}
}

func TestCollapseIPGroups_MixedGroupsNotCrossMerged(t *testing.T) {
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 60; i++ {
		e := hostNeighbor(fmt.Sprintf("10.2.0.%d", i))
		e.Type = softwarecomposition.CommunicationTypeEgress
		e.DNS = "egress.example"
		in = append(in, e)
	}
	for i := 0; i < 60; i++ {
		e := hostNeighbor(fmt.Sprintf("10.2.0.%d", i))
		e.Type = softwarecomposition.CommunicationTypeIngress
		e.DNS = "ingress.example"
		in = append(in, e)
	}

	out := collapseIPGroups(in, testSettings())

	dnsSeen := map[string]softwarecomposition.CommunicationType{}
	for _, e := range out {
		if prev, ok := dnsSeen[e.DNS]; ok {
			assert.Equal(t, prev, e.Type)
		}
		dnsSeen[e.DNS] = e.Type
	}
	assert.Contains(t, dnsSeen, "egress.example")
	assert.Contains(t, dnsSeen, "ingress.example")
}

func TestCollapseIPGroups_DifferentSelectorsNotMerged(t *testing.T) {
	sel := func(v string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{"app": v}}
	}
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 64; i++ { // a full /26 per selector -> one exact block each
		e := hostNeighbor(fmt.Sprintf("10.3.0.%d", i))
		e.PodSelector = sel("a")
		in = append(in, e)
	}
	for i := 0; i < 64; i++ {
		e := hostNeighbor(fmt.Sprintf("10.3.0.%d", i))
		e.PodSelector = sel("b")
		in = append(in, e)
	}

	out := collapseIPGroups(in, testSettings())

	require.Len(t, out, 2)
	selectors := map[string]bool{}
	for _, e := range out {
		require.NotNil(t, e.PodSelector)
		selectors[e.PodSelector.MatchLabels["app"]] = true
	}
	assert.True(t, selectors["a"])
	assert.True(t, selectors["b"])
}

func TestCollapseIPGroups_RealWorldShapeOrdersOfMagnitude(t *testing.T) {
	var in []softwarecomposition.NetworkNeighbor
	// 256 IPs fully covering 100.68.0.0/24
	for i := 0; i < 256; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("100.68.0.%d", i)))
	}
	// 256 IPs fully covering 16.15.180.0/24
	for i := 0; i < 256; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("16.15.180.%d", i)))
	}

	out := collapseIPGroups(in, testSettings())

	// 512 contiguous hosts exact-cover to a handful of blocks (two /24s here).
	assert.Less(t, len(out), 10)
	assert.Less(t, len(out), len(in)/50)
	for _, e := range out {
		require.Len(t, e.IPAddresses, 1)
		p, err := netip.ParsePrefix(e.IPAddresses[0])
		require.NoError(t, err)
		assert.GreaterOrEqual(t, p.Bits(), 16)
	}
}

func TestCollapseIPGroups_Idempotent(t *testing.T) {
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 80; i++ {
		e := hostNeighbor(fmt.Sprintf("100.68.%d.%d", i/64, i%64))
		e.Ports = []softwarecomposition.NetworkPort{{Name: "tcp-443"}}
		in = append(in, e)
	}
	// already-collapsed CIDR carried in the plural field
	in = append(in, softwarecomposition.NetworkNeighbor{
		Type:        softwarecomposition.CommunicationTypeEgress,
		DNS:         "example.com",
		IPAddresses: []string{"200.0.0.0/16"},
	})
	// "*" sentinel
	in = append(in, softwarecomposition.NetworkNeighbor{
		Type:        softwarecomposition.CommunicationTypeEgress,
		DNS:         "example.com",
		IPAddresses: []string{"*"},
	})
	// IPv6 entry — a lone v6 host exact-covers to its /128
	in = append(in, softwarecomposition.NetworkNeighbor{
		Type:      softwarecomposition.CommunicationTypeEgress,
		DNS:       "example.com",
		IPAddress: "2001:db8::1",
	})

	once := collapseIPGroups(in, testSettings())
	twice := collapseIPGroups(once, testSettings())

	assert.Equal(t, once, twice, "collapseIPGroups must be a fixpoint")

	// pass-through + covered values survived
	var values []string
	for _, e := range once {
		values = append(values, e.IPAddresses...)
	}
	assert.Contains(t, values, "*")
	assert.Contains(t, values, "200.0.0.0/16")
	assert.Contains(t, values, "2001:db8::1/128")
}

func TestCollapseIPGroups_FieldContract(t *testing.T) {
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 60; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("10.4.0.%d", i)))
	}

	out := collapseIPGroups(in, testSettings())

	for _, e := range out {
		assert.NotEmpty(t, e.IPAddresses)
		assert.Empty(t, e.IPAddress)
		assert.NotEmpty(t, e.Identifier)
	}
}

func TestCollapseIPGroups_MultiBucketReplicatesDNSNamesAndPorts(t *testing.T) {
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 60; i++ {
		e := hostNeighbor(fmt.Sprintf("%d.%d.0.1", 20+i, i))
		e.DNSNames = []string{fmt.Sprintf("host-%d.example", i)}
		e.Ports = []softwarecomposition.NetworkPort{{Name: fmt.Sprintf("tcp-%d", 8000+i)}}
		in = append(in, e)
	}

	out := collapseIPGroups(in, testSettings())

	require.Greater(t, len(out), 1)
	first := out[0]
	require.NotEmpty(t, first.DNSNames)
	require.NotEmpty(t, first.Ports)
	for _, e := range out {
		assert.Equal(t, first.DNSNames, e.DNSNames, "every bucket entry gets the full merged DNSNames")
		assert.Equal(t, first.Ports, e.Ports, "every bucket entry gets the full merged Ports")
	}
}

func TestCollapseIPGroups_IPv6Aggregated(t *testing.T) {
	// A full IPv6 /120 (256 contiguous v6 hosts) exact-covers to that /120, and a
	// lone v6 host to its /128 — alongside a v4 group, in one mixed-family pass.
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 256; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("2606:4700:0:1::%x", i)))
	}
	in = append(in, hostNeighbor("2001:db8::42"))
	for i := 0; i < 60; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("10.5.0.%d", i)))
	}

	out := collapseIPGroups(in, testSettings())

	var values []string
	for _, e := range out {
		values = append(values, e.IPAddresses...)
	}
	assert.Contains(t, values, "2606:4700:0:1::/120", "contiguous v6 hosts aggregate")
	assert.Contains(t, values, "2001:db8::42/128", "lone v6 host covers to /128")
}

func TestCoverPrefixes_IPv6ExactAndMerge(t *testing.T) {
	// Two adjacent v6 /33 halves merge into the parent /32 (Cloudflare 2606:4700::/32),
	// independent of any IPv4 floor.
	got := coverPrefixes(nil, []netip.Prefix{
		netip.MustParsePrefix("2606:4700::/33"),
		netip.MustParsePrefix("2606:4700:8000::/33"),
	}, 24)
	assert.Equal(t, []string{"2606:4700::/32"}, got)
}

// TestCoverPrefixes_RealCloudRangesDedupAndMerge feeds netipx the kind of messy,
// overlapping, non-aggregated CIDR lists cloud providers publish (a subsumed
// range, two adjacent siblings that merge, and disjoint blocks across families)
// and asserts the minimal exact cover.
func TestCoverPrefixes_RealCloudRangesDedupAndMerge(t *testing.T) {
	pass := []netip.Prefix{
		// AWS S3 us-east-1: 52.216.0.0/15 subsumes the more specific 52.216.4.0/24
		netip.MustParsePrefix("52.216.0.0/15"),
		netip.MustParsePrefix("52.216.4.0/24"),
		// Cloudflare: 104.16.0.0/13 subsumes 104.16.0.0/14
		netip.MustParsePrefix("104.16.0.0/13"),
		netip.MustParsePrefix("104.16.0.0/14"),
		// Cloudflare v6 siblings that merge to a /31
		netip.MustParsePrefix("2606:4700::/32"),
		netip.MustParsePrefix("2606:4701::/32"),
	}
	// Permissive floor (/8) so the cap does not split these broad blocks — this
	// isolates the dedup/merge behavior (the floor cap has its own test).
	got := coverPrefixes(nil, pass, 8)
	// sorted lexicographically (the collapse output order)
	assert.Equal(t, []string{
		"104.16.0.0/13",
		"2606:4700::/31",
		"52.216.0.0/15",
	}, got)
}

func TestCollapseIPGroups_NilInput(t *testing.T) {
	assert.Nil(t, collapseIPGroups(nil, testSettings()))
}

func TestCollapseIPGroups_IncrementalReCollapseDeduplicatesAndAbsorbs(t *testing.T) {
	// Regression for the incremental-learning garbage [/26, /26, /27]: a group
	// that already holds collapsed CIDRs from earlier saves (a /27 and a /26)
	// plus freshly observed hosts that re-aggregate to 52.216.0.0/26 must
	// converge to exactly one 52.216.0.0/26 — the duplicate /26 deduplicated and
	// the nested /27 absorbed — instead of accumulating all three entries.
	settings := dynamicpathdetector.CollapseSettings{
		NetworkIPGroupThreshold: 5,
		NetworkCIDRFloorBits:    16,
	}
	cidr := func(c string) softwarecomposition.NetworkNeighbor {
		return softwarecomposition.NetworkNeighbor{
			Type:        softwarecomposition.CommunicationTypeEgress,
			DNS:         "example.com",
			IPAddresses: []string{c},
		}
	}
	in := []softwarecomposition.NetworkNeighbor{
		cidr("52.216.0.0/27"),
		cidr("52.216.0.0/26"),
	}
	for _, h := range []string{"52.216.0.1", "52.216.0.10", "52.216.0.20", "52.216.0.40", "52.216.0.55", "52.216.0.60"} {
		in = append(in, hostNeighbor(h))
	}

	out := collapseIPGroups(in, settings)

	var cidrs []string
	for _, e := range out {
		cidrs = append(cidrs, e.IPAddresses...)
		assert.Empty(t, e.IPAddress)
	}
	assert.Equal(t, []string{"52.216.0.0/26"}, cidrs, "must converge to a single covering /26, not [/26 /26 /27]")
}

func TestCoverPrefixes_ExactNoOverApproximation(t *testing.T) {
	// Three non-adjacent hosts cover exactly {.1,.2,.3} — never a single /30
	// (which would admit the unobserved .0).
	hosts := []netip.Addr{
		netip.MustParseAddr("52.216.0.1"),
		netip.MustParseAddr("52.216.0.2"),
		netip.MustParseAddr("52.216.0.3"),
	}
	got := coverPrefixes(hosts, nil, 16)
	assert.Equal(t, []string{"52.216.0.1/32", "52.216.0.2/31"}, got)
}

func TestCoverPrefixes_MergesAdjacentSiblings(t *testing.T) {
	// The two /25 halves of a /24 merge into the single parent /24.
	got := coverPrefixes(nil, []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/25"),
		netip.MustParsePrefix("10.0.0.128/25"),
	}, 16)
	assert.Equal(t, []string{"10.0.0.0/24"}, got)
}

func TestCoverPrefixes_FloorCapSplitsBroadBlock(t *testing.T) {
	// A pass-through /22 under a /24 floor splits into its four /24 children;
	// none is broader than the floor.
	got := coverPrefixes(nil, []netip.Prefix{netip.MustParsePrefix("10.9.0.0/22")}, 24)
	assert.Equal(t, []string{"10.9.0.0/24", "10.9.1.0/24", "10.9.2.0/24", "10.9.3.0/24"}, got)
}
