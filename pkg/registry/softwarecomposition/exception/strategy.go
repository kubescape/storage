package exception

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

// NewStrategy creates and returns a exceptionStrategy instance
func NewStrategy(typer runtime.ObjectTyper) exceptionStrategy {
	return exceptionStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.Exception)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchException is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchException(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.Exception) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type exceptionStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (exceptionStrategy) NamespaceScoped() bool {
	return true
}

func (exceptionStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (exceptionStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (exceptionStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	exception := obj.(*softwarecomposition.Exception)
	return validation.AlwaysValid(exception)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (exceptionStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (exceptionStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (exceptionStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (exceptionStrategy) Canonicalize(obj runtime.Object) {
}

func (exceptionStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (exceptionStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}