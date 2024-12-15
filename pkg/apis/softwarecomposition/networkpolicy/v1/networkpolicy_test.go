package networkpolicy

import (
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"

	softwarecomposition "github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGenerateNetworkPolicy(t *testing.T) {
	timeProvider := v1.Now()
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"two": "2",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"two": "2",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
								{
									Port:     ptr.To(int32(50)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
								{
									Port:     ptr.To(int32(40)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(50)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(40)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
								{
									Port:     ptr.To(int32(50)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
								{
									Port:     ptr.To(int32(40)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(50)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(40)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"one": "1",
									"two": "2",
								},
							},
							NamespaceSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"ns": "ns",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
											MatchLabels: map[string]string{
												"one": "1",
												"two": "2",
											},
										},
										NamespaceSelector: &v1.LabelSelector{
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "154.53.46.32",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchExpressions: []v1.LabelSelectorRequirement{
									{
										Key:      "one",
										Operator: v1.LabelSelectorOpIn,
										Values: []string{
											"1",
										},
									},
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
								{
									Port:     ptr.To(int32(50)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(50)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
											MatchExpressions: []v1.LabelSelectorRequirement{
												{
													Key:      "one",
													Operator: v1.LabelSelectorOpIn,
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "174.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(50)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-50",
								},
							},
						},
						{
							IPAddress: "156.43.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(50)),
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
										Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
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
									Port:     ptr.To(int32(80)),
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
									Port:     ptr.To(int32(90)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
										Port:     ptr.To(int32(90)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
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
									Port:     ptr.To(int32(80)),
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
									Port:     ptr.To(int32(90)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
										Port:     ptr.To(int32(90)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
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
									Port:     ptr.To(int32(80)),
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
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
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
									Port:     ptr.To(int32(80)),
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
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "196.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Completed,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "nginx",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							PodSelector: &v1.LabelSelector{
								MatchLabels: map[string]string{
									"app": "redis",
								},
							},
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										PodSelector: &v1.LabelSelector{
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Completed,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Egress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(443)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "196.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "196.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(443)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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
										Port:     ptr.To(int32(443)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Completed,
					},
				},
				Spec: softwarecomposition.NetworkNeighborsSpec{
					LabelSelector: v1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Ingress: []softwarecomposition.NetworkNeighbor{
						{
							IPAddress: "172.17.0.1",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
									Protocol: softwarecomposition.ProtocolTCP,
									Name:     "TCP-80",
								},
							},
						},
						{
							IPAddress: "172.17.0.2",
							Ports: []softwarecomposition.NetworkPort{
								{
									Port:     ptr.To(int32(80)),
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
				ObjectMeta: v1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				TypeMeta: v1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io",
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: v1.ObjectMeta{
						Name:      "deployment-nginx",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: v1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "nginx",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
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

		got, err := GenerateNetworkPolicy(test.networkNeighbors, softwarecomposition.NewKnownServersFinderImpl(test.KnownServer), timeProvider)

		assert.NoError(t, err)

		assert.Equal(t, test.expectedNetworkPolicy, got, test.name)
	}
}
