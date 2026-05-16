package v1beta1

import (
	"os"
	"reflect"
	"strings"
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

// TestNetworkNeighbor_ProtoFile_DeclaresIPAddresses pins the
// source-of-truth alignment between the Go struct's protobuf tag
// (`protobuf:"bytes,9,rep,name=ipAddresses"`) and the .proto schema.
// CodeRabbit upstream PR #326 finding #2: when these two diverge, any
// future code-generation pass (or any consumer that compiles against
// the .proto file in a non-Go language) loses the IPAddresses field
// entirely without a single test failing.
//
// The runtime is unaffected — generated.pb.go already encodes field 9
// correctly — but the .proto file must declare it for the documentation
// surface to match the wire surface.
func TestNetworkNeighbor_ProtoFile_DeclaresIPAddresses(t *testing.T) {
	// First half of the contract: verify the Go struct tag actually
	// declares field 9 / wire type 2 / repeated. Tag drift on the Go
	// side would silently make any roundtrip use a different wire
	// number — the .proto-text scan below would still pass because
	// nothing connects the two halves yet. CodeRabbit upstream PR #33
	// follow-up review: pin both sides of the schema/tag alignment.
	sf, ok := reflect.TypeOf(NetworkNeighbor{}).FieldByName("IPAddresses")
	if !ok {
		t.Fatal("NetworkNeighbor.IPAddresses field not found")
	}
	goTag := sf.Tag.Get("protobuf")
	if !strings.Contains(goTag, "bytes,9,rep,name=ipAddresses") {
		t.Fatalf("unexpected protobuf tag for NetworkNeighbor.IPAddresses: %q "+
			"(want substring bytes,9,rep,name=ipAddresses)", goTag)
	}

	// Second half: the .proto file must declare the matching field on
	// the NetworkNeighbor message. Without this, regeneration drops the
	// field for downstream non-Go consumers.
	proto, err := os.ReadFile("generated.proto")
	if err != nil {
		t.Fatalf("read generated.proto: %v", err)
	}
	src := string(proto)

	nnStart := strings.Index(src, "message NetworkNeighbor {")
	if nnStart < 0 {
		t.Fatal("generated.proto: cannot find `message NetworkNeighbor {`")
	}
	// Slice from the NN message opening to the next message keyword (or EOF).
	nnEnd := strings.Index(src[nnStart+1:], "\nmessage ")
	if nnEnd < 0 {
		nnEnd = len(src) - nnStart - 1
	}
	body := src[nnStart : nnStart+1+nnEnd]

	// The Go struct tag uses field 9 / wire type 2 (length-delimited)
	// for IPAddresses. The .proto syntax for repeated string at field 9
	// is `repeated string ipAddresses = 9;`.
	if !strings.Contains(body, "repeated string ipAddresses = 9;") {
		t.Errorf("generated.proto NetworkNeighbor must declare `repeated string ipAddresses = 9;`\n"+
			"to match the Go struct's protobuf tag bytes,9,rep,name=ipAddresses.\n"+
			"body=\n%s", body)
	}
}
