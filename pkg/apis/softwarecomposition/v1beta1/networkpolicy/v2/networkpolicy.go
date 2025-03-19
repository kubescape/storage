package networkpolicy

import (
	sc "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	np "github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy/v2"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	storageV1Beta1ApiVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
)

func GenerateNetworkPolicy(networkNeighborhood *v1beta1.NetworkNeighborhood, knownServersFinder sc.IKnownServersFinder, timeProvider metav1.Time, actionGUID string) (v1beta1.GeneratedNetworkPolicy, error) {
	networkNeighborhoodV1, err := convertNetworkNeighborhood(networkNeighborhood)
	if err != nil {
		return v1beta1.GeneratedNetworkPolicy{}, err
	}

	npv1, err := np.GenerateNetworkPolicy(networkNeighborhoodV1, knownServersFinder, timeProvider, actionGUID)
	if err != nil {
		return v1beta1.GeneratedNetworkPolicy{}, err
	}

	generatedNetworkPolicy, conversionErr := convertGeneratedNetworkPolicy(&npv1)
	if conversionErr != nil {
		return v1beta1.GeneratedNetworkPolicy{}, conversionErr
	}

	return generatedNetworkPolicy, nil
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

func convertNetworkNeighborhood(old *v1beta1.NetworkNeighborhood) (*sc.NetworkNeighborhood, error) {
	neighbors := &sc.NetworkNeighborhood{}
	err := v1beta1.Convert_v1beta1_NetworkNeighborhood_To_softwarecomposition_NetworkNeighborhood(old, neighbors, nil)
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
