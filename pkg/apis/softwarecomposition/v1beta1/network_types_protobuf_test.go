package v1beta1

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestNetworkNeighbor_ServiceSelectors_ProtobufRoundtrip pins the wire
// contract for the ServiceRef/ServiceSelector/Entity fields (protobuf field
// numbers 10-13). These carry Kubernetes-native peer selectors
// (k8sstormcenter/node-agent#92); a dropped field silently reverts an
// allowlist to unresolved and reopens the FP/blindspot it was closing.
func TestNetworkNeighbor_ServiceSelectors_ProtobufRoundtrip(t *testing.T) {
	original := &NetworkNeighbor{
		Identifier:          "svc-entry",
		Type:                "internal",
		ServiceRefNamespace: "honey",
		ServiceRefName:      "alertmanager",
		ServiceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "guestbook"},
			MatchExpressions: []metav1.LabelSelectorRequirement{
				{Key: "tier", Operator: metav1.LabelSelectorOpIn, Values: []string{"frontend", "backend"}},
			},
		},
		Entity: "host",
		Ports:               []NetworkPort{{Name: "TCP-9093", Protocol: "TCP", Port: ptr(int32(9093))}},
	}

	wire, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded := &NetworkNeighbor{}
	if err := decoded.Unmarshal(wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ServiceRefNamespace != original.ServiceRefNamespace {
		t.Errorf("ServiceRefNamespace: got %q want %q", decoded.ServiceRefNamespace, original.ServiceRefNamespace)
	}
	if decoded.ServiceRefName != original.ServiceRefName {
		t.Errorf("ServiceRefName: got %q want %q", decoded.ServiceRefName, original.ServiceRefName)
	}
	if decoded.Entity != original.Entity {
		t.Errorf("Entity: got %q want %q", decoded.Entity, original.Entity)
	}
	if !reflect.DeepEqual(decoded.ServiceSelector, original.ServiceSelector) {
		t.Errorf("ServiceSelector roundtrip mismatch:\n  got:  %v\n  want: %v", decoded.ServiceSelector, original.ServiceSelector)
	}
}

// TestNetworkNeighbor_NewFields_Absent confirms a neighbor that sets none of
// the new fields round-trips with them empty (no corruption) and its existing
// fields intact — the common case for profiles predating this change. The
// scalar fields are proto2 optional/non-nullable, so go-to-protobuf marshals
// them unconditionally (like the existing dns/ipAddress fields); this pins that
// an unset->marshal->unmarshal cycle still yields empty, not garbage.
func TestNetworkNeighbor_NewFields_Absent(t *testing.T) {
	original := &NetworkNeighbor{
		Identifier:  "id",
		Type:        "external",
		IPAddresses: []string{"10.0.0.0/8"},
	}
	wire, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded := &NetworkNeighbor{}
	if err := decoded.Unmarshal(wire); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.ServiceRefNamespace != "" || decoded.ServiceRefName != "" || decoded.Entity != "" || decoded.ServiceSelector != nil {
		t.Errorf("unset new fields must round-trip empty, got %+v", decoded)
	}
	if !reflect.DeepEqual(decoded.IPAddresses, original.IPAddresses) {
		t.Errorf("existing field lost: got %v", decoded.IPAddresses)
	}
}

func ptr[T any](v T) *T { return &v }

// TestNetworkNeighbor_IPAddresses_ProtobufRoundtrip pins the v0.0.2
// protobuf wire contract for the new IPAddresses field. Storage persists
// network neighbor entries to etcd via this protobuf encoding; if
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
