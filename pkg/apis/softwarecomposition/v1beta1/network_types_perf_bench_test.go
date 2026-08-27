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

// scanWireFields decodes the top-level protobuf frame of wire and returns, per
// field number, the payload length of each occurrence. Every NetworkNeighbor
// field is length-delimited (wiretype 2), so any other wiretype is a contract
// violation.
func scanWireFields(t *testing.T, wire []byte) map[int][]int {
	t.Helper()
	readVarint := func(i int) (uint64, int) {
		var v uint64
		for shift := uint(0); ; shift += 7 {
			if i >= len(wire) {
				t.Fatalf("truncated varint at offset %d", i)
			}
			b := wire[i]
			i++
			v |= uint64(b&0x7f) << shift
			if b < 0x80 {
				return v, i
			}
		}
	}
	fields := map[int][]int{}
	for i := 0; i < len(wire); {
		key, next := readVarint(i)
		field, wiretype := int(key>>3), int(key&7)
		if wiretype != 2 {
			t.Fatalf("field %d has wiretype %d, want 2 (length-delimited)", field, wiretype)
		}
		length, next := readVarint(next)
		if next+int(length) > len(wire) {
			t.Fatalf("field %d payload overruns buffer", field)
		}
		fields[field] = append(fields[field], int(length))
		i = next + int(length)
	}
	return fields
}

// TestNetworkNeighbor_EmptyNewFields_WireEncoding pins the exact wire encoding
// the four new fields (10-13) produce on a neighbor that does NOT use them —
// the overwhelmingly common case. The generated marshaller emits the scalar
// string fields 10/11/13 unconditionally (tag + zero-length varint, 2 bytes
// each) and omits the nil ServiceSelector pointer entirely; if a regeneration
// changes a field number, drops a field, or starts encoding an empty selector
// message, this fails instead of silently changing what peers decode.
func TestNetworkNeighbor_EmptyNewFields_WireEncoding(t *testing.T) {
	n := plainNeighbor()
	wire, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	// Hard-coded from the current generated marshaller; any drift is a wire
	// contract change and must be a deliberate decision, not an accident.
	const plainWireSize = 149
	if len(wire) != plainWireSize {
		t.Errorf("plain neighbor wire size = %d bytes, want %d — the encoding changed", len(wire), plainWireSize)
	}
	if n.Size() != len(wire) {
		t.Errorf("Size()=%d but Marshal produced %d bytes", n.Size(), len(wire))
	}

	fields := scanWireFields(t, wire)
	for _, f := range []int{10, 11, 13} {
		occ := fields[f]
		if len(occ) != 1 || occ[0] != 0 {
			t.Errorf("unset string field %d: want exactly one zero-length occurrence, got lengths %v", f, occ)
		}
	}
	if occ, present := fields[12]; present {
		t.Errorf("nil ServiceSelector must be absent from the wire, got field 12 with lengths %v", occ)
	}

	// Set fields must land on their declared numbers with their exact payloads.
	ref := serviceRefNeighbor()
	refWire, err := ref.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if ref.Size() != len(refWire) {
		t.Errorf("serviceRef neighbor: Size()=%d but Marshal produced %d bytes", ref.Size(), len(refWire))
	}
	refFields := scanWireFields(t, refWire)
	if got := refFields[10]; len(got) != 1 || got[0] != len("honey") {
		t.Errorf("field 10 (ServiceRefNamespace): want one occurrence of %d bytes, got %v", len("honey"), got)
	}
	if got := refFields[11]; len(got) != 1 || got[0] != len("alertmanager") {
		t.Errorf("field 11 (ServiceRefName): want one occurrence of %d bytes, got %v", len("alertmanager"), got)
	}

	sel := serviceSelectorNeighbor()
	selWire, err := sel.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if sel.Size() != len(selWire) {
		t.Errorf("serviceSelector neighbor: Size()=%d but Marshal produced %d bytes", sel.Size(), len(selWire))
	}
	selFields := scanWireFields(t, selWire)
	wantSel := sel.ServiceSelector.Size()
	if got := selFields[12]; len(got) != 1 || got[0] != wantSel {
		t.Errorf("field 12 (ServiceSelector): want one occurrence of %d bytes, got %v", wantSel, got)
	}
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
