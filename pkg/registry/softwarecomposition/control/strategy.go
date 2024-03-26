package control

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

// NewStrategy creates and returns a controlStrategy instance
func NewStrategy(typer runtime.ObjectTyper) controlStrategy {
	return controlStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.Control)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchControl is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchControl(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.Control) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type controlStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (controlStrategy) NamespaceScoped() bool {
	return true
}

func (controlStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (controlStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (controlStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	control := obj.(*softwarecomposition.Control)
	return validation.AlwaysValid(control)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (controlStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (controlStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (controlStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (controlStrategy) Canonicalize(obj runtime.Object) {
}

func (controlStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (controlStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}