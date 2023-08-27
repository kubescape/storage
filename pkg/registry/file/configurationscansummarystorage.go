package file

import (
	"context"
	"encoding/json"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/spf13/afero"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

const (
	ConfigurationScanSummaryKind = "ConfigurationScanSummary"
)

type ConfigurationScanSummaryStorage struct {
	realStore StorageImpl
	versioner storage.Versioner
}

var _ storage.Interface = &ConfigurationScanSummaryStorage{}

func NewConfigurationScanSummaryStorage(appFs afero.Fs, root string) storage.Interface {
	return &ConfigurationScanSummaryStorage{
		realStore: StorageImpl{
			appFs:           appFs,
			watchDispatcher: newWatchDispatcher(),
			root:            root,
			versioner:       storage.APIObjectVersioner{},
		},
		versioner: storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *ConfigurationScanSummaryStorage) Versioner() storage.Versioner {
	return s.versioner
}

func (s *ConfigurationScanSummaryStorage) Create(ctx context.Context, key string, obj, out runtime.Object, _ uint64) error {
	return storage.NewInvalidObjError(key, "")
}

func (s *ConfigurationScanSummaryStorage) Delete(ctx context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	return storage.NewInvalidObjError(key, "")
}

func (s *ConfigurationScanSummaryStorage) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	return nil, storage.NewInvalidObjError(key, "")
}

func (s *ConfigurationScanSummaryStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "ConfigurationScanSummaryStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	workloadScanSummaryListObjPtr := &softwarecomposition.WorkloadConfigurationScanSummaryList{}

	if err := s.realStore.GetByNamespace(ctx, v1beta1.GroupName, "workloadconfigurationscansummaries", getNamespaceFromKey(key), workloadScanSummaryListObjPtr); err != nil {
		return err
	}

	configurationScanSummaryObj := generateConfigurationScanSummary(*workloadScanSummaryListObjPtr)

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

func (s *ConfigurationScanSummaryStorage) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "ConfigurationScanSummaryStorage.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	workloadScanSummaryListObjPtr := &softwarecomposition.WorkloadConfigurationScanSummaryList{}

	// ask for all workloadconfigurationscansummaries in the cluster
	if err := s.realStore.GetByCluster(ctx, v1beta1.GroupName, "workloadconfigurationscansummaries", workloadScanSummaryListObjPtr); err != nil {
		return err
	}

	// generate a single configurationScanSummary for the cluster, with an configuration scan summary for each namespace
	nsSummaries := generateConfigurationScanSummaryForCluster(*workloadScanSummaryListObjPtr)

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

func (s *ConfigurationScanSummaryStorage) GuaranteedUpdate(
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return storage.NewInvalidObjError(key, "")
}

func (s *ConfigurationScanSummaryStorage) Count(key string) (int64, error) {
	return 0, storage.NewInvalidObjError(key, "")
}

// generateConfigurationScanSummaryForCluster generates a single configuration scan summary for the cluster, where each item is a configuration scan summary for a namespace
func generateConfigurationScanSummaryForCluster(wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList) softwarecomposition.ConfigurationScanSummaryList {

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
			Kind:       ConfigurationScanSummaryKind,
			APIVersion: storageV1Beta1ApiVersion,
		},
	}

	// 1 - build a workload configuration scan summary list for each namespace
	// 2 - generate a single configuration scan summary for the namespace
	// 3 - add the configuration scan summary to the cluster summary object
	for _, wlSummaries := range mapNamespaceToSummaries {
		// for each namespace, create a single workload configuration scan summary object
		nsListObj := softwarecomposition.WorkloadConfigurationScanSummaryList{
			TypeMeta: v1.TypeMeta{
				Kind:       ConfigurationScanSummaryKind,
				APIVersion: storageV1Beta1ApiVersion,
			},
			Items: wlSummaries,
		}

		configurationScanSummaryList.Items = append(configurationScanSummaryList.Items, generateConfigurationScanSummary(nsListObj))
	}

	return configurationScanSummaryList
}

func generateConfigurationScanSummary(wlConfigurationScanSummaryList softwarecomposition.WorkloadConfigurationScanSummaryList) softwarecomposition.ConfigurationScanSummary {

	if len(wlConfigurationScanSummaryList.Items) == 0 {
		return softwarecomposition.ConfigurationScanSummary{}
	}

	cfgObjs := wlConfigurationScanSummaryList.Items

	configurationScanSummaryObj := softwarecomposition.ConfigurationScanSummary{
		TypeMeta: v1.TypeMeta{
			Kind:       ConfigurationScanSummaryKind,
			APIVersion: storageV1Beta1ApiVersion,
		},
		ObjectMeta: v1.ObjectMeta{
			Name: cfgObjs[0].Namespace,
		},
	}

	for i := range cfgObjs {
		configurationScanSummaryObj.Spec.Severities.Critical += cfgObjs[i].Spec.Severities.Critical
		configurationScanSummaryObj.Spec.Severities.High += cfgObjs[i].Spec.Severities.High
		configurationScanSummaryObj.Spec.Severities.Medium += cfgObjs[i].Spec.Severities.Medium
		configurationScanSummaryObj.Spec.Severities.Low += cfgObjs[i].Spec.Severities.Low
		configurationScanSummaryObj.Spec.Severities.Unknown += cfgObjs[i].Spec.Severities.Unknown

		wlIdentifier := softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
			Namespace: cfgObjs[i].Namespace,
			Kind:      "WorkloadConfigurationScanSummary",
			Name:      cfgObjs[i].Name,
		}

		configurationScanSummaryObj.Spec.WorkloadConfigurationScanSummaryIdentifiers = append(configurationScanSummaryObj.Spec.WorkloadConfigurationScanSummaryIdentifiers, wlIdentifier)

	}

	return configurationScanSummaryObj

}
