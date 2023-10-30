package file

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

const (
	networkNeighborsResource = "networkneighborses"
)

// GeneratedNetworkPolicyStorage offers a storage solution for GeneratedNetworkPolicy objects, implementing custom business logic for these objects and using the underlying default storage implementation.
type GeneratedNetworkPolicyStorage struct {
	realStore StorageQuerier
	versioner storage.Versioner
}

var _ storage.Interface = &GeneratedNetworkPolicyStorage{}

func NewGeneratedNetworkPolicyStorage(realStore *StorageQuerier) storage.Interface {
	return &GeneratedNetworkPolicyStorage{
		realStore: *realStore,
		versioner: storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *GeneratedNetworkPolicyStorage) Versioner() storage.Versioner {
	return s.versioner
}

// Create is not supported for GeneratedNetworkPolicy objects. Objects are generated on the fly and not stored.
func (s *GeneratedNetworkPolicyStorage) Create(ctx context.Context, key string, obj, out runtime.Object, _ uint64) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Delete is not supported for GeneratedNetworkPolicy objects. Objects are generated on the fly and not stored.
func (s *GeneratedNetworkPolicyStorage) Delete(ctx context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Watch is not supported for GeneratedNetworkPolicy objects. Objects are generated on the fly and not stored.
func (s *GeneratedNetworkPolicyStorage) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	return nil, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Get generates and returns a single GeneratedNetworkPolicy object
func (s *GeneratedNetworkPolicyStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "GeneratedNetworkPolicyStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	logger.L().Ctx(ctx).Debug("GeneratedNetworkPolicyStorage.Get", helpers.String("key", key))

	// retrieve network neighbor with the same name
	networkNeighborsObjPtr := &softwarecomposition.NetworkNeighbors{}

	key = replaceKeyForKind(key, networkNeighborsResource)

	if err := s.realStore.Get(ctx, key, opts, networkNeighborsObjPtr); err != nil {
		return err
	}

	if networkNeighborsObjPtr == nil {
		return fmt.Errorf("network neighbor not found")
	}

	// TODO(DanielGrunberegerCA): get known servers
	generatedNetworkPolicy, err := generateNetworkPolicy(*networkNeighborsObjPtr, []softwarecomposition.KnownServers{})
	if err != nil {
		return fmt.Errorf("error generating network policy: %w", err)
	}

	data, err := json.Marshal(generatedNetworkPolicy)
	if err != nil {
		logger.L().Ctx(ctx).Error("json marshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	if err = json.Unmarshal(data, objPtr); err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	return nil
}

// GetList generates and returns a list of GeneratedNetworkPolicy objects for the given namespace
func (s *GeneratedNetworkPolicyStorage) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	// get all network neighbors on namespace
	networkNeighborsObjListPtr := &softwarecomposition.NetworkNeighborsList{}

	generatedNetworkPolicyList := &softwarecomposition.GeneratedNetworkPolicyList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: storageV1Beta1ApiVersion,
		},
	}

	namespace := getNamespaceFromKey(key)

	if err := s.realStore.GetByNamespace(ctx, softwarecomposition.GroupName, networkNeighborsResource, namespace, networkNeighborsObjListPtr); err != nil {
		return err
	}

	if networkNeighborsObjListPtr == nil {
		return storage.NewInternalError("network neighbors list is nil")
	}

	for _, networkNeighbors := range networkNeighborsObjListPtr.Items {
		generatedNetworkPolicy, err := generateNetworkPolicy(networkNeighbors, []softwarecomposition.KnownServers{})
		if err != nil {
			return fmt.Errorf("error generating network policy: %w", err)
		}

		generatedNetworkPolicyList.Items = append(generatedNetworkPolicyList.Items, generatedNetworkPolicy)

	}

	data, err := json.Marshal(generatedNetworkPolicyList)
	if err != nil {
		logger.L().Ctx(ctx).Error("json marshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	if err = json.Unmarshal(data, listObj); err != nil {
		logger.L().Ctx(ctx).Error("json unmarshal failed", helpers.Error(err), helpers.String("key", key))
		return err
	}

	return nil
}

// GuaranteedUpdate is not supported for GeneratedNetworkPolicy objects. Objects are generated on the fly and not stored.
func (s *GeneratedNetworkPolicyStorage) GuaranteedUpdate(
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Count is not supported for GeneratedNetworkPolicy objects. Objects are generated on the fly and not stored.
func (s *GeneratedNetworkPolicyStorage) Count(key string) (int64, error) {
	return 0, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

func generateNetworkPolicy(networkNeighbors softwarecomposition.NetworkNeighbors, knownServers []softwarecomposition.KnownServers) (softwarecomposition.GeneratedNetworkPolicy, error) {
	networkPolicy := softwarecomposition.NetworkPolicy{
		Kind:       "NetworkPolicy",
		APIVersion: "networking.k8s.io/v1",
		ObjectMeta: metav1.ObjectMeta{
			Name:      networkNeighbors.Name,
			Namespace: networkNeighbors.Namespace,
			Annotations: map[string]string{
				"generated-by": "kubescape",
			},
		},
	}

	if networkNeighbors.Spec.MatchLabels != nil {
		networkPolicy.Spec.PodSelector.MatchLabels = networkNeighbors.Spec.MatchLabels
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
			Name:      networkNeighbors.Name,
			Namespace: networkNeighbors.Namespace,
			Labels:    networkNeighbors.Labels,
		},
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

	generatedNetworkPolicy.Spec = networkPolicy

	return generatedNetworkPolicy, nil
}

func generateEgressRule(neighbor softwarecomposition.NetworkNeighbor, knownServers []softwarecomposition.KnownServers) (softwarecomposition.NetworkPolicyEgressRule, []softwarecomposition.PolicyRef) {
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
		for _, knownServer := range knownServers {
			_, subNet, err := net.ParseCIDR(knownServer.IPBlock)
			if err != nil {
				logger.L().Error("error parsing cidr", helpers.Error(err))
				continue
			}
			if subNet.Contains(net.ParseIP(neighbor.IPAddress)) {
				egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
					IPBlock: &softwarecomposition.IPBlock{
						CIDR: knownServer.IPBlock,
					},
				})
				isKnownServer = true

				policyRef := softwarecomposition.PolicyRef{
					Name:       knownServer.Name,
					OriginalIP: neighbor.IPAddress,
					IPBlock:    knownServer.IPBlock,
				}

				if knownServer.DNS != "" {
					policyRef.DNS = knownServer.DNS
				}

				policyRefs = append(policyRefs, policyRef)
				break
			}
		}

		if !isKnownServer {
			ipBlock := &softwarecomposition.IPBlock{CIDR: neighbor.IPAddress + "/32"}
			egressRule.To = append(egressRule.To, softwarecomposition.NetworkPolicyPeer{
				IPBlock: ipBlock,
			})

			if neighbor.DNS != "" {
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					Name:       neighbor.DNS,
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

func generateIngressRule(neighbor softwarecomposition.NetworkNeighbor, knownServers []softwarecomposition.KnownServers) (softwarecomposition.NetworkPolicyIngressRule, []softwarecomposition.PolicyRef) {
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
		for _, knownServer := range knownServers {
			_, subNet, err := net.ParseCIDR(knownServer.IPBlock)
			if err != nil {
				logger.L().Error("error parsing cidr", helpers.Error(err))
				continue
			}
			if subNet.Contains(net.ParseIP(neighbor.IPAddress)) {
				ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
					IPBlock: &softwarecomposition.IPBlock{
						CIDR: knownServer.IPBlock,
					},
				})
				isKnownServer = true

				policyRef := softwarecomposition.PolicyRef{
					Name:       knownServer.Name,
					OriginalIP: neighbor.IPAddress,
					IPBlock:    knownServer.IPBlock,
				}

				if knownServer.DNS != "" {
					policyRef.DNS = knownServer.DNS
				}

				policyRefs = append(policyRefs, policyRef)
				break
			}
		}

		if !isKnownServer {
			ipBlock := &softwarecomposition.IPBlock{CIDR: neighbor.IPAddress + "/32"}
			ingressRule.From = append(ingressRule.From, softwarecomposition.NetworkPolicyPeer{
				IPBlock: ipBlock,
			})

			if neighbor.DNS != "" {
				policyRefs = append(policyRefs, softwarecomposition.PolicyRef{
					Name:       neighbor.DNS,
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
