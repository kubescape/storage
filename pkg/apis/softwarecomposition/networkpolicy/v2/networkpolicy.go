package networkpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy"
	"github.com/kubescape/storage/pkg/registry/file/networkmatch"

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
	kind, ok := nn.Labels[helpersv1.RelatedKindMetadataKey]
	if !ok {
		return softwarecomposition.GeneratedNetworkPolicy{}, fmt.Errorf("nn %s/%s does not have a kind label", nn.Namespace, nn.Name)
	}
	name, ok := nn.Labels[helpersv1.RelatedNameMetadataKey]
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
	ingressPolicyRefsHash := make(map[string]bool)
	for _, neighbor := range listIngressNetworkNeighbors(nn) {

		rule, policyRefs := generateIngressRule(neighbor, knownServers)

		if ruleHash, err := hash(rule); err == nil {
			if ok := ingressHash[ruleHash]; !ok {
				networkPolicy.Spec.Ingress = append(networkPolicy.Spec.Ingress, rule)
				ingressHash[ruleHash] = true
			}
		}

		if refsHash, err := hash(policyRefs); err == nil {
			if ok := ingressPolicyRefsHash[refsHash]; !ok {
				generatedNetworkPolicy.PoliciesRef = append(generatedNetworkPolicy.PoliciesRef, policyRefs...)
				ingressPolicyRefsHash[refsHash] = true
			}
		}

	}

	egressHash := make(map[string]bool)
	egressPolicyRefsHash := make(map[string]bool)
	for _, neighbor := range listEgressNetworkNeighbors(nn) {

		rule, policyRefs := generateEgressRule(neighbor, knownServers)

		if ruleHash, err := hash(rule); err == nil {
			if ok := egressHash[ruleHash]; !ok {
				networkPolicy.Spec.Egress = append(networkPolicy.Spec.Egress, rule)
				egressHash[ruleHash] = true
			}
		}

		if refsHash, err := hash(policyRefs); err == nil {
			if ok := egressPolicyRefsHash[refsHash]; !ok {
				generatedNetworkPolicy.PoliciesRef = append(generatedNetworkPolicy.PoliciesRef, policyRefs...)
				egressPolicyRefsHash[refsHash] = true
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

// containsIPBlockPeer reports whether peers already contains an entry with the given CIDR.
// Used to avoid duplicate peer entries when merging rules that reference the same IP
// from multiple distinct NetworkNeighbor entries (e.g. the same peer seen across containers).
func containsIPBlockPeer(peers []softwarecomposition.NetworkPolicyPeer, cidr string) bool {
	for _, existing := range peers {
		if existing.IPBlock != nil && existing.IPBlock.CIDR == cidr {
			return true
		}
	}
	return false
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
				if peer.IPBlock == nil {
					continue
				}
				if !containsIPBlockPeer(merged[key], peer.IPBlock.CIDR) {
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
				if peer.IPBlock == nil {
					continue
				}
				if !containsIPBlockPeer(merged[key], peer.IPBlock.CIDR) {
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

	skipPorts := false
	if len(neighbor.IPAddresses) > 0 {
		peers, refs := buildIPAddressesPeers(neighbor.IPAddresses, neighbor.DNS, knownServers)
		egressRule.To = append(egressRule.To, peers...)
		policyRefs = append(policyRefs, refs...)
		if len(peers) == 0 && neighbor.PodSelector == nil && neighbor.NamespaceSelector == nil {
			skipPorts = true
		}
	} else if neighbor.IPAddress != "" {
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

	if skipPorts {
		return egressRule, policyRefs
	}

	portMap := make(map[PortProtocolKey]bool)
	for _, networkPort := range neighbor.Ports {
		protocol := v1.Protocol(strings.ToUpper(string(networkPort.Protocol)))
		portInt32 := networkPort.Port

		key := PortProtocolKey{Port: *portInt32, Protocol: protocol}
		if !portMap[key] {
			egressRule.Ports = append(egressRule.Ports, softwarecomposition.NetworkPolicyPort{
				Protocol: &protocol,
				Port:     portInt32,
			})
			portMap[key] = true
		}
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

	skipPorts := false
	if len(neighbor.IPAddresses) > 0 {
		peers, refs := buildIPAddressesPeers(neighbor.IPAddresses, neighbor.DNS, knownServers)
		ingressRule.From = append(ingressRule.From, peers...)
		policyRefs = append(policyRefs, refs...)
		if len(peers) == 0 && neighbor.PodSelector == nil && neighbor.NamespaceSelector == nil {
			skipPorts = true
		}
	} else if neighbor.IPAddress != "" {
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

	if skipPorts {
		return ingressRule, policyRefs
	}

	portMap := make(map[PortProtocolKey]bool)
	for _, networkPort := range neighbor.Ports {
		protocol := v1.Protocol(strings.ToUpper(string(networkPort.Protocol)))
		portInt32 := networkPort.Port

		key := PortProtocolKey{Port: *portInt32, Protocol: protocol}
		if !portMap[key] {
			ingressRule.Ports = append(ingressRule.Ports, softwarecomposition.NetworkPolicyPort{
				Protocol: &protocol,
				Port:     portInt32,
			})
			portMap[key] = true
		}
	}

	return ingressRule, policyRefs
}

// buildIPAddressesPeers builds NetworkPolicyPeer/PolicyRef pairs from the plural
// NetworkNeighbor.IPAddresses field, shared by generateEgressRule/generateIngressRule.
func buildIPAddressesPeers(ipAddresses []string, dns string, knownServers softwarecomposition.IKnownServersFinder) ([]softwarecomposition.NetworkPolicyPeer, []softwarecomposition.PolicyRef) {
	var peers []softwarecomposition.NetworkPolicyPeer
	var policyRefs []softwarecomposition.PolicyRef

	for _, entry := range ipAddresses {
		if prefix, err := netip.ParsePrefix(entry); err == nil {
			if !prefix.Addr().Is4() {
				continue // IPv6 CIDR, out of scope (AC9)
			}
			peers = append(peers, softwarecomposition.NetworkPolicyPeer{
				IPBlock: &softwarecomposition.IPBlock{CIDR: entry},
			})
			if dns != "" {
				// no single original IP for a CIDR range
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					DNS:        dns,
					IPBlock:    entry,
					OriginalIP: "",
				})
			}
			continue
		}

		if entry == networkmatch.AnyIPSentinel {
			const anyCIDR = "0.0.0.0/0"
			peers = append(peers, softwarecomposition.NetworkPolicyPeer{
				IPBlock: &softwarecomposition.IPBlock{CIDR: anyCIDR},
			})
			if dns != "" {
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					DNS:        dns,
					IPBlock:    anyCIDR,
					OriginalIP: "",
				})
			}
			continue
		}

		addr, err := netip.ParseAddr(entry)
		if err != nil || !addr.Is4() {
			continue // IPv6 or unparseable, out of scope (AC9)
		}

		// bare IPv4: mirror the singular IPAddress path exactly, including known-server enrichment
		if entries, contains := knownServers.Contains(net.ParseIP(entry)); contains {
			for _, ks := range entries {
				peers = append(peers, softwarecomposition.NetworkPolicyPeer{
					IPBlock: &softwarecomposition.IPBlock{CIDR: ks.GetIPBlock()},
				})

				policyRef := softwarecomposition.PolicyRef{
					Name:       ks.GetName(),
					OriginalIP: entry,
					IPBlock:    ks.GetIPBlock(),
					Server:     ks.GetServer(),
				}
				if dns != "" {
					policyRef.DNS = dns
				}
				policyRefs = append(policyRefs, policyRef)
			}
		} else {
			ipBlock := getSingleIP(entry)
			peers = append(peers, softwarecomposition.NetworkPolicyPeer{IPBlock: ipBlock})

			if dns != "" {
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					DNS:        dns,
					IPBlock:    ipBlock.CIDR,
					OriginalIP: entry,
				})
			}
		}
	}

	return peers, policyRefs
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
	if nn.GetAnnotations()[helpersv1.ManagedByMetadataKey] == helpersv1.ManagedByUserValue {
		return true
	}
	switch nn.GetAnnotations()[helpersv1.StatusMetadataKey] {
	case helpersv1.Learning, helpersv1.Completed:
		return true
	default:
		return false
	}
}

// hash must be a deterministic function of s's contents: gob's map encoding follows Go's
// randomized map iteration order, so gob-encoding a struct containing a map (e.g. a
// LabelSelector's MatchLabels) previously produced a different byte sequence - and thus a
// different hash - across otherwise-identical calls. json.Marshal sorts map keys, giving a
// stable encoding regardless of map iteration order.
func hash(s any) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	vv := sha256.Sum256(b)
	return hex.EncodeToString(vv[:]), nil
}
