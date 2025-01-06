package networkpolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"fmt"
	"net"
	"sort"
	"strings"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func GenerateNetworkPolicy(nn *softwarecomposition.NetworkNeighborhood, knownServers softwarecomposition.IKnownServersFinder, timeProvider metav1.Time) (softwarecomposition.GeneratedNetworkPolicy, error) {
	if !IsAvailable(nn) {
		return softwarecomposition.GeneratedNetworkPolicy{}, fmt.Errorf("nn %s/%s status annotation is not ready nor completed", nn.Namespace, nn.Name)
	}

	// get name from labels and clean labels
	kind, ok := nn.Labels[helpersv1.KindMetadataKey]
	if !ok {
		return softwarecomposition.GeneratedNetworkPolicy{}, fmt.Errorf("nn %s/%s does not have a kind label", nn.Namespace, nn.Name)
	}
	name, ok := nn.Labels[helpersv1.NameMetadataKey]
	if !ok {
		logger.L().Debug("nn does not have a workload-name label, falling back to nn.Name", helpers.String("name", nn.Name), helpers.String("namespace", nn.Namespace))
		name = nn.Name
	}
	delete(nn.Labels, helpersv1.TemplateHashKey)

	networkPolicy := softwarecomposition.NetworkPolicy{
		Kind:       "NetworkPolicy",
		APIVersion: "networking.k8s.io/v1",
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", strings.ToLower(kind), name),
			Namespace: nn.Namespace,
			Annotations: map[string]string{
				"generated-by": "kubescape",
			},
			Labels: nn.Labels,
		},
		Spec: softwarecomposition.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []softwarecomposition.PolicyType{
				softwarecomposition.PolicyTypeIngress,
				softwarecomposition.PolicyTypeEgress,
			},
		},
	}

	if nn.Spec.MatchLabels != nil {
		networkPolicy.Spec.PodSelector.MatchLabels = nn.Spec.MatchLabels
	}

	if nn.Spec.MatchExpressions != nil {
		networkPolicy.Spec.PodSelector.MatchExpressions = nn.Spec.MatchExpressions
	}

	generatedNetworkPolicy := softwarecomposition.GeneratedNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GeneratedNetworkPolicy",
			APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              nn.Name,
			Namespace:         nn.Namespace,
			Labels:            nn.Labels,
			CreationTimestamp: timeProvider,
		},
		PoliciesRef: []softwarecomposition.PolicyRef{},
	}

	ingressHash := make(map[string]bool)
	for _, neighbor := range listIngressNetworkNeighbors(nn) {

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
	for _, neighbor := range listEgressNetworkNeighbors(nn) {

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

func listIngressNetworkNeighbors(nn *softwarecomposition.NetworkNeighborhood) []softwarecomposition.NetworkNeighbor {
	var neighbors []softwarecomposition.NetworkNeighbor
	for i := range nn.Spec.Containers {
		neighbors = append(neighbors, nn.Spec.Containers[i].Ingress...)
	}
	for i := range nn.Spec.InitContainers {
		neighbors = append(neighbors, nn.Spec.InitContainers[i].Ingress...)
	}
	for i := range nn.Spec.EphemeralContainers {
		neighbors = append(neighbors, nn.Spec.EphemeralContainers[i].Ingress...)
	}
	return neighbors

}

func listEgressNetworkNeighbors(nn *softwarecomposition.NetworkNeighborhood) []softwarecomposition.NetworkNeighbor {
	var neighbors []softwarecomposition.NetworkNeighbor
	for i := range nn.Spec.Containers {
		neighbors = append(neighbors, nn.Spec.Containers[i].Egress...)
	}
	for i := range nn.Spec.InitContainers {
		neighbors = append(neighbors, nn.Spec.InitContainers[i].Egress...)
	}
	for i := range nn.Spec.EphemeralContainers {
		neighbors = append(neighbors, nn.Spec.EphemeralContainers[i].Egress...)
	}
	return neighbors

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
			return false // Keep the order as is if softwarecomposition.IPBlock is nil
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

type PortProtocolKey struct {
	Port     int32
	Protocol v1.Protocol
}

// NewPortProtocolKey creates a new PortProtocolKey from a softwarecomposition.NetworkPolicyPort
// It ensures nil values are handled correctly (i.e. 0 for port and TCP for protocol)
func NewPortProtocolKey(port softwarecomposition.NetworkPolicyPort) PortProtocolKey {
	num := int32(0)
	if port.Port != nil {
		num = *port.Port
	}
	proto := v1.ProtocolTCP
	if port.Protocol != nil {
		proto = *port.Protocol
	}
	return PortProtocolKey{Port: num, Protocol: proto}
}

func mergeEgressRulesByPorts(rules []softwarecomposition.NetworkPolicyEgressRule) []softwarecomposition.NetworkPolicyEgressRule {

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
			key := NewPortProtocolKey(port)
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
			return false // Keep the order as is if softwarecomposition.IPBlock is nil
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
			egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
				NamespaceSelector: neighbor.NamespaceSelector,
			})
		}
	}

	if neighbor.IPAddress != "" {
		// look if this IP is part of any known server
		if entries, contains := knownServers.Contains(net.ParseIP(neighbor.IPAddress)); contains {
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
			ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
				NamespaceSelector: neighbor.NamespaceSelector,
			})
		}
	}

	if neighbor.IPAddress != "" {
		// look if this IP is part of any known server
		if entries, ok := knownServers.Contains(net.ParseIP(neighbor.IPAddress)); ok {
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

func getSingleIP(ipAddress string) *softwarecomposition.IPBlock {
	ipBlock := &softwarecomposition.IPBlock{CIDR: ipAddress + "/32"}
	return ipBlock
}

func removeLabels(labels map[string]string) {
	for key := range labels {
		if networkpolicy.IsIgnoredLabel(key) {
			delete(labels, key)
		}
	}
}

func IsAvailable(nn *softwarecomposition.NetworkNeighborhood) bool {
	switch nn.GetAnnotations()[helpersv1.StatusMetadataKey] {
	case helpersv1.Ready, helpersv1.Completed:
		return true
	default:
		return false
	}
}

func hash(s any) (string, error) {

	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(s); err != nil {
		return "", err
	}
	vv := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(vv[:]), nil
}
