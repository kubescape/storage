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

func TestCollapseIPGroups_ServiceRefAndEntityPreserved(t *testing.T) {
	// A collapsing IP group must not take serviceRef/entity neighbors with it:
	// they carry no aggregatable IPs and the group rebuild would drop their
	// fields (regression guard for the silent-data-loss bug).
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 256; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("10.1.5.%d", i)))
	}
	port := int32(9093)
	in = append(in,
		softwarecomposition.NetworkNeighbor{
			Identifier: "alertmanager", Type: "internal",
			ServiceRefNamespace: "honey", ServiceRefName: "alertmanager",
			Ports: []softwarecomposition.NetworkPort{{Name: "TCP-9093", Protocol: "TCP", Port: &port}},
		},
		softwarecomposition.NetworkNeighbor{Identifier: "probes", Type: "internal", Entity: "host"},
	)

	out := collapseIPGroups(in, testSettings())

	var svc, host *softwarecomposition.NetworkNeighbor
	for i := range out {
		if out[i].ServiceRefName == "alertmanager" {
			svc = &out[i]
		}
		if out[i].Entity == "host" {
			host = &out[i]
		}
	}
	require.NotNil(t, svc, "serviceRef neighbor must survive collapse")
	require.NotNil(t, host, "entity neighbor must survive collapse")
	assert.Equal(t, "honey", svc.ServiceRefNamespace)
	require.Len(t, svc.Ports, 1)
	assert.Equal(t, int32(9093), *svc.Ports[0].Port)
}

func TestCollapseIPGroups_ScatteredHostsBucketedToFloor(t *testing.T) {
	// 60 lone hosts, each in its own /16, do not share a common prefix as long as
	// the floor, so each is bucketed into its floor-length (/16) network. Output
	// is one block per distinct floor network — bounded by the number of networks
	// reached, not the host count — and no block is broader than the floor.
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 60; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("%d.%d.0.1", 10+i, i)))
	}

	out := collapseIPGroups(in, testSettings())

	require.Len(t, out, 60, "one bucket per distinct /16")
	for _, e := range out {
		require.Len(t, e.IPAddresses, 1)
		p, err := netip.ParsePrefix(e.IPAddresses[0])
		require.NoError(t, err)
		assert.Equal(t, 16, p.Bits(), "each lone host is bucketed into its floor-length network")
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

	// The two /24s fall in different /16s and share no common prefix as long as
	// the /16 floor, so each is bucketed into its floor network: a handful of
	// blocks (two /16s here), orders of magnitude below the host count.
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
	// IPv6 entry — held as a pass-through value verbatim (IPv6 is not aggregated,
	// since policy generation consumes only IPv4 collapsed entries)
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
	assert.Contains(t, values, "2001:db8::1")
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

func TestCollapseIPGroups_IPv6NotAggregatedHeldPassThrough(t *testing.T) {
	// IPv6 hosts are NOT aggregated into CIDRs: policy generation
	// (buildIPAddressesPeers) consumes only IPv4 collapsed entries, so folding
	// IPv6 hosts into an IPv6 CIDR would silently drop them — and their ports —
	// from the derived NetworkPolicy. They are held as individual pass-through
	// values instead, even when contiguous, while the co-located v4 group still
	// collapses normally.
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
	// v6 hosts survive verbatim, never merged into a /120 or /128 CIDR
	assert.Contains(t, values, "2606:4700:0:1::0", "contiguous v6 hosts stay individual")
	assert.Contains(t, values, "2001:db8::42", "lone v6 host stays verbatim")
	assert.NotContains(t, values, "2606:4700:0:1::/120", "v6 must not be aggregated into a CIDR")
	// the co-located IPv4 group still collapses (a fully-observed /26)
	assert.Contains(t, values, "10.5.0.0/26", "co-located IPv4 group still collapses")
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

func TestCoverPrefixes_HostsCollapseToCommonPrefixWhenTighterThanFloor(t *testing.T) {
	// Hosts sharing a common prefix at least as long as the floor collapse to that
	// single common block. {.1,.2,.3} share a /30, which is tighter than the /16
	// floor, so they aggregate to 52.216.0.0/30 (bounding the entry count to one
	// rather than emitting a /32 and a /31). The block is capped at the floor, but
	// the workload's own common prefix is honored when it is already narrower.
	hosts := []netip.Addr{
		netip.MustParseAddr("52.216.0.1"),
		netip.MustParseAddr("52.216.0.2"),
		netip.MustParseAddr("52.216.0.3"),
	}
	got := coverPrefixes(hosts, nil, 16)
	assert.Equal(t, []string{"52.216.0.0/30"}, got)
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

func TestDeflateNetworkNeighbors_ServiceFieldsNotCrossMerged(t *testing.T) {
	// The agent's Identifier hash covers Type/IPAddress/DNS/selectors only, so
	// serviceRef/serviceSelector/entity neighbors — whose hash inputs are all
	// empty — share one Identifier. Merging on Identifier alone would keep only
	// the first entry's service fields, silently dropping the other allowlist
	// entries and grafting their ports onto the survivor.
	p9093, p5432, p10250 := int32(9093), int32(5432), int32(10250)
	in := []softwarecomposition.NetworkNeighbor{
		{
			Identifier: "collide", Type: "internal",
			ServiceRefNamespace: "honey", ServiceRefName: "alertmanager",
			Ports: []softwarecomposition.NetworkPort{{Name: "TCP-9093", Protocol: "TCP", Port: &p9093}},
		},
		{
			Identifier: "collide", Type: "internal",
			ServiceRefNamespace: "db", ServiceRefName: "postgres",
			Ports: []softwarecomposition.NetworkPort{{Name: "TCP-5432", Protocol: "TCP", Port: &p5432}},
		},
		{
			Identifier: "collide", Type: "internal",
			ServiceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "guestbook"}},
			Ports:           []softwarecomposition.NetworkPort{{Name: "TCP-5432", Protocol: "TCP", Port: &p5432}},
		},
		{
			Identifier: "collide", Type: "internal", Entity: "host",
			Ports: []softwarecomposition.NetworkPort{{Name: "TCP-10250", Protocol: "TCP", Port: &p10250}},
		},
	}

	out := deflateNetworkNeighbors(in, testSettings())

	require.Len(t, out, 4, "neighbors differing only by service fields must not be merged")
	assert.Equal(t, in, out)
}

func TestDeflateNetworkNeighbors_FixpointWithServiceNeighborsAndCollapse(t *testing.T) {
	// Repeated PreSave cycles run the full deflate on its own output; with service
	// neighbors mixed into a collapsing IP group the held-out entries must reach a
	// stable fixpoint too — no loss, no duplication, no field stripping.
	p9093 := int32(9093)
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 80; i++ {
		e := hostNeighbor(fmt.Sprintf("100.68.%d.%d", i/64, i%64))
		e.Identifier = fmt.Sprintf("host-%d", i)
		in = append(in, e)
	}
	in = append(in,
		softwarecomposition.NetworkNeighbor{Type: softwarecomposition.CommunicationTypeEgress, DNS: "example.com", Identifier: "cidr", IPAddresses: []string{"200.0.0.0/16"}},
		softwarecomposition.NetworkNeighbor{Type: softwarecomposition.CommunicationTypeEgress, DNS: "example.com", Identifier: "star", IPAddresses: []string{"*"}},
		softwarecomposition.NetworkNeighbor{
			Identifier: "collide", Type: "internal",
			ServiceRefNamespace: "honey", ServiceRefName: "alertmanager",
			Ports: []softwarecomposition.NetworkPort{{Name: "TCP-9093", Protocol: "TCP", Port: &p9093}},
		},
		softwarecomposition.NetworkNeighbor{
			Identifier: "collide", Type: "internal",
			ServiceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "guestbook"}},
		},
		softwarecomposition.NetworkNeighbor{Identifier: "collide", Type: "internal", Entity: "host"},
		// same group key (Type+DNS+selectors) as the collapsing host group: the
		// group rebuild would swallow it and strip its serviceRef if it were not
		// held out of the collapse.
		softwarecomposition.NetworkNeighbor{
			Identifier: "gw", Type: softwarecomposition.CommunicationTypeEgress, DNS: "example.com",
			ServiceRefNamespace: "infra", ServiceRefName: "s3-gateway",
		},
	)

	settings := dynamicpathdetector.CollapseSettings{NetworkIPGroupThreshold: 50, NetworkCIDRFloorBits: 16}
	once := deflateNetworkNeighbors(in, settings)
	twice := deflateNetworkNeighbors(once, settings)
	thrice := deflateNetworkNeighbors(twice, settings)

	assert.Equal(t, once, twice, "deflateNetworkNeighbors must be a fixpoint with service neighbors present")
	assert.Equal(t, twice, thrice)

	var svc, sel, ent, gw int
	for _, e := range once {
		if e.ServiceRefName == "alertmanager" {
			svc++
			assert.Equal(t, "honey", e.ServiceRefNamespace)
			require.Len(t, e.Ports, 1)
		}
		if e.ServiceRefName == "s3-gateway" {
			gw++
			assert.Equal(t, "infra", e.ServiceRefNamespace)
		}
		if e.ServiceSelector != nil {
			sel++
			assert.Equal(t, map[string]string{"app": "guestbook"}, e.ServiceSelector.MatchLabels)
		}
		if e.Entity == "host" {
			ent++
		}
	}
	assert.Equal(t, 1, svc, "serviceRef neighbor survives exactly once")
	assert.Equal(t, 1, sel, "serviceSelector neighbor survives exactly once")
	assert.Equal(t, 1, ent, "entity neighbor survives exactly once")
	assert.Equal(t, 1, gw, "serviceRef neighbor sharing the collapsing group's key must be held out, not swallowed by the CIDR rebuild")
}

func TestDeflateNetworkNeighbors_IdenticalServiceNeighborsStillMerge(t *testing.T) {
	// Counterpart of the not-cross-merged test: a genuine duplicate (same
	// Identifier AND same service fields) must still dedup, or repeated saves
	// would grow the profile unboundedly.
	p9093 := int32(9093)
	entry := softwarecomposition.NetworkNeighbor{
		Identifier: "collide", Type: "internal",
		ServiceRefNamespace: "honey", ServiceRefName: "alertmanager",
		DNSNames: []string{"alertmanager.honey.svc.cluster.local."},
		Ports:    []softwarecomposition.NetworkPort{{Name: "TCP-9093", Protocol: "TCP", Port: &p9093}},
	}

	out := deflateNetworkNeighbors([]softwarecomposition.NetworkNeighbor{entry, entry}, testSettings())

	require.Len(t, out, 1)
	assert.Equal(t, []string{"alertmanager.honey.svc.cluster.local."}, out[0].DNSNames)
	require.Len(t, out[0].Ports, 1)
}
