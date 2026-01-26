package file

import (
	"context"
	"fmt"

	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

type NetworkNeighborhoodStorage struct {
	realStore StorageQuerier
}

func (a NetworkNeighborhoodStorage) EnableResourceSizeEstimation(keysFunc storage.KeysFunc) error {
	return nil
}

func (a NetworkNeighborhoodStorage) Stats(_ context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (a NetworkNeighborhoodStorage) SetKeysFunc(_ storage.KeysFunc) {}

func (a NetworkNeighborhoodStorage) CompactRevision() int64 {
	return 0
}

var _ storage.Interface = (*NetworkNeighborhoodStorage)(nil)

func NewNetworkNeighborhoodStorage(realStore StorageQuerier) storage.Interface {
	return &NetworkNeighborhoodStorage{realStore: realStore}
}

func (a NetworkNeighborhoodStorage) GetCurrentResourceVersion(_ context.Context) (uint64, error) {
	return 0, nil
}

func (a NetworkNeighborhoodStorage) Versioner() storage.Versioner {
	return a.realStore.Versioner()
}

func (a NetworkNeighborhoodStorage) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	return a.realStore.Create(ctx, key, obj, out, ttl)
}

func (a NetworkNeighborhoodStorage) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions, validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	return a.realStore.Delete(ctx, key, out, preconditions, validateDeletion, cachedExistingObject, opts)
}

func (a NetworkNeighborhoodStorage) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	return a.realStore.Watch(ctx, key, opts)
}

func (a NetworkNeighborhoodStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	if err := a.realStore.Get(ctx, key, opts, objPtr); err != nil {
		return err
	}
	nn, ok := objPtr.(*softwarecomposition.NetworkNeighborhood)
	if !ok {
		return fmt.Errorf("object is not an NetworkNeighborhood")
	}
	if len(nn.Parts) > 0 {
		matchLabels := make(map[string]string)
		var matchExpressions []metav1.LabelSelectorRequirement
		for cpKey := range nn.Parts {
			cp := &softwarecomposition.ContainerProfile{}
			if err := a.realStore.Get(ctx, cpKey, opts, cp); err != nil {
				logger.L().Debug("NetworkNeighborhoodStorage.Get - get cp object", loggerhelpers.Error(err))
				return nil
			}
			matchLabels = utils.MergeMaps(matchLabels, cp.Spec.MatchLabels)
			matchExpressions = append(matchExpressions, cp.Spec.MatchExpressions...)
			container := softwarecomposition.NetworkNeighborhoodContainer{
				Name:    cp.Labels[helpersv1.ContainerNameMetadataKey],
				Ingress: cp.Spec.Ingress,
				Egress:  cp.Spec.Egress,
			}
			switch cp.Annotations[helpersv1.ContainerTypeMetadataKey] {
			case "containers":
				nn.Spec.Containers = append(nn.Spec.Containers, container)
			case "initContainers":
				nn.Spec.InitContainers = append(nn.Spec.InitContainers, container)
			case "ephemeralContainers":
				nn.Spec.EphemeralContainers = append(nn.Spec.EphemeralContainers, container)
			default:
				return fmt.Errorf("unknown container type: %s", cp.Annotations[helpersv1.ContainerTypeMetadataKey])
			}
		}
		nn.Spec.MatchLabels = matchLabels
		nn.Spec.MatchExpressions = DeflateLabelSelectorRequirement(matchExpressions)
	}
	return nil
}

func (a NetworkNeighborhoodStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	if opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec {
		return fmt.Errorf("GetList with %s is not allowed for NetworkNeighborhoods", softwarecomposition.ResourceVersionFullSpec)
	}
	return a.realStore.GetList(ctx, key, opts, listObj)
}

func (a NetworkNeighborhoodStorage) GuaranteedUpdate(ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool, preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return a.realStore.GuaranteedUpdate(ctx, key, destination, ignoreNotFound, preconditions, tryUpdate, cachedExistingObject)
}

func (a NetworkNeighborhoodStorage) ReadinessCheck() error {
	return a.realStore.ReadinessCheck()
}

func (a NetworkNeighborhoodStorage) RequestWatchProgress(ctx context.Context) error {
	return a.realStore.RequestWatchProgress(ctx)
}
