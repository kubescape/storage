package softwarecomposition

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

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	storageV1ApiVersion = "spdx.kubescape.io"
)

func (nn *NetworkNeighborhood) GenerateNetworkPolicy(knownServers []KnownServer, timeProvider metav1.Time) (GeneratedNetworkPolicy, error) {
	if !nn.IsAvailable() {
		return GeneratedNetworkPolicy{}, fmt.Errorf("nn %s/%s status annotation is not ready", nn.Namespace, nn.Name)
	}

	networkPolicy := NetworkPolicy{
		Kind:       "NetworkPolicy",
		APIVersion: "networking.k8s.io/v1",
		ObjectMeta: metav1.ObjectMeta{
			Name:      nn.Name,
			Namespace: nn.Namespace,
			Annotations: map[string]string{
				"generated-by": "kubescape",
			},
			Labels: nn.Labels,
		},
		Spec: NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []PolicyType{
				PolicyTypeIngress,
				PolicyTypeEgress,
			},
		},
	}

	if nn.Spec.MatchLabels != nil {
		networkPolicy.Spec.PodSelector.MatchLabels = nn.Spec.MatchLabels
	}

	if nn.Spec.MatchExpressions != nil {
		networkPolicy.Spec.PodSelector.MatchExpressions = nn.Spec.MatchExpressions
	}

	generatedNetworkPolicy := GeneratedNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "GeneratedNetworkPolicy",
			APIVersion: storageV1ApiVersion,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              nn.Name,
			Namespace:         nn.Namespace,
			Labels:            nn.Labels,
			CreationTimestamp: timeProvider,
		},
		PoliciesRef: []PolicyRef{},
	}

	ingressHash := make(map[string]bool)
	for _, neighbor := range nn.listIngressNetworkNeighbors() {

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
	for _, neighbor := range nn.listEgressNetworkNeighbors() {

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

func (nn *NetworkNeighborhood) listIngressNetworkNeighbors() []NetworkNeighbor {
	var neighbors []NetworkNeighbor
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

func (nn *NetworkNeighborhood) listEgressNetworkNeighbors() []NetworkNeighbor {
	var neighbors []NetworkNeighbor
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

func mergeIngressRulesByPorts(rules []NetworkPolicyIngressRule) []NetworkPolicyIngressRule {
	type PortProtocolKey struct {
		Port     int32
		Protocol v1.Protocol
	}

	merged := make(map[PortProtocolKey][]NetworkPolicyPeer)
	var keys []PortProtocolKey
	var nonMergedRules []NetworkPolicyIngressRule

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
	mergedRules := []NetworkPolicyIngressRule{}
	for i := range keys {
		peers := merged[keys[i]]
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].IPBlock != nil && peers[j].IPBlock != nil {
				return peers[i].IPBlock.CIDR < peers[j].IPBlock.CIDR
			}
			return false // Keep the order as is if IPBlock is nil
		})

		mergedRules = append(mergedRules, NetworkPolicyIngressRule{
			Ports: []NetworkPolicyPort{{Protocol: &keys[i].Protocol, Port: &keys[i].Port}},
			From:  peers,
		})
	}

	// Combine merged and non-merged rules
	mergedRules = append(mergedRules, nonMergedRules...)

	return mergedRules
}

func mergeEgressRulesByPorts(rules []NetworkPolicyEgressRule) []NetworkPolicyEgressRule {
	type PortProtocolKey struct {
		Port     int32
		Protocol v1.Protocol
	}

	merged := make(map[PortProtocolKey][]NetworkPolicyPeer)
	var keys []PortProtocolKey
	var nonMergedRules []NetworkPolicyEgressRule

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
	mergedRules := []NetworkPolicyEgressRule{}
	for i := range keys {
		peers := merged[keys[i]]
		sort.Slice(peers, func(i, j int) bool {
			if peers[i].IPBlock != nil && peers[j].IPBlock != nil {
				return peers[i].IPBlock.CIDR < peers[j].IPBlock.CIDR
			}
			return false // Keep the order as is if IPBlock is nil
		})

		mergedRules = append(mergedRules, NetworkPolicyEgressRule{
			Ports: []NetworkPolicyPort{{Protocol: &keys[i].Protocol, Port: &keys[i].Port}},
			To:    peers,
		})
	}

	// Combine merged and non-merged rules
	mergedRules = append(mergedRules, nonMergedRules...)

	return mergedRules
}

func generateEgressRule(neighbor NetworkNeighbor, knownServers []KnownServer) (NetworkPolicyEgressRule, []PolicyRef) {
	egressRule := NetworkPolicyEgressRule{}
	policyRefs := []PolicyRef{}

	if neighbor.PodSelector != nil {
		removeLabels(neighbor.PodSelector.MatchLabels)
		egressRule.To = append(egressRule.To, NetworkPolicyPeer{
			PodSelector: neighbor.PodSelector,
		})
	}

	if neighbor.NamespaceSelector != nil {
		// the ns label goes together with the pod label
		if len(egressRule.To) > 0 {
			egressRule.To[0].NamespaceSelector = neighbor.NamespaceSelector
		} else {
			egressRule.To = append(egressRule.To, NetworkPolicyPeer{
				NamespaceSelector: neighbor.NamespaceSelector,
			})
		}
	}

	if neighbor.IPAddress != "" {
		isKnownServer := false
		// look if this IP is part of any known server
		for _, knownServer := range knownServers {
			for _, entry := range knownServer.Spec {
				_, subNet, err := net.ParseCIDR(entry.IPBlock)
				if err != nil {
					logger.L().Error("error parsing cidr", helpers.Error(err))
					continue
				}
				if subNet.Contains(net.ParseIP(neighbor.IPAddress)) {
					egressRule.To = append(egressRule.To, NetworkPolicyPeer{
						IPBlock: &IPBlock{
							CIDR: entry.IPBlock,
						},
					})
					isKnownServer = true

					policyRef := PolicyRef{
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
			egressRule.To = append(egressRule.To, NetworkPolicyPeer{
				IPBlock: ipBlock,
			})

			if neighbor.DNS != "" {
				policyRefs = append(policyRefs, PolicyRef{
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

		egressRule.Ports = append(egressRule.Ports, NetworkPolicyPort{
			Protocol: &protocol,
			Port:     portInt32,
		})
	}

	return egressRule, policyRefs
}

func generateIngressRule(neighbor NetworkNeighbor, knownServers []KnownServer) (NetworkPolicyIngressRule, []PolicyRef) {
	ingressRule := NetworkPolicyIngressRule{}
	policyRefs := []PolicyRef{}

	if neighbor.PodSelector != nil {
		removeLabels(neighbor.PodSelector.MatchLabels)
		ingressRule.From = append(ingressRule.From, NetworkPolicyPeer{
			PodSelector: neighbor.PodSelector,
		})
	}
	if neighbor.NamespaceSelector != nil {
		// the ns label goes together with the pod label
		if len(ingressRule.From) > 0 {
			ingressRule.From[0].NamespaceSelector = neighbor.NamespaceSelector
		} else {
			ingressRule.From = append(ingressRule.From, NetworkPolicyPeer{
				NamespaceSelector: neighbor.NamespaceSelector,
			})
		}
	}

	if neighbor.IPAddress != "" {
		isKnownServer := false
		// look if this IP is part of any known server
		for _, knownServer := range knownServers {
			for _, entry := range knownServer.Spec {
				_, subNet, err := net.ParseCIDR(entry.IPBlock)
				if err != nil {
					logger.L().Error("error parsing cidr", helpers.Error(err))
					continue
				}
				if subNet.Contains(net.ParseIP(neighbor.IPAddress)) {
					ingressRule.From = append(ingressRule.From, NetworkPolicyPeer{
						IPBlock: &IPBlock{
							CIDR: entry.IPBlock,
						},
					})
					isKnownServer = true

					policyRef := PolicyRef{
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
			ingressRule.From = append(ingressRule.From, NetworkPolicyPeer{
				IPBlock: ipBlock,
			})

			if neighbor.DNS != "" {
				policyRefs = append(policyRefs, PolicyRef{
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

		ingressRule.Ports = append(ingressRule.Ports, NetworkPolicyPort{
			Protocol: &protocol,
			Port:     portInt32,
		})
	}

	return ingressRule, policyRefs
}

func getSingleIP(ipAddress string) *IPBlock {
	ipBlock := &IPBlock{CIDR: ipAddress + "/32"}
	return ipBlock
}

func removeLabels(labels map[string]string) {
	for key := range labels {
		if isIgnoredLabel(key) {
			delete(labels, key)
		}
	}
}

func (nn *NetworkNeighborhood) IsAvailable() bool {
	switch nn.GetAnnotations()[helpersv1.StatusMetadataKey] {
	case helpersv1.Ready, helpersv1.Completed:
		return true
	default:
		return false
	}
}

var ignoreLabels map[string]bool

func init() {

	ignoreLabels = map[string]bool{
		"app.kubernetes.io/name":                      false,
		"app.kubernetes.io/part-of":                   false,
		"app.kubernetes.io/component":                 false,
		"app.kubernetes.io/instance":                  true,
		"app.kubernetes.io/version":                   true,
		"app.kubernetes.io/managed-by":                true,
		"app.kubernetes.io/created-by":                true,
		"app.kubernetes.io/owner":                     true,
		"app.kubernetes.io/revision":                  true,
		"statefulset.kubernetes.io/pod-name":          true,
		"scheduler.alpha.kubernetes.io/node-selector": true,
		"pod-template-hash":                           true,
		"controller-revision-hash":                    true,
		"pod-template-generation":                     true,
		"helm.sh/chart":                               true,
	}
}

// IsIgnoredLabel returns true if the label is ignored
func isIgnoredLabel(label string) bool {
	return ignoreLabels[label]
}

func hash(s any) (string, error) {

	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(s); err != nil {
		return "", err
	}
	vv := sha256.Sum256(b.Bytes())
	return hex.EncodeToString(vv[:]), nil
}
