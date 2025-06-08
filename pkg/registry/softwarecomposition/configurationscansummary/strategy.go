package configurationscansummary

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

// NewStrategy creates and returns a configurationScanStrategy instance
func NewStrategy(typer runtime.ObjectTyper) ConfigurationScanStrategy {
	return ConfigurationScanStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ConfigurationScanSummary)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a ConfigurationScanSummary")
	}
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
}

// MatchConfigurationScanSummary is the filter used by the generic etcd backend to watch events
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

type ConfigurationScanStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (ConfigurationScanStrategy) NamespaceScoped() bool {
	return false
}

func (ConfigurationScanStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (ConfigurationScanStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {
}

func (ConfigurationScanStrategy) Validate(_ context.Context, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (ConfigurationScanStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (ConfigurationScanStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (ConfigurationScanStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (ConfigurationScanStrategy) Canonicalize(_ runtime.Object) {
}

func (ConfigurationScanStrategy) ValidateUpdate(_ context.Context, _, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (ConfigurationScanStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
