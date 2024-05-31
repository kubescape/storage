package seccompprofiles

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

// NewStrategy creates and returns a seccompProfileStrategy instance
func NewStrategy(typer runtime.ObjectTyper) seccompProfileStrategy {
	return seccompProfileStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.SeccompProfile)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not an SeccompProfile")
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
func SelectableFields(obj *softwarecomposition.SeccompProfile) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type seccompProfileStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (seccompProfileStrategy) NamespaceScoped() bool {
	return true
}

func (seccompProfileStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (seccompProfileStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (seccompProfileStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (seccompProfileStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (seccompProfileStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (seccompProfileStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (seccompProfileStrategy) Canonicalize(obj runtime.Object) {
}

func (seccompProfileStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (seccompProfileStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
