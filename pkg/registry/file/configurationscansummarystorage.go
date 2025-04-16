package file

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

const (
	configurationScanSummaryKind               = "ConfigurationScanSummary"
	workloadConfigurationScanSummariesResource = "workloadconfigurationscansummaries"
)

// ConfigurationScanSummaryStorage offers a storage solution for ConfigurationScanSummary objects, implementing custom business logic for these objects and using the underlying default storage implementation.
type ConfigurationScanSummaryStorage struct {
	immutableStorage
	realStore StorageQuerier
}

var _ storage.Interface = &ConfigurationScanSummaryStorage{}

func NewConfigurationScanSummaryStorage(realStore StorageQuerier) storage.Interface {
	return &ConfigurationScanSummaryStorage{realStore: realStore}
}

// Get generates and returns a single ConfigurationScanSummary object for a namespace
func (s *ConfigurationScanSummaryStorage) Get(ctx context.Context, key string, _ storage.GetOptions, objPtr runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "ConfigurationScanSummaryStorage.Get")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	workloadScanSummaryListObjPtr := &softwarecomposition.WorkloadConfigurationScanSummaryList{}

	namespace := getNamespaceFromKey(key)

	if err := s.realStore.GetByNamespace(ctx, v1beta1.GroupName, workloadConfigurationScanSummariesResource, namespace, workloadScanSummaryListObjPtr); err != nil {
		return err
	}

	if &workloadScanSummaryListObjPtr == nil {
		return storage.NewInternalError(fmt.Errorf("workload scan summary list is nil"))
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
func (s *ConfigurationScanSummaryStorage) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	ctx, span := otel.Tracer("").Start(ctx, "ConfigurationScanSummaryStorage.GetList")
	span.SetAttributes(attribute.String("key", key))
	defer span.End()

	workloadScanSummaryListObjPtr := &softwarecomposition.WorkloadConfigurationScanSummaryList{}

	// ask for all workloadconfigurationscansummaries in the cluster
	if err := s.realStore.GetList(ctx, "/spdx.softwarecomposition.kubescape.io/"+workloadConfigurationScanSummariesResource, opts, workloadScanSummaryListObjPtr); err != nil {
		return err
	}

	// generate a single configurationScanSummary for the cluster, with a configuration scan summary for each namespace
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

// buildConfigurationScanSummaryForCluster generates a configuration scan summary list for the cluster, where each item is a configuration scan summary for a namespace
func buildConfigurationScanSummaryForCluster(list softwarecomposition.WorkloadConfigurationScanSummaryList) softwarecomposition.ConfigurationScanSummaryList {

	// build a map of namespace to workload configuration scan summaries
	perNS := map[string][]softwarecomposition.WorkloadConfigurationScanSummary{}
	for _, s := range list.Items {
		perNS[s.Namespace] = append(perNS[s.Namespace], s)
	}

	ret := softwarecomposition.ConfigurationScanSummaryList{
		TypeMeta: v1.TypeMeta{
			Kind:       configurationScanSummaryKind,
			APIVersion: StorageV1Beta1ApiVersion,
		},
	}

	type wList = softwarecomposition.WorkloadConfigurationScanSummaryList
	// 1 - build a workload configuration scan summary list for each namespace
	// 2 - generate a single configuration scan summary for the namespace
	// 3 - add the configuration scan summary to the cluster summary list object
	for ns, sums := range perNS {
		// for each namespace, create a single workload configuration scan summary object
		ret.Items = append(ret.Items, buildConfigurationScanSummary(wList{Items: sums}, ns))
	}

	return ret
}

// buildConfigurationScanSummary generates a single configuration scan summary for the given namespace
func buildConfigurationScanSummary(list softwarecomposition.WorkloadConfigurationScanSummaryList, namespace string) softwarecomposition.ConfigurationScanSummary {
	summary := softwarecomposition.ConfigurationScanSummary{
		TypeMeta: v1.TypeMeta{
			Kind:       configurationScanSummaryKind,
			APIVersion: StorageV1Beta1ApiVersion,
		},
		ObjectMeta: v1.ObjectMeta{
			Name: namespace,
		},
	}

	for i := range list.Items {
		summary.Spec.Severities.Critical += list.Items[i].Spec.Severities.Critical
		summary.Spec.Severities.High += list.Items[i].Spec.Severities.High
		summary.Spec.Severities.Medium += list.Items[i].Spec.Severities.Medium
		summary.Spec.Severities.Low += list.Items[i].Spec.Severities.Low
		summary.Spec.Severities.Unknown += list.Items[i].Spec.Severities.Unknown

		id := softwarecomposition.WorkloadConfigurationScanSummaryIdentifier{
			Namespace: list.Items[i].Namespace,
			Kind:      "WorkloadConfigurationScanSummary",
			Name:      list.Items[i].Name,
		}

		summary.Spec.WorkloadConfigurationScanSummaryIdentifiers = append(summary.Spec.WorkloadConfigurationScanSummaryIdentifiers, id)

	}

	return summary
}
