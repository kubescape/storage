package softwarecomposition

import (
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateNetworkPolicy(t *testing.T) {
	timeProvider := metav1.Now()
	protocolTCP := v1.ProtocolTCP

	tests := []struct {
		name                  string
		networkNeighborhood   NetworkNeighborhood
		knownServers          []KnownServer
		expectedNetworkPolicy GeneratedNetworkPolicy
		expectError           bool
	}{
		{
			name: "basic ingress rule",
			networkNeighborhood: NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: NetworkNeighborhoodSpec{
					Containers: []NetworkNeighborhoodContainer{
						{
							Ingress: []NetworkNeighbor{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "nginx",
										},
									},
									Ports: []NetworkPort{
										{
											Port:     ptrToInt32(80),
											Protocol: ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.kubescape.io",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					Labels:            nil,
					CreationTimestamp: timeProvider,
				},
				PoliciesRef: []PolicyRef{},
				Spec: NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []PolicyType{
							PolicyTypeIngress,
							PolicyTypeEgress,
						},
						Ingress: []NetworkPolicyIngressRule{
							{
								Ports: []NetworkPolicyPort{
									{
										Port:     ptrToInt32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"app": "nginx",
											},
										},
									},
								},
							},
						},
						Egress: []NetworkPolicyEgressRule{},
					},
				},
			},
			expectError: false,
		},
		{
			name: "network neighborhood not ready",
			networkNeighborhood: NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: "not-ready",
					},
				},
				Spec: NetworkNeighborhoodSpec{},
			},
			expectedNetworkPolicy: GeneratedNetworkPolicy{},
			expectError:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateNetworkPolicy(tt.networkNeighborhood, tt.knownServers, timeProvider)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedNetworkPolicy, got)
			}
		})
	}
}

func ptrToInt32(i int32) *int32 {
	return &i
}
