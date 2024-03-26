package framework

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

// NewStrategy creates and returns a frameworkStrategy instance
func NewStrategy(typer runtime.ObjectTyper) frameworkStrategy {
	return frameworkStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.Framework)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchFramework is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchFramework(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.Framework) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type frameworkStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (frameworkStrategy) NamespaceScoped() bool {
	return true
}

func (frameworkStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (frameworkStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (frameworkStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	framework := obj.(*softwarecomposition.Framework)
	return validation.AlwaysValid(framework)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (frameworkStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (frameworkStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (frameworkStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (frameworkStrategy) Canonicalize(obj runtime.Object) {
}

func (frameworkStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (frameworkStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}