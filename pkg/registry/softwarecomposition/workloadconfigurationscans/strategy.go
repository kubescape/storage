package workloadconfigurationscan

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// NewStrategy creates and returns a vulnManifestStrategy instance
func NewStrategy(typer runtime.ObjectTyper) WorkloadConfigurationScanStrategy {
	return WorkloadConfigurationScanStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.WorkloadConfigurationScan)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a WorkloadConfigurationScan")
	}
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
}

// MatchWorkloadConfigurationScan is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchWorkloadConfigurationScan(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.WorkloadConfigurationScan) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type WorkloadConfigurationScanStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (WorkloadConfigurationScanStrategy) NamespaceScoped() bool {
	return true
}

func (WorkloadConfigurationScanStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (WorkloadConfigurationScanStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {
}

func (WorkloadConfigurationScanStrategy) Validate(_ context.Context, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (WorkloadConfigurationScanStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (WorkloadConfigurationScanStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (WorkloadConfigurationScanStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (WorkloadConfigurationScanStrategy) Canonicalize(_ runtime.Object) {
}

func (WorkloadConfigurationScanStrategy) ValidateUpdate(_ context.Context, _, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (WorkloadConfigurationScanStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
