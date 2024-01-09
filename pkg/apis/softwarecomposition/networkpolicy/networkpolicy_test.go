package networkpolicy

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestGenerateNetworkPolicy(t *testing.T) {
	timeProvider := metav1.Now()
	protocolTCP := corev1.ProtocolTCP
	tests := []struct {
		name                  string
		networkNeighbors      softwarecomposition.NetworkNeighbors
		KnownServer           []softwarecomposition.KnownServer
		expectedNetworkPolicy softwarecomposition.GeneratedNetworkPolicy
	}{
		{
			name: "same port on different entries - one entry per workload",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"two": "2",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"one": "1",
											},
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"two": "2",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "same port on different entries - one entry per workload egress",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"two": "2",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"one": "1",
											},
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"two": "2",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple ports on same entry - ports aggregated under one entry",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
								{
									Port:     pointer.Int32(50),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
								{
									Port:     pointer.Int32(40),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-40",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
									{
										Port:     pointer.Int32(50),
										Protocol: &protocolTCP,
									},
									{
										Port:     pointer.Int32(40),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"one": "1",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple ports on same entry - ports aggregated under one entry egress",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
								{
									Port:     pointer.Int32(50),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
								{
									Port:     pointer.Int32(40),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-40",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
									{
										Port:     pointer.Int32(50),
										Protocol: &protocolTCP,
									},
									{
										Port:     pointer.Int32(40),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"one": "1",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "entry with namespace and multiple pod selectors - all labels are added together",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
									"two": "2",
								},
							},
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"ns": "ns",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"one": "1",
												"two": "2",
											},
										},
										NamespaceSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"ns": "ns",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "entry with raw IP and empty known servers - IPBlock is IP/32",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "154.53.46.32",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "154.53.46.32/32",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "matchExpressions as labels - labels are saved correctly",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{
										Key:      "one",
										Operator: metav1.LabelSelectorOpIn,
										Values: []string{
											"1",
										},
									},
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
								{
									Port:     pointer.Int32(50),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				PoliciesRef: []softwarecomposition.PolicyRef{},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
									{
										Port:     pointer.Int32(50),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchExpressions: []metav1.LabelSelectorRequirement{
												{
													Key:      "one",
													Operator: metav1.LabelSelectorOpIn,
													Values: []string{
														"1",
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "IP in known server  - policy is enriched",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			KnownServer: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							IPBlock: "172.17.0.0/16",
							Name:    "test",
							Server:  ""},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.0/16",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.2",
						DNS:        "",
						Name:       "test",
					},
				},
			},
		},
		{
			name: "multiple IPs in known servers  - policy is enriched",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "174.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(50),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
							},
						},
						{
							IPAddress: "156.43.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			KnownServer: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							IPBlock: "172.17.0.0/16",
							Name:    "name1",
							Server:  "",
						},
					}},
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							IPBlock: "174.17.0.0/16",
							Name:    "name2",
							Server:  "",
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(50),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "174.17.0.0/16",
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "156.43.0.2/32",
										},
									},
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.0/16",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.2",
						DNS:        "",
						Name:       "name1",
					},
					{
						IPBlock:    "174.17.0.0/16",
						OriginalIP: "174.17.0.2",
						DNS:        "",
						Name:       "name2",
					},
				},
			},
		},
		{
			name: "dns in network neighbor  - policy is enriched",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							DNS:       "test.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "198.17.0.2",
							DNS:       "stripe.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(90),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.2/32",
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(90),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "198.17.0.2/32",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.2/32",
						OriginalIP: "172.17.0.2",
						DNS:        "test.com",
					},
					{
						IPBlock:    "198.17.0.2/32",
						OriginalIP: "198.17.0.2",
						DNS:        "stripe.com",
					},
				},
			},
		},
		{
			name: "dns and known servers   - policy is enriched",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							DNS:       "test.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "198.17.0.2",
							DNS:       "stripe.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(90),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			KnownServer: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							Name:    "test",
							Server:  "test-server",
							IPBlock: "172.17.0.0/16",
						},
					}},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.0/16",
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(90),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "198.17.0.2/32",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.2",
						DNS:        "test.com",
						Name:       "test",
						Server:     "test-server",
					},
					{
						IPBlock:    "198.17.0.2/32",
						OriginalIP: "198.17.0.2",
						DNS:        "stripe.com",
					},
				},
			},
		},
		{
			name: "dns and known servers   - policy is enriched for egress",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							DNS:       "test.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "198.17.0.2",
							DNS:       "stripe.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			KnownServer: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							Name:    "test",
							Server:  "test-server",
							IPBlock: "172.17.0.0/16",
						},
					}},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.0/16",
										},
									},
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "198.17.0.2/32",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.2",
						DNS:        "test.com",
						Name:       "test",
						Server:     "test-server",
					},
					{
						IPBlock:    "198.17.0.2/32",
						OriginalIP: "198.17.0.2",
						DNS:        "stripe.com",
					},
				},
			},
		},
		{
			name: "multiple known servers   - policy is enriched for egress",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							DNS:       "test.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "198.17.0.2",
							DNS:       "stripe.com",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			KnownServer: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							Name:    "test",
							Server:  "test-server",
							IPBlock: "172.17.0.0/16",
						},
						{
							Name:    "stripe",
							Server:  "stripe-payments",
							IPBlock: "198.17.0.0/16",
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.0/16",
										},
									},
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "198.17.0.0/16",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.2",
						DNS:        "test.com",
						Name:       "test",
						Server:     "test-server",
					},
					{
						IPBlock:    "198.17.0.0/16",
						OriginalIP: "198.17.0.2",
						DNS:        "stripe.com",
						Name:       "stripe",
						Server:     "stripe-payments",
					},
				},
			},
		},
		{
			name: "same ports with different addresses - addresses are merged",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "196.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.2/32",
										},
									},
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "196.17.0.2/32",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "same ports for pod traffic",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "nginx",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							PodSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "redis",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"app": "nginx",
											},
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &metav1.LabelSelector{
											MatchLabels: map[string]string{
												"app": "redis",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "same ports for multiple IPs - addresses are merged correctly",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(443),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "196.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "196.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(443),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.2/32",
										},
									},
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "196.17.0.2/32",
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(443),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.2/32",
										},
									},
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "196.17.0.2/32",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple IPs in known servers  - policy is enriched",
			networkNeighbors: softwarecomposition.NetworkNeighbors{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.1",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     pointer.Int32(80),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			KnownServer: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							IPBlock: "172.17.0.0/16",
							Name:    "name-172.17.0.0",
							Server:  "name.server",
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     pointer.Int32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "172.17.0.0/16",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.1",
						Name:       "name-172.17.0.0",
						Server:     "name.server",
					},
					{
						IPBlock:    "172.17.0.0/16",
						OriginalIP: "172.17.0.2",
						Name:       "name-172.17.0.0",
						Server:     "name.server",
					},
				},
			},
		},
	}

	for _, test := range tests {

		got, err := GenerateNetworkPolicy(test.networkNeighbors, test.KnownServer, timeProvider)

		assert.NoError(t, err)

		assert.Equal(t, test.expectedNetworkPolicy, got, test.name)
	}
}
