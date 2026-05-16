package v1beta1

import (
	"reflect"
	"testing"
)

// TestNetworkNeighbor_IPAddresses_ProtobufRoundtrip pins the v0.0.2
// protobuf wire contract for the new IPAddresses field. Storage persists
// NetworkNeighborhood objects to etcd via this protobuf encoding; if
// the field is dropped on round-trip, the spec field is silently lost
// and runtime matchers see an empty list.
//
// Protobuf field number 9 (declared on the struct tag) MUST be preserved
// across Marshal → Unmarshal.
func TestNetworkNeighbor_IPAddresses_ProtobufRoundtrip(t *testing.T) {
	original := &NetworkNeighbor{
		Identifier:  "test-entry",
		Type:        "external",
		IPAddress:   "10.1.2.3", // deprecated singular still works
		IPAddresses: []string{"10.0.0.0/8", "192.168.0.0/16", "*", "2001:db8::/32"},
		DNSNames:    []string{"api.stripe.com.", "*.stripe.com."},
	}

	wire, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	decoded := &NetworkNeighbor{}
	if err := decoded.Unmarshal(wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if !reflect.DeepEqual(decoded.IPAddresses, original.IPAddresses) {
		t.Errorf("IPAddresses roundtrip mismatch:\n  got:  %v\n  want: %v",
			decoded.IPAddresses, original.IPAddresses)
	}

	// Sanity: existing fields still survive (no regression).
	if decoded.IPAddress != original.IPAddress {
		t.Errorf("deprecated IPAddress lost: got %q want %q", decoded.IPAddress, original.IPAddress)
	}
	if !reflect.DeepEqual(decoded.DNSNames, original.DNSNames) {
		t.Errorf("DNSNames lost: got %v want %v", decoded.DNSNames, original.DNSNames)
	}
}

// TestNetworkNeighbor_IPAddresses_EmptyOmitted confirms that an empty
// IPAddresses slice is not encoded on the wire (zero overhead for
// existing profiles that don't use the new field).
func TestNetworkNeighbor_IPAddresses_EmptyOmitted(t *testing.T) {
	withField := &NetworkNeighbor{
		Identifier:  "id",
		Type:        "external",
		IPAddresses: nil,
	}
	withoutField := &NetworkNeighbor{
		Identifier: "id",
		Type:       "external",
	}
	a, err := withField.Marshal()
	if err != nil {
		t.Fatalf("Marshal(withField): %v", err)
	}
	b, err := withoutField.Marshal()
	if err != nil {
		t.Fatalf("Marshal(withoutField): %v", err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("nil IPAddresses must encode identically to absent field;\n  got %d bytes vs %d bytes",
			len(a), len(b))
	}
}
