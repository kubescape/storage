package configurationscansummary

import (
	"context"
	"fmt"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/validation"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// NewStrategy creates and returns a configurationScanStrategy instance
func NewStrategy(typer runtime.ObjectTyper) configurationScanStrategy {
	return configurationScanStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ConfigurationScanSummary)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a ConfigurationScanSummary")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// ConfigurationScanSummary is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchConfigurationScanSummary(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.ConfigurationScanSummary) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type configurationScanStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (configurationScanStrategy) NamespaceScoped() bool {
	return false
}

func (configurationScanStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (configurationScanStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (configurationScanStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return validation.AlwaysValid(obj)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (configurationScanStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (configurationScanStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (configurationScanStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (configurationScanStrategy) Canonicalize(obj runtime.Object) {
}

func (configurationScanStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (configurationScanStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
