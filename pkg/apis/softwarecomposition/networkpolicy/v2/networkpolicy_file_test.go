package networkpolicy

import (
	_ "embed"
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	softwarecompositionv1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// cp-operator.json is the ContainerProfile-shaped migration of the pre-migration
// nn-operator.json NetworkNeighborhood fixture: the single container's ingress /
// egress moved into spec.ingress / spec.egress, the neighborhood selector into the
// spec's embedded label selector, and the container-name label / type annotation set.
//
//go:embed testdata/cp-operator.json
var containerProfileFile string

// np-operator.json and np.new.json are the pre-migration golden GeneratedNetworkPolicy
// outputs, kept UNCHANGED. They are the diff-oracle: generating directly from the
// ContainerProfile must reproduce the exact network content that the removed
// NetworkNeighborhood intermediate produced.
//
//go:embed testdata/np-operator.json
var networkPolicyFile string

//go:embed testdata/np.new.json
var networkPolicyNewFile string

//go:embed testdata/known-servers.json
var knownServersFile string

// TestGenerateNetworkPolicyFromFile is the migrated diff-oracle for the removed
// NetworkNeighborhood generation path. It loads the migrated ContainerProfile
// fixture, generates a policy directly from it, and asserts the generated
// ingress/egress rules match the pre-migration golden outputs (compared after the
// generator's own deterministic sort, exactly as the pre-migration file test did).
func TestGenerateNetworkPolicyFromFile(t *testing.T) {
	timeProvider := metav1.Now()

	// The fixture is the served v1beta1 shape (matching the goldens and a real
	// ContainerProfile CRD). Unmarshal it as v1beta1, then convert to the
	// internal type the generator consumes — exactly as storage does on the read
	// path. (Unmarshalling directly into the internal type would drop metadata:
	// its embedded ObjectMeta carries no `json:"metadata"` tag.)
	v1beta1Profile := &softwarecompositionv1beta1.ContainerProfile{}
	if err := json.Unmarshal([]byte(containerProfileFile), v1beta1Profile); err != nil {
		t.Fatalf("failed to unmarshal container profile fixture: %v", err)
	}
	containerProfile := &softwarecomposition.ContainerProfile{}
	if err := softwarecompositionv1beta1.Convert_v1beta1_ContainerProfile_To_softwarecomposition_ContainerProfile(v1beta1Profile, containerProfile, nil); err != nil {
		t.Fatalf("failed to convert container profile fixture to internal type: %v", err)
	}
	knownServers := []softwarecomposition.KnownServer{}

	if err := json.Unmarshal([]byte(knownServersFile), &knownServers); err != nil {
		t.Fatalf("failed to unmarshal known servers fixture: %v", err)
	}

	generatedNetworkPolicy, err := GenerateNetworkPolicy(containerProfile, softwarecomposition.NewKnownServersFinderImpl(knownServers), timeProvider)
	if err != nil {
		t.Fatalf("failed to generate network policy: %v", err)
	}

	// The generated policy must reproduce the pre-migration golden output.
	for name, golden := range map[string]string{
		"np-operator.json": networkPolicyFile,
		"np.new.json":      networkPolicyNewFile,
	} {
		t.Run(name, func(t *testing.T) {
			expected := &softwarecomposition.GeneratedNetworkPolicy{}
			if err := json.Unmarshal([]byte(golden), expected); err != nil {
				t.Fatalf("failed to unmarshal golden %s: %v", name, err)
			}
			assert.Nil(t, compareNP(&generatedNetworkPolicy, expected), "generated policy diverged from golden %s", name)
		})
	}
}
