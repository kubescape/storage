package applicationactivity

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

// NewStrategy creates and returns a applicationActivityStrategy instance
func NewStrategy(typer runtime.ObjectTyper) applicationActivityStrategy {
	return applicationActivityStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ApplicationActivity)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchApplicationActivity is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchApplicationActivity(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.ApplicationActivity) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type applicationActivityStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (applicationActivityStrategy) NamespaceScoped() bool {
	return true
}

func (applicationActivityStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (applicationActivityStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (applicationActivityStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	applicationActivity := obj.(*softwarecomposition.ApplicationActivity)
	return validation.AlwaysValid(applicationActivity)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (applicationActivityStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (applicationActivityStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (applicationActivityStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (applicationActivityStrategy) Canonicalize(obj runtime.Object) {
}

func (applicationActivityStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (applicationActivityStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
