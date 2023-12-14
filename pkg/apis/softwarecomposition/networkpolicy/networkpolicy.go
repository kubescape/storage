package networkpolicy

import (
	"net"
	"sort"
	"strings"

	"golang.org/x/exp/maps"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	storageV1Beta1ApiVersion = "spdx.softwarecomposition.kubescape.io/v1beta1"
)

func GenerateNetworkPolicy(networkNeighbors softwarecomposition.NetworkNeighbors, knownServers []softwarecomposition.KnownServer, timeProvider metav1.Time) (softwarecomposition.GeneratedNetworkPolicy, error) {
	networkPolicy := softwarecomposition.NetworkPolicy{
		Kind:       "NetworkPolicy",
		APIVersion: "networking.k8s.io/v1",
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkNeighbors.Name,
			Namespace: networkNeighbors.Namespace,
			Annotations: map[string]string{
				"generated-by": "kubescape",
			},
			Labels: networkNeighbors.Labels,
		},
	}

	if networkNeighbors.Spec.MatchLabels != nil {
		networkPolicy.Spec.PodSelector.MatchLabels = maps.Clone(networkNeighbors.Spec.MatchLabels)
	}

	if networkNeighbors.Spec.MatchExpressions != nil {
		networkPolicy.Spec.PodSelector.MatchExpressions = networkNeighbors.Spec.MatchExpressions
	}

	if len(networkNeighbors.Spec.Ingress) > 0 {
		networkPolicy.Spec.PolicyTypes = append(networkPolicy.Spec.PolicyTypes, "Ingress")
	}

	if len(networkNeighbors.Spec.Egress) > 0 {
		networkPolicy.Spec.PolicyTypes = append(networkPolicy.Spec.PolicyTypes, "Egress")
	}

	generatedNetworkPolicy := softwarecomposition.GeneratedNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GeneratedNetworkPolicy",
			APIVersion: storageV1Beta1ApiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              networkNeighbors.Name,
			Namespace:         networkNeighbors.Namespace,
			Labels:            networkNeighbors.Labels,
			CreationTimestamp: timeProvider,
		},
		PoliciesRef: []softwarecomposition.PolicyRef{},
	}

	for _, neighbor := range networkNeighbors.Spec.Ingress {

		ingressRules, policyRefs := generateIngressRule(neighbor, knownServers)

		generatedNetworkPolicy.PoliciesRef = append(generatedNetworkPolicy.PoliciesRef, policyRefs...)

		networkPolicy.Spec.Ingress = append(networkPolicy.Spec.Ingress, ingressRules)

	}

	for _, neighbor := range networkNeighbors.Spec.Egress {

		egressRules, policyRefs := generateEgressRule(neighbor, knownServers)

		generatedNetworkPolicy.PoliciesRef = append(generatedNetworkPolicy.PoliciesRef, policyRefs...)

		networkPolicy.Spec.Egress = append(networkPolicy.Spec.Egress, egressRules)

	}

	networkPolicy.Spec.Egress = mergeEgressRulesByPorts(networkPolicy.Spec.Egress)

	networkPolicy.Spec.Ingress = mergeIngressRulesByPorts(networkPolicy.Spec.Ingress)

	generatedNetworkPolicy.Spec = networkPolicy

	return generatedNetworkPolicy, nil
}

func mergeIngressRulesByPorts(rules []softwarecomposition.NetworkPolicyIngressRule) []softwarecomposition.NetworkPolicyIngressRule {
	type PortProtocolKey struct {
		Port     int32
		Protocol v1.Protocol
	}

	merged := make(map[PortProtocolKey][]softwarecomposition.NetworkPolicyPeer)
	var keys []PortProtocolKey
	var nonMergedRules []softwarecomposition.NetworkPolicyIngressRule

	for _, rule := range rules {
		hasSelector := false
		for _, peer := range rule.From {
			if peer.PodSelector != nil || peer.NamespaceSelector != nil {
				hasSelector = true
				break
			}
		}

		if hasSelector {
			nonMergedRules = append(nonMergedRules, rule)
			continue
		}

		for _, port := range rule.Ports {
			key := PortProtocolKey{Port: *port.Port, Protocol: *port.Protocol}
			if _, exists := merged[key]; !exists {
				keys = append(keys, key)
			}
			for _, peer := range rule.From {
				if peer.IPBlock != nil {
					merged[key] = append(merged[key], peer)
				}
			}
		}
	}

	// Sort the keys
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Port != keys[j].Port {
			return keys[i].Port < keys[j].Port
		}
		return keys[i].Protocol < keys[j].Protocol
	})

	// Construct merged rules using sorted keys
	var mergedRules []softwarecomposition.NetworkPolicyIngressRule
	for i := range keys {
		peers := merged[keys[i]]
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].IPBlock != nil && peers[j].IPBlock != nil {
				return peers[i].IPBlock.CIDR < peers[j].IPBlock.CIDR
			}
			return false // Keep the order as is if IPBlock is nil
		})

		mergedRules = append(mergedRules, softwarecomposition.NetworkPolicyIngressRule{
			Ports: []softwarecomposition.NetworkPolicyPort{{Protocol: &keys[i].Protocol, Port: &keys[i].Port}},
			From:  peers,
		})
	}

	// Combine merged and non-merged rules
	mergedRules = append(mergedRules, nonMergedRules...)

	return mergedRules
}

func mergeEgressRulesByPorts(rules []softwarecomposition.NetworkPolicyEgressRule) []softwarecomposition.NetworkPolicyEgressRule {
	type PortProtocolKey struct {
		Port     int32
		Protocol v1.Protocol
	}

	merged := make(map[PortProtocolKey][]softwarecomposition.NetworkPolicyPeer)
	var keys []PortProtocolKey
	var nonMergedRules []softwarecomposition.NetworkPolicyEgressRule

	for _, rule := range rules {
		hasSelector := false
		for _, peer := range rule.To {
			if peer.PodSelector != nil || peer.NamespaceSelector != nil {
				hasSelector = true
				break
			}
		}

		if hasSelector {
			nonMergedRules = append(nonMergedRules, rule)
			continue
		}

		for _, port := range rule.Ports {
			key := PortProtocolKey{Port: *port.Port, Protocol: *port.Protocol}
			if _, exists := merged[key]; !exists {
				keys = append(keys, key)
			}
			for _, peer := range rule.To {
				if peer.IPBlock != nil {
					merged[key] = append(merged[key], peer)
				}
			}
		}
	}

	// Sort the keys
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Port != keys[j].Port {
			return keys[i].Port < keys[j].Port
		}
		return keys[i].Protocol < keys[j].Protocol
	})

	// Construct merged rules using sorted keys
	var mergedRules []softwarecomposition.NetworkPolicyEgressRule
	for i := range keys {
		peers := merged[keys[i]]
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].IPBlock != nil && peers[j].IPBlock != nil {
				return peers[i].IPBlock.CIDR < peers[j].IPBlock.CIDR
			}
			return false // Keep the order as is if IPBlock is nil
		})

		mergedRules = append(mergedRules, softwarecomposition.NetworkPolicyEgressRule{
			Ports: []softwarecomposition.NetworkPolicyPort{{Protocol: &keys[i].Protocol, Port: &keys[i].Port}},
			To:    peers,
		})
	}

	// Combine merged and non-merged rules
	mergedRules = append(mergedRules, nonMergedRules...)

	return mergedRules
}

func generateEgressRule(neighbor softwarecomposition.NetworkNeighbor, KnownServer []softwarecomposition.KnownServer) (softwarecomposition.NetworkPolicyEgressRule, []softwarecomposition.PolicyRef) {
	egressRule := softwarecomposition.NetworkPolicyEgressRule{}
	policyRefs := []softwarecomposition.PolicyRef{}

	if neighbor.PodSelector != nil {
		egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
			PodSelector: neighbor.PodSelector,
		})
	}

	if neighbor.NamespaceSelector != nil {
		// the ns label goes together with the pod label
		if len(egressRule.To) > 0 {
			egressRule.To[0].NamespaceSelector = neighbor.NamespaceSelector
		} else {
			// TOD0(DanielGrunberegerCA): is this a valid case?
			egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
				NamespaceSelector: neighbor.NamespaceSelector,
			})
		}
	}

	if neighbor.IPAddress != "" {
		isKnownServer := false
		// look if this IP is part of any known server
		for _, knownServer := range KnownServer {
			for _, entry := range knownServer.Spec {
				_, subNet, err := net.ParseCIDR(entry.IPBlock)
				if err != nil {
					logger.L().Error("error parsing cidr", helpers.Error(err))
					continue
				}
				if subNet.Contains(net.ParseIP(neighbor.IPAddress)) {
					egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
						IPBlock: &softwarecomposition.IPBlock{
							CIDR: entry.IPBlock,
						},
					})
					isKnownServer = true

					policyRef := softwarecomposition.PolicyRef{
						Name:       entry.Name,
						OriginalIP: neighbor.IPAddress,
						IPBlock:    entry.IPBlock,
						Server:     entry.Server,
					}

					if neighbor.DNS != "" {
						policyRef.DNS = neighbor.DNS
					}

					policyRefs = append(policyRefs, policyRef)
					break
				}
			}
		}

		if !isKnownServer {
			ipBlock := getSingleIP(neighbor.IPAddress)
			egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
				IPBlock: ipBlock,
			})

			if neighbor.DNS != "" {
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					DNS:        neighbor.DNS,
					IPBlock:    ipBlock.CIDR,
					OriginalIP: neighbor.IPAddress,
				})
			}
		}
	}

	for _, networkPort := range neighbor.Ports {
		protocol := v1.Protocol(strings.ToUpper(string(networkPort.Protocol)))
		portInt32 := networkPort.Port

		egressRule.Ports = append(egressRule.Ports, softwarecomposition.NetworkPolicyPort{
			Protocol: &protocol,
			Port:     portInt32,
		})
	}

	return egressRule, policyRefs
}

func generateIngressRule(neighbor softwarecomposition.NetworkNeighbor, KnownServer []softwarecomposition.KnownServer) (softwarecomposition.NetworkPolicyIngressRule, []softwarecomposition.PolicyRef) {
	ingressRule := softwarecomposition.NetworkPolicyIngressRule{}
	policyRefs := []softwarecomposition.PolicyRef{}

	if neighbor.PodSelector != nil {
		ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
			PodSelector: neighbor.PodSelector,
		})
	}
	if neighbor.NamespaceSelector != nil {
		// the ns label goes together with the pod label
		if len(ingressRule.From) > 0 {
			ingressRule.From[0].NamespaceSelector = neighbor.NamespaceSelector
		} else {
			// TOD0(DanielGrunberegerCA): is this a valid case?
			ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
				NamespaceSelector: neighbor.NamespaceSelector,
			})
		}
	}

	if neighbor.IPAddress != "" {
		isKnownServer := false
		// look if this IP is part of any known server
		for _, knownServer := range KnownServer {
			for _, entry := range knownServer.Spec {
				_, subNet, err := net.ParseCIDR(entry.IPBlock)
				if err != nil {
					logger.L().Error("error parsing cidr", helpers.Error(err))
					continue
				}
				if subNet.Contains(net.ParseIP(neighbor.IPAddress)) {
					ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
						IPBlock: &softwarecomposition.IPBlock{
							CIDR: entry.IPBlock,
						},
					})
					isKnownServer = true

					policyRef := softwarecomposition.PolicyRef{
						Name:       entry.Name,
						OriginalIP: neighbor.IPAddress,
						IPBlock:    entry.IPBlock,
						Server:     entry.Server,
					}

					if neighbor.DNS != "" {
						policyRef.DNS = neighbor.DNS
					}

					policyRefs = append(policyRefs, policyRef)
					break
				}
			}
		}

		if !isKnownServer {
			ipBlock := getSingleIP(neighbor.IPAddress)
			ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
				IPBlock: ipBlock,
			})

			if neighbor.DNS != "" {
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					DNS:        neighbor.DNS,
					IPBlock:    ipBlock.CIDR,
					OriginalIP: neighbor.IPAddress,
				})
			}
		}
	}

	for _, networkPort := range neighbor.Ports {
		protocol := v1.Protocol(strings.ToUpper(string(networkPort.Protocol)))
		portInt32 := networkPort.Port

		ingressRule.Ports = append(ingressRule.Ports, softwarecomposition.NetworkPolicyPort{
			Protocol: &protocol,
			Port:     portInt32,
		})
	}

	return ingressRule, policyRefs
}

func getSingleIP(ipAddress string) *softwarecomposition.IPBlock {
	ipBlock := &softwarecomposition.IPBlock{CIDR: ipAddress + "/32"}
	return ipBlock
}
