package file

import (
	"context"
	"encoding/json"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

const (
	configurationScanSummaryKind               = "ConfigurationScanSummary"
	operationNotSupportedMsg                   = "operation not supported"
	workloadConfigurationScanSummariesResource = "workloadconfigurationscansummaries"
)

// ConfigurationScanSummaryStorage offers a storage solution for ConfigurationScanSummary objects, implementing custom business logic for these objects and using the underlying default storage implementation.
type ConfigurationScanSummaryStorage struct {
	realStore StorageQuerier
	versioner storage.Versioner
}

var _ storage.Interface = &ConfigurationScanSummaryStorage{}

func NewConfigurationScanSummaryStorage(realStore *StorageQuerier) storage.Interface {
	return &ConfigurationScanSummaryStorage{
		realStore: *realStore,
		versioner: storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *ConfigurationScanSummaryStorage) Versioner() storage.Versioner {
	return s.versioner
}

// Create is not supported for ConfigurationScanSummary objects. Objects are generated on the fly and not stored.
func (s *ConfigurationScanSummaryStorage) Create(ctx context.Context, key string, obj, out runtime.Object, _ uint64) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Delete is not supported for ConfigurationScanSummary objects. Objects are generated on the fly and not stored.
func (s *ConfigurationScanSummaryStorage) Delete(ctx context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Watch is not supported for ConfigurationScanSummary objects. Objects are generated on the fly and not stored.
func (s *ConfigurationScanSummaryStorage) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	return nil, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Get generates and returns a single ConfigurationScanSummary object for a namespace
func (s *ConfigurationScanSummaryStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "ConfigurationScanSummaryStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	workloadScanSummaryListObjPtr := &softwarecomposition.WorkloadConfigurationScanSummaryList{}

	namespace := getNamespaceFromKey(key)

	if err := s.realStore.GetByNamespace(ctx, v1beta1.GroupName, workloadConfigurationScanSummariesResource, namespace, workloadScanSummaryListObjPtr); err != nil {
		return err
	}

	if workloadScanSummaryListObjPtr == nil {
		return storage.NewInternalError("workload scan summary list is nil")
	}

	if len(workloadScanSummaryListObjPtr.Items) == 0 {
		return storage.NewKeyNotFoundError(key, 0)
	}

	configurationScanSummaryObj := buildConfigurationScanSummary(*workloadScanSummaryListObjPtr, namespace)

	data, err := json.Marshal(configurationScanSummaryObj)
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

// GetList generates and returns a list of ConfigurationScanSummary objects for the cluster
func (s *ConfigurationScanSummaryStorage) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "ConfigurationScanSummaryStorage.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	workloadScanSummaryListObjPtr := &softwarecomposition.WorkloadConfigurationScanSummaryList{}

	// ask for all workloadconfigurationscansummaries in the cluster
	if err := s.realStore.GetByCluster(ctx, v1beta1.GroupName, workloadConfigurationScanSummariesResource, workloadScanSummaryListObjPtr); err != nil {
		return err
	}

	// generate a single configurationScanSummary for the cluster, with an configuration scan summary for each namespace
	nsSummaries := buildConfigurationScanSummaryForCluster(*workloadScanSummaryListObjPtr)

	data, err := json.Marshal(nsSummaries)
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

// GuaranteedUpdate is not supported for ConfigurationScanSummary objects. Objects are generated on the fly and not stored.
func (s *ConfigurationScanSummaryStorage) GuaranteedUpdate(
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Count is not supported for ConfigurationScanSummary objects. Objects are generated on the fly and not stored.
func (s *ConfigurationScanSummaryStorage) Count(key string) (int64, error) {
	return 0, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// RequestWatchProgress fulfills the storage.Interface
//
// It’s function is only relevant to etcd.
func (s *ConfigurationScanSummaryStorage) RequestWatchProgress(context.Context) error {
	return nil
}

// buildConfigurationScanSummaryForCluster generates a configuration scan summary list for the cluster, where each item is a configuration scan summary for a namespace
func buildConfigurationScanSummaryForCluster(wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList) softwarecomposition.ConfigurationScanSummaryList {

	// build an map of namespace to workload configuration scan summaries
	mapNamespaceToSummaries := make(map[string][]softwarecomposition.WorkloadConfigurationScanSummary)

	for _, wlSummary := range wlConfigurationScanSummaryList.Items {
		if _, ok := mapNamespaceToSummaries[wlSummary.Namespace]; !ok {
			mapNamespaceToSummaries[wlSummary.Namespace] = make([]softwarecomposition.WorkloadConfigurationScanSummary, 0)
		}
		mapNamespaceToSummaries[wlSummary.Namespace] = append(mapNamespaceToSummaries[wlSummary.Namespace], wlSummary)
	}

	configurationScanSummaryList := softwarecomposition.ConfigurationScanSummaryList{
		TypeMeta: v1.TypeMeta{
			Kind:       configurationScanSummaryKind,
			APIVersion: StorageV1Beta1ApiVersion,
		},
	}

	// 1 - build a workload configuration scan summary list for each namespace
	// 2 - generate a single configuration scan summary for the namespace
	// 3 - add the configuration scan summary to the cluster summary list object
	for namespace, wlSummaries := range mapNamespaceToSummaries {
		// for each namespace, create a single workload configuration scan summary object
		nsListObj := softwarecomposition.WorkloadConfigurationScanSummaryList{
			TypeMeta: v1.TypeMeta{
				Kind:       configurationScanSummaryKind,
				APIVersion: StorageV1Beta1ApiVersion,
			},
			Items: wlSummaries,
		}

		configurationScanSummaryList.Items = append(configurationScanSummaryList.Items, buildConfigurationScanSummary(nsListObj, namespace))
	}

	return configurationScanSummaryList
}

// buildConfigurationScanSummary generates a single configuration scan summary for the given namespace
func buildConfigurationScanSummary(wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList, namespace string) softwarecomposition.ConfigurationScanSummary {
	configurationScanSummaryObj := softwarecomposition.ConfigurationScanSummary{
		TypeMeta: v1.TypeMeta{
			Kind:       configurationScanSummaryKind,
			APIVersion: StorageV1Beta1ApiVersion,
		},
		ObjectMeta: v1.ObjectMeta{
			Name: namespace,
		},
	}

	for i := range wlConfigurationScanSummaryList.Items {
		configurationScanSummaryObj.Spec.Severities.Critical += wlConfigurationScanSummaryList.Items[i].Spec.Severities.Critical
		configurationScanSummaryObj.Spec.Severities.High += wlConfigurationScanSummaryList.Items[i].Spec.Severities.High
		configurationScanSummaryObj.Spec.Severities.Medium += wlConfigurationScanSummaryList.Items[i].Spec.Severities.Medium
		configurationScanSummaryObj.Spec.Severities.Low += wlConfigurationScanSummaryList.Items[i].Spec.Severities.Low
		configurationScanSummaryObj.Spec.Severities.Unknown += wlConfigurationScanSummaryList.Items[i].Spec.Severities.Unknown

		wlIdentifier := softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
			Namespace: wlConfigurationScanSummaryList.Items[i].Namespace,
			Kind:      "WorkloadConfigurationScanSummary",
			Name:      wlConfigurationScanSummaryList.Items[i].Name,
		}

		configurationScanSummaryObj.Spec.WorkloadConfigurationScanSummaryIdentifiers = append(configurationScanSummaryObj.Spec.WorkloadConfigurationScanSummaryIdentifiers, wlIdentifier)

	}

	return configurationScanSummaryObj
}
