package softwarecomposition

import (
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// The chart's excludeNamespaces is a controller-side workload filter; it has no
// representation on the peer API. A NetworkNeighbor scopes namespaces only by
// label (NamespaceSelector) or by naming one Service (ServiceRefNamespace) —
// there is no hard namespace-denylist field a peer could carry. This guards
// that surface so a future scalar namespace field is a deliberate, reviewed add.
func TestNetworkNeighbor_NoHardNamespaceExceptServiceRef(t *testing.T) {
	tp := reflect.TypeOf(NetworkNeighbor{})

	var scalarNamespaceFields []string
	for i := 0; i < tp.NumField(); i++ {
		f := tp.Field(i)
		if f.Type.Kind() == reflect.String && strings.Contains(f.Name, "Namespace") {
			scalarNamespaceFields = append(scalarNamespaceFields, f.Name)
		}
	}
	if !reflect.DeepEqual(scalarNamespaceFields, []string{"ServiceRefNamespace"}) {
		t.Errorf("the only hard-namespace scalar on a peer must be ServiceRefNamespace, got %v", scalarNamespaceFields)
	}

	labelSel := reflect.TypeOf((*metav1.LabelSelector)(nil))
	for _, name := range []string{"PodSelector", "NamespaceSelector", "ServiceSelector"} {
		f, ok := tp.FieldByName(name)
		if !ok {
			t.Fatalf("expected selector field %s to exist", name)
		}
		if f.Type != labelSel {
			t.Errorf("%s must be *metav1.LabelSelector (namespace scoping is label-based), got %s", name, f.Type)
		}
	}

	if f, ok := tp.FieldByName("Entity"); !ok || f.Type.Kind() != reflect.String {
		t.Error("Entity must remain a namespaceless string peer identity")
	}
}

// A peer's namespace-relevant fields survive DeepCopy unchanged — the
// serialized policy is what the resolver later reads, independent of any
// excludeNamespaces the controller applies to sources.
func TestNetworkNeighbor_SelectorFieldsRoundTrip(t *testing.T) {
	in := &NetworkNeighbor{
		Identifier:          "peer-1",
		Type:                CommunicationTypeEgress,
		ServiceRefNamespace: "kube-system",
		ServiceRefName:      "kube-dns",
		ServiceSelector:     &metav1.LabelSelector{MatchLabels: map[string]string{"probe": "yes"}},
		NamespaceSelector:   &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "honey"}},
		Entity:              "host",
	}
	out := in.DeepCopy()
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("DeepCopy diverged:\n in=%+v\nout=%+v", in, out)
	}
	out.ServiceSelector.MatchLabels["probe"] = "no"
	if in.ServiceSelector.MatchLabels["probe"] != "yes" {
		t.Error("DeepCopy must not alias the ServiceSelector map")
	}
}
