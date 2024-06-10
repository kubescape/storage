package networkpolicy

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestGenerateNetworkPolicy(t *testing.T) {
	timeProvider := metav1.Now()
	protocolTCP := v1.ProtocolTCP

	tests := []struct {
		name                  string
		networkNeighborhood   softwarecomposition.NetworkNeighborhood
		knownServers          []softwarecomposition.KnownServer
		expectedNetworkPolicy softwarecomposition.GeneratedNetworkPolicy
		expectError           bool
	}{
		{
			name: "basic ingress rule",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "nginx",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "nginx",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "nginx",
										},
									},
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(80),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-nginx",
					Namespace:         "kubescape",
					Labels:            nil,
					CreationTimestamp: timeProvider,
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
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptrToInt32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
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
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
					},
				},
			},
			expectError: false,
		},
		{
			name: "network neighborhood not ready",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-nginx",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: "not-ready",
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "nginx",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{},
			expectError:           true,
		},
		{
			name: "network_policy_with_multiple_ports_and_labels",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-multi",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "multi",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app":  "multi-app",
							"tier": "frontend",
						},
						MatchExpressions: []metav1.LabelSelectorRequirement{
							{
								Key:      "environment",
								Operator: metav1.LabelSelectorOpIn,
								Values:   []string{"prod", "staging"},
							},
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8080)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-multi",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-multi",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app":  "multi-app",
								"tier": "frontend",
							},
							MatchExpressions: []metav1.LabelSelectorRequirement{
								{
									Key:      "environment",
									Operator: metav1.LabelSelectorOpIn,
									Values:   []string{"prod", "staging"},
								},
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(443)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.1/32",
										},
									},
								},
							},
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(8080)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "192.168.1.1/32",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
			},
		},
		{
			name: "policy_with_known_servers",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-known-servers",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "known-servers",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "known-app",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(80),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
						{
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(8080),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
					},
				},
			},
			knownServers: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							IPBlock: "10.0.0.0/8",
							Name:    "known-server-1",
							Server:  "server-1",
						},
						{
							IPBlock: "192.168.0.0/16",
							Name:    "known-server-2",
							Server:  "server-2",
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-known-servers",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-known-servers",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "known-app",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptrToInt32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.0/8",
										},
									},
								},
							},
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptrToInt32(8080),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "192.168.0.0/16",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						Name:       "known-server-1",
						OriginalIP: "10.0.0.1",
						IPBlock:    "10.0.0.0/8",
						Server:     "server-1",
					},
					{
						Name:       "known-server-2",
						OriginalIP: "192.168.1.1",
						IPBlock:    "192.168.0.0/16",
						Server:     "server-2",
					},
				},
			},
			expectError: false,
		},
		{
			name: "policy_with_known_servers",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-known-servers",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "known-servers",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "known-app",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(80),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
						{
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(8080),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
					},
				},
			},
			knownServers: []softwarecomposition.KnownServer{
				{
					Spec: softwarecomposition.KnownServerSpec{
						{
							IPBlock: "10.0.0.0/8",
							Name:    "known-server-1",
							Server:  "server-1",
						},
						{
							IPBlock: "192.168.0.0/16",
							Name:    "known-server-2",
							Server:  "server-2",
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-known-servers",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-known-servers",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "known-app",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptrToInt32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.0/8",
										},
									},
								},
							},
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptrToInt32(8080),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "192.168.0.0/16",
										},
									},
								},
							},
						},
					},
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						Name:       "known-server-1",
						OriginalIP: "10.0.0.1",
						IPBlock:    "10.0.0.0/8",
						Server:     "server-1",
					},
					{
						Name:       "known-server-2",
						OriginalIP: "192.168.1.1",
						IPBlock:    "192.168.0.0/16",
						Server:     "server-2",
					},
				},
			},
			expectError: false,
		},
		{
			name: "policy_with_dns_neighbors",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-dns",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "dns",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "dns-app",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									DNS:       "example.com",
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
				},
			},
			knownServers: []softwarecomposition.KnownServer{},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-dns",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				PoliciesRef: []softwarecomposition.PolicyRef{
					{
						DNS:        "example.com",
						IPBlock:    "192.168.1.1/32",
						OriginalIP: "192.168.1.1",
					},
				},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-dns",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "dns-app",
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
											CIDR: "192.168.1.1/32",
										},
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "network_policy_with_multiple_containers",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-multi-container",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "multi-container",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "multi-container",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "nginx",
										},
									},
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(80),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
						},
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									PodSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											"app": "nginx",
										},
									},
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptrToInt32(443),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-multi-container",
					Namespace:         "kubescape",
					Labels:            nil,
					CreationTimestamp: timeProvider,
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-multi-container",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "multi-container",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptrToInt32(80),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
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
										Port:     ptrToInt32(443),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
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
						Egress: []softwarecomposition.NetworkPolicyEgressRule{},
					},
				},
			},
			expectError: false,
		},
		{
			name: "network_policy_with_multiple_containers_with_same_ip",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-multi-containers",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "multi-containers",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "multi-container-app",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Name: "container-1",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8080)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
						{
							Name: "container-2",
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8080)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-multi-containers",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-multi-containers",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "multi-container-app",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(443)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.1/32",
										},
									},
								},
							},
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(8080)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "192.168.1.1/32",
										},
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "network_policy_with_multiple_different_containers",
			networkNeighborhood: softwarecomposition.NetworkNeighborhood{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "deployment-multi-containers",
					Namespace: "kubescape",
					Annotations: map[string]string{
						helpersv1.StatusMetadataKey: helpersv1.Ready,
					},
					Labels: map[string]string{
						helpersv1.KindMetadataKey: "Deployment",
						helpersv1.NameMetadataKey: "multi-containers",
					},
				},
				Spec: softwarecomposition.NetworkNeighborhoodSpec{
					LabelSelector: metav1.LabelSelector{
						MatchLabels: map[string]string{
							"app": "multi-container-app",
						},
					},
					Containers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8080)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.2",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8081)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8081",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.2",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8082)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8082",
										},
									},
								},
							},
						},
					},
					InitContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.3",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(90)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-90",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8080)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.2",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8081)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8081",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.2",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8082)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8082",
										},
									},
								},
							},
						},
					},
					EphemeralContainers: []softwarecomposition.NetworkNeighborhoodContainer{
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.4",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(80)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-80",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
								{
									IPAddress: "10.0.0.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(100)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-100",
										},
										{
											Port:     ptr.To(int32(443)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-443",
										},
									},
								},
							},
							Egress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "192.168.1.1",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8080)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8080",
										},
									},
								},
							},
						},
						{
							Ingress: []softwarecomposition.NetworkNeighbor{
								{
									IPAddress: "10.0.0.2",
									Ports: []softwarecomposition.NetworkPort{
										{
											Port:     ptr.To(int32(8081)),
											Protocol: softwarecomposition.ProtocolTCP,
											Name:     "TCP-8081",
										},
									},
								},
							},
						},
					},
				},
			},
			expectedNetworkPolicy: softwarecomposition.GeneratedNetworkPolicy{
				TypeMeta: metav1.TypeMeta{
					Kind:       "GeneratedNetworkPolicy",
					APIVersion: "spdx.softwarecomposition.kubescape.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "deployment-multi-containers",
					Namespace:         "kubescape",
					CreationTimestamp: timeProvider,
				},
				PoliciesRef: []softwarecomposition.PolicyRef{},
				Spec: softwarecomposition.NetworkPolicy{
					Kind:       "NetworkPolicy",
					APIVersion: "networking.k8s.io/v1",
					ObjectMeta: metav1.ObjectMeta{
						Name:      "deployment-multi-containers",
						Namespace: "kubescape",
						Annotations: map[string]string{
							"generated-by": "kubescape",
						},
					},
					Spec: softwarecomposition.NetworkPolicySpec{
						PodSelector: metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "multi-container-app",
							},
						},
						PolicyTypes: []softwarecomposition.PolicyType{
							softwarecomposition.PolicyTypeIngress,
							softwarecomposition.PolicyTypeEgress,
						},
						Ingress: []softwarecomposition.NetworkPolicyIngressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(80)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(90)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(100)),
										Protocol: &protocolTCP,
									},
									{
										Port:     ptr.To(int32(443)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.1/32",
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
									{
										Port:     ptr.To(int32(443)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.3/32",
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
									{
										Port:     ptr.To(int32(443)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.4/32",
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(8081)),
										Protocol: &protocolTCP,
									},
								},
								From: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "10.0.0.2/32",
										},
									},
								},
							},
						},
						Egress: []softwarecomposition.NetworkPolicyEgressRule{
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(8080)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "192.168.1.1/32",
										},
									},
								},
							},
							{
								Ports: []softwarecomposition.NetworkPolicyPort{
									{
										Port:     ptr.To(int32(8082)),
										Protocol: &protocolTCP,
									},
								},
								To: []softwarecomposition.NetworkPolicyPeer{
									{
										IPBlock: &softwarecomposition.IPBlock{
											CIDR: "192.168.1.2/32",
										},
									},
								},
							},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GenerateNetworkPolicy(&tt.networkNeighborhood, tt.knownServers, timeProvider)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Nil(t, compareNP(&tt.expectedNetworkPolicy, &got))
			}
		})
	}
}

func TestGetSingleIP(t *testing.T) {
	ipAddress := "192.168.1.1"
	expected := &softwarecomposition.IPBlock{CIDR: "192.168.1.1/32"}

	result := getSingleIP(ipAddress)

	if result.CIDR != expected.CIDR {
		t.Errorf("getSingleIP() = %v, want %v", result, expected)
	}
}
func TestRemoveLabels(t *testing.T) {
	labels := map[string]string{
		"app.kubernetes.io/name":     "value",
		"app.kubernetes.io/instance": "1234",
	}

	expected := map[string]string{
		"app.kubernetes.io/name": "value",
	}

	removeLabels(labels)

	if !reflect.DeepEqual(labels, expected) {
		t.Errorf("removeLabels() = %v, want %v", labels, expected)
	}
}

func TestMergeIngressRulesByPorts(t *testing.T) {
	protocolTCP := v1.ProtocolTCP

	tests := []struct {
		name     string
		rules    []softwarecomposition.NetworkPolicyIngressRule
		expected []softwarecomposition.NetworkPolicyIngressRule
	}{
		{
			name: "merge multiple rules with same ports and different IPs",
			rules: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
		},
		{
			name: "do not merge rules with different ports",
			rules: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(443),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(443),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
		},
		{
			name: "do not merge rules with selectors",
			rules: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
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
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
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
		},
		{
			name: "merge rules with no selectors",
			rules: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyIngressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					From: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := mergeIngressRulesByPorts(tt.rules)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func TestMergeEgressRulesByPorts(t *testing.T) {
	protocolTCP := v1.ProtocolTCP

	tests := []struct {
		name     string
		rules    []softwarecomposition.NetworkPolicyEgressRule
		expected []softwarecomposition.NetworkPolicyEgressRule
	}{
		{
			name: "merge multiple rules with same ports and different IPs",
			rules: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
		},
		{
			name: "do not merge rules with different ports",
			rules: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(443),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(443),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
		},
		{
			name: "do not merge rules with selectors",
			rules: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
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
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
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
			},
		},
		{
			name: "merge rules with no selectors",
			rules: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
					},
				},
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
			expected: []softwarecomposition.NetworkPolicyEgressRule{
				{
					Ports: []softwarecomposition.NetworkPolicyPort{
						{
							Port:     ptrToInt32(80),
							Protocol: &protocolTCP,
						},
					},
					To: []softwarecomposition.NetworkPolicyPeer{
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.1/32",
							},
						},
						{
							IPBlock: &softwarecomposition.IPBlock{
								CIDR: "10.0.0.2/32",
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := mergeEgressRulesByPorts(tt.rules)
			assert.Equal(t, tt.expected, actual)
		})
	}
}

func ptrToInt32(i int32) *int32 {
	return &i
}

// embed file

func compareNP(p1, p2 *softwarecomposition.GeneratedNetworkPolicy) error {
	if p1 == nil || p2 == nil {
		return fmt.Errorf("one of the policies is nil")
	}

	if err := compareEgress(p1.Spec.Spec.Egress, p1.Spec.Spec.Egress); err != nil {
		return fmt.Errorf("Spec is different. p1.Spec.Spec.Egress: %v, p2.Spec.Spec.Egress: %v", p1.Spec.Spec.Egress, p2.Spec.Spec.Egress)
	}
	if err := compareIngress(p1.Spec.Spec.Ingress, p1.Spec.Spec.Ingress); err != nil {
		return fmt.Errorf("Spec is different. p1.Spec.Spec.Ingress: %v, p2.Spec.Spec.Ingress: %v", p1.Spec.Spec.Ingress, p2.Spec.Spec.Ingress)
	}

	return nil
}

func toString(i interface{}) string {
	b, _ := json.Marshal(i)
	return string(b)
}

func compareIngress(a, b []softwarecomposition.NetworkPolicyIngressRule) error {
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

func compareEgress(a, b []softwarecomposition.NetworkPolicyEgressRule) error {
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
