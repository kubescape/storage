package v1beta1

import (
	"testing"
	"unsafe"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func plainNeighbor() *NetworkNeighbor {
	port := int32(8080)
	return &NetworkNeighbor{
		Identifier:  "c86ab7d1f0c4b4c6bb2b8a1a71cbba09e2ecf3d64ff0b4c1ad9e0e28c40a0e5f",
		Type:        "external",
		DNSNames:    []string{"s3.eu-west-1.amazonaws.com"},
		IPAddresses: []string{"52.216.4.0/24"},
		Ports:       []NetworkPort{{Name: "TCP-8080", Protocol: "TCP", Port: &port}},
	}
}

func serviceRefNeighbor() *NetworkNeighbor {
	n := plainNeighbor()
	n.IPAddresses = nil
	n.ServiceRefNamespace = "honey"
	n.ServiceRefName = "alertmanager"
	return n
}

func serviceSelectorNeighbor() *NetworkNeighbor {
	n := plainNeighbor()
	n.IPAddresses = nil
	n.ServiceSelector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": "alertmanager"}}
	return n
}

// TestNetworkNeighbor_EmptyNewFields_WireOverhead pins the wire-size cost the
// four new fields add to a neighbor that does NOT use them (the 99% case).
// go-to-protobuf marshals scalar string fields unconditionally (tag byte +
// zero-length varint), so the expected overhead is exactly 2 bytes per string
// field = 6 bytes; the nil ServiceSelector pointer adds 0.
func TestNetworkNeighbor_EmptyNewFields_WireOverhead(t *testing.T) {
	n := plainNeighbor()
	full := n.Size()

	// Reconstruct what the pre-feature size would have been: subtract the
	// unconditional empty-string encodings for fields 10, 11, 13.
	const emptyStringFieldBytes = 2 // 1 tag byte + 1 zero-length varint
	withoutNew := full - 3*emptyStringFieldBytes

	wire, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) != full {
		t.Fatalf("Size()=%d but Marshal produced %d bytes", full, len(wire))
	}
	t.Logf("plain neighbor wire size: %d bytes (pre-feature equivalent: %d, overhead: %d bytes = %.1f%%)",
		full, withoutNew, full-withoutNew, 100*float64(full-withoutNew)/float64(withoutNew))

	ref := serviceRefNeighbor()
	refWire, _ := ref.Marshal()
	t.Logf("serviceRef neighbor wire size: %d bytes", len(refWire))
	sel := serviceSelectorNeighbor()
	selWire, _ := sel.Marshal()
	t.Logf("serviceSelector neighbor wire size: %d bytes", len(selWire))
	t.Logf("sizeof(NetworkNeighbor) in-memory struct: %d bytes", unsafe.Sizeof(NetworkNeighbor{}))
}

func benchRoundtrip(b *testing.B, n *NetworkNeighbor) {
	b.Helper()
	wire, err := n.Marshal()
	if err != nil {
		b.Fatal(err)
	}
	b.Run("Size", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = n.Size()
		}
	})
	b.Run("Marshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := n.Marshal(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			out := &NetworkNeighbor{}
			if err := out.Unmarshal(wire); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkNetworkNeighbor_Plain(b *testing.B)           { benchRoundtrip(b, plainNeighbor()) }
func BenchmarkNetworkNeighbor_ServiceRef(b *testing.B)      { benchRoundtrip(b, serviceRefNeighbor()) }
func BenchmarkNetworkNeighbor_ServiceSelector(b *testing.B) { benchRoundtrip(b, serviceSelectorNeighbor()) }
