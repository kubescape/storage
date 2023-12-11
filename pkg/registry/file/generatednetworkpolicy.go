package file

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

const (
	networkNeighborsResource = "networkneighborses"
	knownServersResource     = "knownservers"
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

	knownServersListObjPtr := &softwarecomposition.KnownServerList{}

	if err := s.realStore.GetClusterScopedResource(ctx, softwarecomposition.GroupName, knownServersResource, knownServersListObjPtr); err != nil {
		return err
	}

	generatedNetworkPolicy, err := networkpolicy.GenerateNetworkPolicy(*networkNeighborsObjPtr, knownServersListObjPtr.Items, metav1.Now())
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

	knownServersListObjPtr := &softwarecomposition.KnownServerList{}
	if err := s.realStore.GetClusterScopedResource(ctx, softwarecomposition.GroupName, knownServersResource, knownServersListObjPtr); err != nil {
		return err
	}

	for _, networkNeighbors := range networkNeighborsObjListPtr.Items {
		generatedNetworkPolicy, err := networkpolicy.GenerateNetworkPolicy(networkNeighbors, knownServersListObjPtr.Items, metav1.Now())
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

// RequestWatchProgress fulfills the storage.Interface
//
// It’s function is only relevant to etcd.
func (s *GeneratedNetworkPolicyStorage) RequestWatchProgress(context.Context) error {
	return nil
}
