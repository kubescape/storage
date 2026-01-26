package file

import (
	"context"
	"fmt"
	"strconv"

	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

type ApplicationProfileStorage struct {
	realStore StorageQuerier
}

func (a ApplicationProfileStorage) EnableResourceSizeEstimation(keysFunc storage.KeysFunc) error {
	return nil
}

func (a ApplicationProfileStorage) Stats(_ context.Context) (storage.Stats, error) {
	return storage.Stats{}, fmt.Errorf("unimplemented")
}

func (a ApplicationProfileStorage) SetKeysFunc(_ storage.KeysFunc) {}

func (a ApplicationProfileStorage) CompactRevision() int64 {
	return 0
}

var _ storage.Interface = (*ApplicationProfileStorage)(nil)

func NewApplicationProfileStorage(realStore StorageQuerier) storage.Interface {
	return &ApplicationProfileStorage{realStore: realStore}
}

func (a ApplicationProfileStorage) GetCurrentResourceVersion(_ context.Context) (uint64, error) {
	return 0, nil
}

func (a ApplicationProfileStorage) Versioner() storage.Versioner {
	return a.realStore.Versioner()
}

func (a ApplicationProfileStorage) Create(ctx context.Context, key string, obj, out runtime.Object, ttl uint64) error {
	return a.realStore.Create(ctx, key, obj, out, ttl)
}

func (a ApplicationProfileStorage) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions, validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	return a.realStore.Delete(ctx, key, out, preconditions, validateDeletion, cachedExistingObject, opts)
}

func (a ApplicationProfileStorage) Watch(ctx context.Context, key string, opts storage.ListOptions) (watch.Interface, error) {
	return a.realStore.Watch(ctx, key, opts)
}

func (a ApplicationProfileStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	if err := a.realStore.Get(ctx, key, opts, objPtr); err != nil {
		return err
	}
	ap, ok := objPtr.(*softwarecomposition.ApplicationProfile)
	if !ok {
		return fmt.Errorf("object is not an ApplicationProfile")
	}
	if len(ap.Parts) > 0 {
		var architectures []string
		var size int
		for cpKey := range ap.Parts {
			cp := &softwarecomposition.ContainerProfile{}
			if err := a.realStore.Get(ctx, cpKey, opts, cp); err != nil {
				logger.L().Debug("ApplicationProfileStorage.Get - get cp object", loggerhelpers.Error(err))
				return nil
			}
			architectures = append(architectures, cp.Spec.Architectures...)
			if i, err := strconv.Atoi(cp.Annotations[helpersv1.ResourceSizeMetadataKey]); err == nil {
				size += i
			}
			container := softwarecomposition.ApplicationProfileContainer{
				Name:                 cp.Labels[helpersv1.ContainerNameMetadataKey],
				Capabilities:         cp.Spec.Capabilities,
				Execs:                cp.Spec.Execs,
				Opens:                cp.Spec.Opens,
				Syscalls:             cp.Spec.Syscalls,
				SeccompProfile:       cp.Spec.SeccompProfile,
				Endpoints:            cp.Spec.Endpoints,
				ImageID:              cp.Spec.ImageID,
				ImageTag:             cp.Spec.ImageTag,
				PolicyByRuleId:       cp.Spec.PolicyByRuleId,
				IdentifiedCallStacks: cp.Spec.IdentifiedCallStacks,
			}
			switch cp.Annotations[helpersv1.ContainerTypeMetadataKey] {
			case "containers":
				ap.Spec.Containers = append(ap.Spec.Containers, container)
			case "initContainers":
				ap.Spec.InitContainers = append(ap.Spec.InitContainers, container)
			case "ephemeralContainers":
				ap.Spec.EphemeralContainers = append(ap.Spec.EphemeralContainers, container)
			default:
				return fmt.Errorf("unknown container type: %s", cp.Annotations[helpersv1.ContainerTypeMetadataKey])
			}
		}
		ap.Spec.Architectures = DeflateSortString(architectures)
		ap.Annotations[helpersv1.ResourceSizeMetadataKey] = strconv.Itoa(size)
	}
	return nil
}

func (a ApplicationProfileStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	if opts.ResourceVersion == softwarecomposition.ResourceVersionFullSpec {
		return fmt.Errorf("GetList with %s is not allowed for ApplicationProfiles", softwarecomposition.ResourceVersionFullSpec)
	}
	return a.realStore.GetList(ctx, key, opts, listObj)
}

func (a ApplicationProfileStorage) GuaranteedUpdate(ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool, preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return a.realStore.GuaranteedUpdate(ctx, key, destination, ignoreNotFound, preconditions, tryUpdate, cachedExistingObject)
}

func (a ApplicationProfileStorage) ReadinessCheck() error {
	return a.realStore.ReadinessCheck()
}

func (a ApplicationProfileStorage) RequestWatchProgress(ctx context.Context) error {
	return a.realStore.RequestWatchProgress(ctx)
}
