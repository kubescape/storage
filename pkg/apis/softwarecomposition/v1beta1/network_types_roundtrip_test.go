package v1beta1

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func serviceFieldsNeighbor() NetworkNeighbor {
	return NetworkNeighbor{
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
		Ports:  []NetworkPort{{Name: "TCP-9093", Protocol: "TCP", Port: ptr(int32(9093))}},
	}
}

// TestNetworkNeighbor_ServiceFields_JSONRoundtrip pins the JSON contract for
// the serviceRef/serviceSelector/entity fields: values survive a
// Marshal→Unmarshal cycle exactly, and a neighbor NOT using them serializes
// without the keys at all — profiles written before this feature must not grow
// noise fields on rewrite, and a dropped key silently reverts an allowlist.
func TestNetworkNeighbor_ServiceFields_JSONRoundtrip(t *testing.T) {
	original := serviceFieldsNeighbor()
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded NetworkNeighbor
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(decoded, original) {
		t.Errorf("JSON roundtrip mismatch:\n  got:  %+v\n  want: %+v", decoded, original)
	}

	plain, err := json.Marshal(NetworkNeighbor{Identifier: "id", Type: "external"})
	if err != nil {
		t.Fatalf("Marshal(plain): %v", err)
	}
	for _, key := range []string{"serviceRefNamespace", "serviceRefName", "serviceSelector", "entity"} {
		if strings.Contains(string(plain), key) {
			t.Errorf("omitempty violated: unset %q serialized in %s", key, plain)
		}
	}
}

// TestNetworkNeighbor_ServiceFields_ConversionRoundtrip pins that the
// generated internal<->v1beta1 conversion carries all four new fields both
// ways. The conversion sits on every apiserver read/write path; a regenerated
// zz_generated.conversion.go that misses a field would silently strip it from
// stored profiles.
func TestNetworkNeighbor_ServiceFields_ConversionRoundtrip(t *testing.T) {
	original := serviceFieldsNeighbor()

	var internal softwarecomposition.NetworkNeighbor
	if err := Convert_v1beta1_NetworkNeighbor_To_softwarecomposition_NetworkNeighbor(&original, &internal, nil); err != nil {
		t.Fatalf("to internal: %v", err)
	}
	if internal.ServiceRefNamespace != original.ServiceRefNamespace ||
		internal.ServiceRefName != original.ServiceRefName ||
		internal.Entity != original.Entity ||
		!reflect.DeepEqual(internal.ServiceSelector, original.ServiceSelector) {
		t.Errorf("internal conversion dropped a field:\n  got:  %+v", internal)
	}

	var back NetworkNeighbor
	if err := Convert_softwarecomposition_NetworkNeighbor_To_v1beta1_NetworkNeighbor(&internal, &back, nil); err != nil {
		t.Fatalf("to v1beta1: %v", err)
	}
	if !reflect.DeepEqual(back, original) {
		t.Errorf("conversion roundtrip mismatch:\n  got:  %+v\n  want: %+v", back, original)
	}
}

// TestNetworkNeighbor_ServiceSelector_DeepCopyIsDeep guards against the
// classic pointer-sharing bug in generated deepcopy: if the copy aliased the
// original's ServiceSelector, mutating one profile's selector would corrupt
// every cached copy of it. Checked for both API versions since each has its
// own generated deepcopy.
func TestNetworkNeighbor_ServiceSelector_DeepCopyIsDeep(t *testing.T) {
	t.Run("v1beta1", func(t *testing.T) {
		original := serviceFieldsNeighbor()
		cp := original.DeepCopy()
		cp.ServiceSelector.MatchLabels["app"] = "tampered"
		cp.ServiceSelector.MatchExpressions[0].Values[0] = "tampered"
		if original.ServiceSelector.MatchLabels["app"] != "guestbook" {
			t.Error("DeepCopy shares MatchLabels with the original")
		}
		if original.ServiceSelector.MatchExpressions[0].Values[0] != "frontend" {
			t.Error("DeepCopy shares MatchExpressions with the original")
		}
	})
	t.Run("internal", func(t *testing.T) {
		var original softwarecomposition.NetworkNeighbor
		v1 := serviceFieldsNeighbor()
		if err := Convert_v1beta1_NetworkNeighbor_To_softwarecomposition_NetworkNeighbor(&v1, &original, nil); err != nil {
			t.Fatal(err)
		}
		// The generated conversion aliases pointers via unsafe.Pointer by
		// design; DeepCopy first so the mutation below cannot reach v1.
		original = *original.DeepCopy()
		cp := original.DeepCopy()
		cp.ServiceSelector.MatchLabels["app"] = "tampered"
		cp.ServiceSelector.MatchExpressions[0].Values[0] = "tampered"
		if original.ServiceSelector.MatchLabels["app"] != "guestbook" {
			t.Error("DeepCopy shares MatchLabels with the original")
		}
		if original.ServiceSelector.MatchExpressions[0].Values[0] != "frontend" {
			t.Error("DeepCopy shares MatchExpressions with the original")
		}
	})
}
