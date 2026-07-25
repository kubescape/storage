package file

import (
	"fmt"
	"net/netip"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

func cidrNeighbor(c string) softwarecomposition.NetworkNeighbor {
	return softwarecomposition.NetworkNeighbor{
		Type:        softwarecomposition.CommunicationTypeEgress,
		DNS:         "example.com",
		IPAddresses: []string{c},
	}
}

// BenchmarkCollapseIPGroups measures CPU/allocations of the full deflate path on
// a realistic incremental-learning snapshot: 200 contiguous hosts in a /24 plus
// two already-collapsed pass-through CIDRs from earlier saves, at a /16 and a
// /24 floor (the latter exercising the floor-cap split).
func BenchmarkCollapseIPGroups(b *testing.B) {
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 200; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("52.216.%d.%d", i/256, i%256)))
	}
	in = append(in, cidrNeighbor("52.216.4.0/24"), cidrNeighbor("52.216.0.0/16"))

	for _, floor := range []int{16, 24} {
		settings := dynamicpathdetector.CollapseSettings{NetworkIPGroupThreshold: 5, NetworkCIDRFloorBits: floor}
		b.Run(fmt.Sprintf("floor%d", floor), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = collapseIPGroups(in, settings)
			}
		})
	}
}

// BenchmarkCoverPrefixes isolates the netipx exact-cover step: 256 scattered
// hosts across a /16 plus two pass-through CIDRs, at a /16 and a /24 floor.
func BenchmarkCoverPrefixes(b *testing.B) {
	hosts := make([]netip.Addr, 0, 256)
	for i := 0; i < 256; i++ {
		hosts = append(hosts, netip.AddrFrom4([4]byte{52, 216, byte(i), byte((i * 7) % 256)}))
	}
	cidrPass := []netip.Prefix{
		netip.MustParsePrefix("52.216.4.0/24"),
		netip.MustParsePrefix("52.216.128.0/17"),
	}
	for _, floor := range []int{16, 24} {
		b.Run(fmt.Sprintf("floor%d", floor), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = coverPrefixes(hosts, cidrPass, floor)
			}
		})
	}
}
