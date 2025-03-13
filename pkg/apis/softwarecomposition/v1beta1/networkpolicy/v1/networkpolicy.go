package networkpolicy

import (
	sc "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	np "github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy/v1"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	storageV1Beta1ApiVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
)

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func GenerateNetworkPolicy(networkNeighbors v1beta1.NetworkNeighbors, knownServers sc.IKnownServersFinder, timeProvider metav1.Time) (v1beta1.GeneratedNetworkPolicy, error) {
	networkNeighborsV1, err := convertNetworkNeighbors(&networkNeighbors)
	if err != nil {
		return v1beta1.GeneratedNetworkPolicy{}, err
	}

	npv1, err := np.GenerateNetworkPolicy(networkNeighborsV1, knownServers, timeProvider)
	if err != nil {
		return v1beta1.GeneratedNetworkPolicy{}, err
	}

	return convertGeneratedNetworkPolicy(&npv1)

}

func convertGeneratedNetworkPolicy(old *sc.GeneratedNetworkPolicy) (v1beta1.GeneratedNetworkPolicy, error) {
	npv1beta1 := v1beta1.GeneratedNetworkPolicy{}
	if err := v1beta1.Convert_softwarecomposition_GeneratedNetworkPolicy_To_v1beta1_GeneratedNetworkPolicy(old, &npv1beta1, nil); err != nil {
		return v1beta1.GeneratedNetworkPolicy{}, err
	}
	npv1beta1.TypeMeta.APIVersion = storageV1Beta1ApiVersion
	npv1beta1.TypeMeta.Kind = "GeneratedNetworkPolicy"
	return npv1beta1, nil
}

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func convertNetworkNeighbors(old *v1beta1.NetworkNeighbors) (sc.NetworkNeighbors, error) {
	neighbors := sc.NetworkNeighbors{}
	err := v1beta1.Convert_v1beta1_NetworkNeighbors_To_softwarecomposition_NetworkNeighbors(old, &neighbors, nil)
	return neighbors, err
}
func convertKnownServersList(old []v1beta1.KnownServer) ([]sc.KnownServer, error) {
	var servers []sc.KnownServer
	for i := range old {
		k := sc.KnownServer{}
		err := v1beta1.Convert_v1beta1_KnownServer_To_softwarecomposition_KnownServer(&old[i], &k, nil)
		if err != nil {
			return nil, err
		}
		servers = append(servers, k)
	}
	return servers, nil
}
