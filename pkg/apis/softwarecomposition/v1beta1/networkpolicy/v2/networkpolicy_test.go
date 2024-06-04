package networkpolicy

import (
	_ "embed"
	"encoding/json"
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

	b, _ := json.Marshal(generatedNetworkPolicy)
	t.Fatal(string(b))
	// Compare the generated policy with the expected policy
	assert.Equal(t, expectedNetworkPolicy, generatedNetworkPolicy)
}
