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
	vulnerabilitySummaryKind       = "VulnerabilitySummary"
	vulnerabilitySummariesResource = "vulnerabilitymanifestsummaries"
)

// VulnerabilitySummaryStorage implements a storage for vulnerability summaries.
//
// It provides vulnerability summaries for scopes like namespace and cluster. To get these summaries, the storage fetches existing stored VulnerabilitySummary objects and aggregates them on the fly.
type VulnerabilitySummaryStorage struct {
	realStore StorageQuerier
	versioner storage.Versioner
}

func NewVulnerabilitySummaryStorage(realStore *StorageQuerier) storage.Interface {
	return &VulnerabilitySummaryStorage{
		realStore: *realStore,
		versioner: storage.APIObjectVersioner{},
	}
}

// Versioner Returns Versioner associated with this interface.
func (s *VulnerabilitySummaryStorage) Versioner() storage.Versioner {
	return s.versioner
}

// Create is not supported for VulnerabilitySummary objects. Objects are generated on the fly and not stored.
func (s *VulnerabilitySummaryStorage) Create(ctx context.Context, key string, obj, out runtime.Object, _ uint64) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Delete is not supported for VulnerabilitySummary objects. Objects are generated on the fly and not stored.
func (s *VulnerabilitySummaryStorage) Delete(ctx context.Context, key string, out runtime.Object, _ *storage.Preconditions, _ storage.ValidateObjectFunc, _ runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Watch is not supported for VulnerabilitySummary objects. Objects are generated on the fly and not stored.
func (s *VulnerabilitySummaryStorage) Watch(ctx context.Context, key string, _ storage.ListOptions) (watch.Interface, error) {
	return nil, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

func buildVulnerabilityScanSummary(vulnerabilityManifestSummaryList softwarecomposition.VulnerabilityManifestSummaryList, namespace string) softwarecomposition.VulnerabilitySummary {
	vulnerabilityScanSummaryObj := softwarecomposition.VulnerabilitySummary{
		TypeMeta: v1.TypeMeta{
			Kind:       vulnerabilitySummaryKind,
			APIVersion: storageV1Beta1ApiVersion,
		},
		ObjectMeta: v1.ObjectMeta{
			Name:              namespace,
			CreationTimestamp: v1.Now(),
		},
	}

	for i := range vulnerabilityManifestSummaryList.Items {
		vulnerabilityScanSummaryObj.Merge(&vulnerabilityManifestSummaryList.Items[i])
	}

	return vulnerabilityScanSummaryObj
}

// buildConfigurationScanSummaryForCluster generates a vulnerability summary list for the cluster, where each item is a vulnerability summary for a namespace
func buildVulnerabilitySummaryForCluster(vulnerabilityManifestSummaryList softwarecomposition.VulnerabilityManifestSummaryList) softwarecomposition.VulnerabilitySummaryList {

	// build an map of namespace to workload vulnerability summaries
	mapNamespaceToSummaries := make(map[string][]softwarecomposition.VulnerabilityManifestSummary)

	for _, vlSummary := range vulnerabilityManifestSummaryList.Items {
		if _, ok := mapNamespaceToSummaries[vlSummary.Namespace]; !ok {
			mapNamespaceToSummaries[vlSummary.Namespace] = make([]softwarecomposition.VulnerabilityManifestSummary, 0)
		}
		mapNamespaceToSummaries[vlSummary.Namespace] = append(mapNamespaceToSummaries[vlSummary.Namespace], vlSummary)
	}

	vulnerabilitySummaryList := softwarecomposition.VulnerabilitySummaryList{
		TypeMeta: v1.TypeMeta{
			Kind:       vulnerabilitySummaryKind,
			APIVersion: storageV1Beta1ApiVersion,
		},
	}

	// 1 - build a workload vulnerability summary list for each namespace
	// 2 - generate a single vulnerability summary for the namespace
	// 3 - add the vulnerability summary to the cluster summary list object
	for namespace, vlSummaries := range mapNamespaceToSummaries {
		// for each namespace, create a single workload vulnerability summary object
		nsListObj := softwarecomposition.VulnerabilityManifestSummaryList{
			TypeMeta: v1.TypeMeta{
				Kind:       vulnerabilitySummaryKind,
				APIVersion: storageV1Beta1ApiVersion,
			},
			Items: vlSummaries,
		}

		vulnerabilitySummaryList.Items = append(vulnerabilitySummaryList.Items, buildVulnerabilityScanSummary(nsListObj, namespace))
	}

	return vulnerabilitySummaryList
}

func (s *VulnerabilitySummaryStorage) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "VulnerabilitySummaryStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	vulnerabilityManifestSummaryListObjPtr := &softwarecomposition.VulnerabilityManifestSummaryList{}

	namespace := getNamespaceFromKey(key)

	if err := s.realStore.GetByNamespace(ctx, v1beta1.GroupName, vulnerabilitySummariesResource, namespace, vulnerabilityManifestSummaryListObjPtr); err != nil {
		return err
	}

	if vulnerabilityManifestSummaryListObjPtr == nil {
		return storage.NewInternalError("workload scan summary list is nil")
	}

	if len(vulnerabilityManifestSummaryListObjPtr.Items) == 0 {
		return storage.NewKeyNotFoundError(key, 0)
	}

	vulnerabilitySummaryObj := buildVulnerabilityScanSummary(*vulnerabilityManifestSummaryListObjPtr, namespace)

	data, err := json.Marshal(vulnerabilitySummaryObj)
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

func (s *VulnerabilitySummaryStorage) GetList(ctx context.Context, key string, _ storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "VulnerabilitySummaryStorage.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	vulnerabilityManifestSummaryListObjPtr := &softwarecomposition.VulnerabilityManifestSummaryList{}

	// ask for all vulnerabilitySummaries in the cluster
	if err := s.realStore.GetByCluster(ctx, v1beta1.GroupName, vulnerabilitySummariesResource, vulnerabilityManifestSummaryListObjPtr); err != nil {
		return err
	}

	// generate a single vulnerabilitySummary for the cluster, with an vulnerability summary for each namespace
	nsSummaries := buildVulnerabilitySummaryForCluster(*vulnerabilityManifestSummaryListObjPtr)

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

// GuaranteedUpdate is not supported for VulnerabilitySummary objects. Objects are generated on the fly and not stored.
func (s *VulnerabilitySummaryStorage) GuaranteedUpdate(
	ctx context.Context, key string, destination runtime.Object, ignoreNotFound bool,
	preconditions *storage.Preconditions, tryUpdate storage.UpdateFunc, cachedExistingObject runtime.Object) error {
	return storage.NewInvalidObjError(key, operationNotSupportedMsg)
}

// Count is not supported for VulnerabilitySummary objects. Objects are generated on the fly and not stored.
func (s *VulnerabilitySummaryStorage) Count(key string) (int64, error) {
	return 0, storage.NewInvalidObjError(key, operationNotSupportedMsg)
}
