package networkpolicy

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/stretchr/testify/assert"
)

//go:embed testdata/nn-operator.json
var networkNeighborhoodFile string

//go:embed testdata/np-operator.json
var networkPolicyFile string

//go:embed testdata/known-servers.json
var knownServersFile string

func TestGenerateNetworkPolicyFromFile(t *testing.T) {
	timeProvider := metav1.Now()

	networkNeighborhood := &v1beta1.NetworkNeighborhood{}
	knownServers := []v1beta1.KnownServer{}
	expectedNetworkPolicy := &v1beta1.GeneratedNetworkPolicy{}

	if err := json.Unmarshal([]byte(networkNeighborhoodFile), networkNeighborhood); err != nil {
		t.Fatalf("failed to unmarshal JSON data from file %s: %v", networkNeighborhoodFile, err)
	}
	if err := json.Unmarshal([]byte(knownServersFile), &knownServers); err != nil {
		t.Fatalf("failed to unmarshal JSON data from file %s: %v", networkNeighborhoodFile, err)
	}
	if err := json.Unmarshal([]byte(networkPolicyFile), expectedNetworkPolicy); err != nil {
		t.Fatalf("failed to unmarshal JSON data from file %s: %v", networkNeighborhoodFile, err)
	}
	// Generate the network policy
	generatedNetworkPolicy, err := GenerateNetworkPolicy(networkNeighborhood, knownServers, timeProvider)
	if err != nil {
		t.Fatalf("failed to generate network policy: %v", err)
	}

	// Compare the generated policy with the expected policy
	assert.Nil(t, compareNP(&generatedNetworkPolicy, expectedNetworkPolicy))
}

func compareNP(p1, p2 *v1beta1.GeneratedNetworkPolicy) error {
	if p1 == nil || p2 == nil {
		return fmt.Errorf("one of the policies is nil")
	}
	if !reflect.DeepEqual(p1.TypeMeta, p2.TypeMeta) {
		return fmt.Errorf("TypeMeta is different. p1.TypeMeta: %s, p2.TypeMeta: %s", toString(p1.TypeMeta), p2.TypeMeta)
	}
	p1.ObjectMeta.CreationTimestamp = metav1.Time{}
	p2.ObjectMeta.CreationTimestamp = metav1.Time{}
	if !reflect.DeepEqual(p1.ObjectMeta, p2.ObjectMeta) {
		return fmt.Errorf("ObjectMeta is different. p1.ObjectMeta: %s, p2.ObjectMeta: %s", toString(p1.ObjectMeta), toString(p2.ObjectMeta))
	}

	if !reflect.DeepEqual(p1.Spec.GetAnnotations(), p2.Spec.GetAnnotations()) {
		return fmt.Errorf("Spec is different. p1.Spec.GetAnnotations: %v, p2.Spec.GetAnnotations: %v", p1.Spec.GetAnnotations(), p2.Spec.GetAnnotations())
	}
	if !reflect.DeepEqual(p1.Spec.GetLabels(), p2.Spec.GetLabels()) {
		return fmt.Errorf("Spec is different. p1.Spec.GetLabels: %v, p2.Spec.GetLabels: %v", p1.Spec.GetLabels(), p2.Spec.GetLabels())
	}
	if !reflect.DeepEqual(p1.Spec.Name, p2.Spec.Name) {
		return fmt.Errorf("Spec is different. p1.Spec.Name: %v, p2.Spec.Name: %v", p1.Spec.Name, p2.Spec.Name)
	}
	if err := compareEgress(p1.Spec.Spec.Egress, p1.Spec.Spec.Egress); err != nil {
		return fmt.Errorf("Spec is different. p1.Spec.Spec.Egress: %v, p2.Spec.Spec.Egress: %v", p1.Spec.Spec.Egress, p2.Spec.Spec.Egress)
	}
	if err := compareIngress(p1.Spec.Spec.Ingress, p1.Spec.Spec.Ingress); err != nil {
		return fmt.Errorf("Spec is different. p1.Spec.Spec.Ingress: %v, p2.Spec.Spec.Ingress: %v", p1.Spec.Spec.Ingress, p2.Spec.Spec.Ingress)
	}

	if !reflect.DeepEqual(p1.Spec.Spec.PodSelector, p2.Spec.Spec.PodSelector) {
		return fmt.Errorf("Spec is different. p1.Spec.Spec.PodSelector: %v, p2.Spec.Spec.PodSelector: %v", p1.Spec.Spec.PodSelector, p2.Spec.Spec.PodSelector)
	}
	if !reflect.DeepEqual(p1.Spec.Spec.PolicyTypes, p2.Spec.Spec.PolicyTypes) {
		return fmt.Errorf("Spec is different. p1.Spec.Spec.PolicyTypes: %v, p2.Spec.Spec.PolicyTypes: %v", p1.Spec.Spec.PolicyTypes, p2.Spec.Spec.PolicyTypes)
	}
	return nil
}

func toString(i interface{}) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func compareIngress(a, b []v1beta1.NetworkPolicyIngressRule) error {
	if len(a) != len(b) {
		return fmt.Errorf("len(a) != len(b). len(a): %d, len(b): %d", len(a), len(b))
	}
	var al []string
	var bl []string
	for i := range a {
		al = append(al, toString(a[i]))
		bl = append(bl, toString(b[i]))
	}
	slices.Sort(al)
	slices.Sort(bl)
	if !reflect.DeepEqual(al, bl) {
		return fmt.Errorf("a != b. a: %v, b: %v", a, b)
	}
	return nil
}

func compareEgress(a, b []v1beta1.NetworkPolicyEgressRule) error {
	if len(a) != len(b) {
		return fmt.Errorf("len(a) != len(b). len(a): %d, len(b): %d", len(a), len(b))
	}
	var al []string
	var bl []string
	for i := range a {
		al = append(al, toString(a[i]))
		bl = append(bl, toString(b[i]))
	}
	slices.Sort(al)
	slices.Sort(bl)
	if !reflect.DeepEqual(al, bl) {
		return fmt.Errorf("a != b. a: %v, b: %v", a, b)
	}
	return nil
}
