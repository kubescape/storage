package file

import (
	"fmt"
	"testing"
	"unsafe"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
)

func distinctGroupNeighbor(i int) softwarecomposition.NetworkNeighbor {
	return softwarecomposition.NetworkNeighbor{
		Identifier:  fmt.Sprintf("id-%d", i),
		Type:        softwarecomposition.CommunicationTypeEgress,
		DNS:         fmt.Sprintf("host-%d.example.com", i),
		IPAddresses: []string{fmt.Sprintf("52.216.%d.%d", i/256, i%256)},
	}
}

func serviceRefHeldNeighbor(i int) softwarecomposition.NetworkNeighbor {
	return softwarecomposition.NetworkNeighbor{
		Identifier:          fmt.Sprintf("ref-%d", i),
		Type:                softwarecomposition.CommunicationTypeIngress,
		ServiceRefNamespace: "honey",
		ServiceRefName:      fmt.Sprintf("svc-%d", i),
	}
}

// BenchmarkCollapseIPGroups_NoServiceRefEntries measures the held/toCollapse
// partition overhead on the 99% case: a large neighborhood with ZERO
// serviceRef/serviceSelector/entity entries. Every entry is in its own group
// (distinct DNS) and below the collapse threshold, so group-collapse work is
// minimal and the partition + regroup dominates. sizeof(NetworkNeighbor) is
// logged by TestNetworkNeighborStructSize to translate allocs into bytes.
func BenchmarkCollapseIPGroups_NoServiceRefEntries(b *testing.B) {
	settings := dynamicpathdetector.CollapseSettings{NetworkIPGroupThreshold: 100, NetworkCIDRFloorBits: 24}
	for _, n := range []int{100, 1000} {
		in := make([]softwarecomposition.NetworkNeighbor, 0, n)
		for i := 0; i < n; i++ {
			in = append(in, distinctGroupNeighbor(i))
		}
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = collapseIPGroups(in, settings)
			}
		})
	}
}

// BenchmarkCollapseIPGroups_WithHeldServiceRefs adds a handful of serviceRef
// entries that must be held out of the collapse, over a group of hosts that
// DOES collapse — the mixed case the partition exists for.
func BenchmarkCollapseIPGroups_WithHeldServiceRefs(b *testing.B) {
	settings := dynamicpathdetector.CollapseSettings{NetworkIPGroupThreshold: 5, NetworkCIDRFloorBits: 24}
	var in []softwarecomposition.NetworkNeighbor
	for i := 0; i < 200; i++ {
		in = append(in, hostNeighbor(fmt.Sprintf("52.216.%d.%d", i/256, i%256)))
	}
	for i := 0; i < 8; i++ {
		in = append(in, serviceRefHeldNeighbor(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := collapseIPGroups(in, settings)
		if len(out) < 8 {
			b.Fatal("held serviceRef entries must survive")
		}
	}
}

// BenchmarkPartitionOnly isolates the new held/toCollapse split so its cost is
// attributable independently of grouping/covering.
func BenchmarkPartitionOnly(b *testing.B) {
	in := make([]softwarecomposition.NetworkNeighbor, 0, 1000)
	for i := 0; i < 1000; i++ {
		in = append(in, distinctGroupNeighbor(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var held []softwarecomposition.NetworkNeighbor
		var toCollapse []softwarecomposition.NetworkNeighbor
		for _, e := range in {
			if e.ServiceRefNamespace != "" || e.ServiceRefName != "" || e.ServiceSelector != nil || e.Entity != "" {
				held = append(held, e)
			} else {
				toCollapse = append(toCollapse, e)
			}
		}
		if len(held) != 0 || len(toCollapse) != 1000 {
			b.Fatal("unexpected partition")
		}
	}
}

func TestNetworkNeighborStructSize(t *testing.T) {
	t.Logf("sizeof(softwarecomposition.NetworkNeighbor) = %d bytes", unsafe.Sizeof(softwarecomposition.NetworkNeighbor{}))
}
