package file

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/networkpolicy/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

const (
	containerProfilesResource = "containerprofiles"
	knownServersResource      = "knownservers"
)

// GeneratedNetworkPolicyStorage offers a storage solution for GeneratedNetworkPolicy objects, implementing custom business logic for these objects and using the underlying default storage implementation.
type GeneratedNetworkPolicyStorage struct {
	immutableStorage
	realStore StorageQuerier
}

func (s *GeneratedNetworkPolicyStorage) EnableResourceSizeEstimation(keysFunc storage.KeysFunc) error {
	return nil
}

func (s *GeneratedNetworkPolicyStorage) Stats(_ context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (s *GeneratedNetworkPolicyStorage) SetKeysFunc(_ storage.KeysFunc) {}

func (s *GeneratedNetworkPolicyStorage) CompactRevision() int64 {
	return 0
}

var _ storage.Interface = (*GeneratedNetworkPolicyStorage)(nil)

func NewGeneratedNetworkPolicyStorage(realStore StorageQuerier) storage.Interface {
	return &GeneratedNetworkPolicyStorage{
		realStore: realStore,
	}
}

func (s *GeneratedNetworkPolicyStorage) GetCurrentResourceVersion(_ context.Context) (uint64, error) {
	return 0, nil
}

// Get generates and returns a single GeneratedNetworkPolicy object
func (s *GeneratedNetworkPolicyStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "GeneratedNetworkPolicyStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	logger.L().Debug("GeneratedNetworkPolicyStorage.Get", helpers.String("key", key))

	// retrieve the container profile with the same name and generate the policy
	// directly from it.
	containerProfileObjPtr := &softwarecomposition.ContainerProfile{}

	key = replaceKeyForKind(key, containerProfilesResource)

	if err := s.realStore.Get(ctx, key, opts, containerProfileObjPtr); err != nil {
		return err
	}

	knownServersListObjPtr := &softwarecomposition.KnownServerList{}

	if err := s.realStore.GetByCluster(ctx, softwarecomposition.GroupName, knownServersResource, knownServersListObjPtr); err != nil {
		return err
	}

	generatedNetworkPolicy, err := networkpolicy.GenerateNetworkPolicy(containerProfileObjPtr, softwarecomposition.NewKnownServersFinderImpl(knownServersListObjPtr.Items), metav1.Now())
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
func (s *GeneratedNetworkPolicyStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	generatedNetworkPolicyList := &softwarecomposition.GeneratedNetworkPolicyList{
		TypeMeta: metav1.TypeMeta{
			APIVersion: StorageV1Beta1ApiVersion,
		},
	}

	// get all container profiles on namespace
	containerProfileObjListPtr := &softwarecomposition.ContainerProfileList{}
	if err := s.realStore.GetList(ctx, replaceKeyForKind(key, containerProfilesResource), opts, containerProfileObjListPtr); err != nil {
		return err
	}

	for i := range containerProfileObjListPtr.Items {
		cp := &containerProfileObjListPtr.Items[i]
		if !networkpolicy.IsAvailable(cp) {
			continue
		}
		generatedNetworkPolicyList.Items = append(generatedNetworkPolicyList.Items, softwarecomposition.GeneratedNetworkPolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "GeneratedNetworkPolicy",
				APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:              cp.Name,
				Namespace:         cp.Namespace,
				Labels:            cp.Labels,
				CreationTimestamp: metav1.Now(),
			},
			PoliciesRef: []softwarecomposition.PolicyRef{},
		})
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
