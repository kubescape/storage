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
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/validation"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// NewStrategy creates and returns a vulnManifestStrategy instance
func NewStrategy(typer runtime.ObjectTyper) workloadConfigurationScanStrategy {
	return workloadConfigurationScanStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.WorkloadConfigurationScan)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a WorkloadConfigurationScan")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
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

type workloadConfigurationScanStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (workloadConfigurationScanStrategy) NamespaceScoped() bool {
	return true
}

func (workloadConfigurationScanStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (workloadConfigurationScanStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (workloadConfigurationScanStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return validation.AlwaysValid(obj)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (workloadConfigurationScanStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string { return nil }

func (workloadConfigurationScanStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (workloadConfigurationScanStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (workloadConfigurationScanStrategy) Canonicalize(obj runtime.Object) {
}

func (workloadConfigurationScanStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (workloadConfigurationScanStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
