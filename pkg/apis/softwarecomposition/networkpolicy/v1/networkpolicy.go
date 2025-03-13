package networkpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"maps"
	"net"
	"sort"
	"strings"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func GenerateNetworkPolicy(networkNeighbors softwarecomposition.NetworkNeighbors, knownServers softwarecomposition.IKnownServersFinder, timeProvider metav1.Time) (softwarecomposition.GeneratedNetworkPolicy, error) {
	if !IsAvailable(networkNeighbors) {
		return softwarecomposition.GeneratedNetworkPolicy{}, fmt.Errorf("networkNeighbors %s/%s status annotation is not ready nor completed", networkNeighbors.Namespace, networkNeighbors.Name)
	}

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
		Spec: softwarecomposition.NetworkPolicySpec{
			PolicyTypes: []softwarecomposition.PolicyType{
				softwarecomposition.PolicyTypeIngress,
				softwarecomposition.PolicyTypeEgress,
			},
		},
	}

	if networkNeighbors.Spec.MatchLabels != nil {
		networkPolicy.Spec.PodSelector.MatchLabels = maps.Clone(networkNeighbors.Spec.MatchLabels)
	}

	if networkNeighbors.Spec.MatchExpressions != nil {
		networkPolicy.Spec.PodSelector.MatchExpressions = networkNeighbors.Spec.MatchExpressions
	}

	generatedNetworkPolicy := softwarecomposition.GeneratedNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GeneratedNetworkPolicy",
			APIVersion: softwarecomposition.GroupName,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              networkNeighbors.Name,
			Namespace:         networkNeighbors.Namespace,
			Labels:            networkNeighbors.Labels,
			CreationTimestamp: timeProvider,
		},
		PoliciesRef: []softwarecomposition.PolicyRef{},
	}

	ingressHash := make(map[string]bool)
	for _, neighbor := range networkNeighbors.Spec.Ingress {

		rule, policyRefs := generateIngressRule(neighbor, knownServers)

		if ruleHash, err := hash(rule); err == nil {
			if ok := ingressHash[ruleHash]; !ok {
				networkPolicy.Spec.Ingress = append(networkPolicy.Spec.Ingress, rule)
				ingressHash[ruleHash] = true
			}
		}

		if refsHash, err := hash(policyRefs); err == nil {
			if ok := ingressHash[refsHash]; !ok {
				generatedNetworkPolicy.PoliciesRef = append(generatedNetworkPolicy.PoliciesRef, policyRefs...)
				ingressHash[refsHash] = true
			}
		}

	}

	egressHash := make(map[string]bool)
	for _, neighbor := range networkNeighbors.Spec.Egress {

		rule, policyRefs := generateEgressRule(neighbor, knownServers)

		if ruleHash, err := hash(rule); err == nil {
			if ok := egressHash[ruleHash]; !ok {
				networkPolicy.Spec.Egress = append(networkPolicy.Spec.Egress, rule)
				egressHash[ruleHash] = true
			}
		}

		for i := range policyRefs {
			if refsHash, err := hash(policyRefs[i]); err == nil {
				if ok := egressHash[refsHash]; !ok {
					generatedNetworkPolicy.PoliciesRef = append(generatedNetworkPolicy.PoliciesRef, policyRefs[i])
					egressHash[refsHash] = true
				}
			}
		}
	}

	networkPolicy.Spec.Egress = mergeEgressRulesByPorts(networkPolicy.Spec.Egress)

	networkPolicy.Spec.Ingress = mergeIngressRulesByPorts(networkPolicy.Spec.Ingress)

	generatedNetworkPolicy.Spec = networkPolicy

	return generatedNetworkPolicy, nil
}

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
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
			if port.Port == nil || port.Protocol == nil {
				continue
			}
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
	mergedRules := []softwarecomposition.NetworkPolicyIngressRule{}
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

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
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
			key := PortProtocolKey{}
			if port.Port != nil {
				key.Port = *port.Port
			}
			if port.Protocol != nil {
				key.Protocol = *port.Protocol
			}
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
	mergedRules := []softwarecomposition.NetworkPolicyEgressRule{}
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

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func generateEgressRule(neighbor softwarecomposition.NetworkNeighbor, knownServers softwarecomposition.IKnownServersFinder) (softwarecomposition.NetworkPolicyEgressRule, []softwarecomposition.PolicyRef) {
	egressRule := softwarecomposition.NetworkPolicyEgressRule{}
	policyRefs := []softwarecomposition.PolicyRef{}

	if neighbor.PodSelector != nil {
		removeLabels(neighbor.PodSelector.MatchLabels)
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
		if entries, contains := knownServers.Contains(net.ParseIP(neighbor.IPAddress)); contains {
			// look if this IP is part of any known server
			for _, entry := range entries {

				egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
					IPBlock: &softwarecomposition.IPBlock{
						CIDR: entry.GetIPBlock(),
					},
				})

				policyRef := softwarecomposition.PolicyRef{
					Name:       entry.GetName(),
					OriginalIP: neighbor.IPAddress,
					IPBlock:    entry.GetIPBlock(),
					Server:     entry.GetServer(),
				}

				if neighbor.DNS != "" {
					policyRef.DNS = neighbor.DNS
				}

				policyRefs = append(policyRefs, policyRef)

			}

		} else {
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

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func hash(s any) (string, error) {

	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(s); err != nil {
		return "", err
	}
	vv := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(vv[:]), nil
}

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func generateIngressRule(neighbor softwarecomposition.NetworkNeighbor, knownServers softwarecomposition.IKnownServersFinder) (softwarecomposition.NetworkPolicyIngressRule, []softwarecomposition.PolicyRef) {
	ingressRule := softwarecomposition.NetworkPolicyIngressRule{}
	policyRefs := []softwarecomposition.PolicyRef{}

	if neighbor.PodSelector != nil {
		removeLabels(neighbor.PodSelector.MatchLabels)
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
		// look if this IP is part of any known server
		if entries, contains := knownServers.Contains(net.ParseIP(neighbor.IPAddress)); contains {
			for _, entry := range entries {
				ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
					IPBlock: &softwarecomposition.IPBlock{
						CIDR: entry.GetIPBlock(),
					},
				})

				policyRef := softwarecomposition.PolicyRef{
					Name:       entry.GetName(),
					OriginalIP: neighbor.IPAddress,
					IPBlock:    entry.GetIPBlock(),
					Server:     entry.GetServer(),
				}

				if neighbor.DNS != "" {
					policyRef.DNS = neighbor.DNS
				}

				policyRefs = append(policyRefs, policyRef)

			}
		} else {
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

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func getSingleIP(ipAddress string) *softwarecomposition.IPBlock {
	ipBlock := &softwarecomposition.IPBlock{CIDR: ipAddress + "/32"}
	return ipBlock
}

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func removeLabels(labels map[string]string) {
	for key := range labels {
		if networkpolicy.IsIgnoredLabel(key) {
			delete(labels, key)
		}
	}
}

// Deprecated: Use v2 instead. This version relies on deprecated functionality.
func IsAvailable(networkNeighbors softwarecomposition.NetworkNeighbors) bool {
	switch networkNeighbors.GetAnnotations()[helpersv1.StatusMetadataKey] {
	case helpersv1.Ready, helpersv1.Completed:
		return true
	default:
		return false
	}
}
