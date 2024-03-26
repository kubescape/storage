package controlconfiguration

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

// NewStrategy creates and returns a controlconfigurationStrategy instance
func NewStrategy(typer runtime.ObjectTyper) controlconfigurationStrategy {
	return controlconfigurationStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ControlConfiguration)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchControlConfiguration is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchControlConfiguration(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.ControlConfiguration) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type controlconfigurationStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (controlconfigurationStrategy) NamespaceScoped() bool {
	return true
}

func (controlconfigurationStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (controlconfigurationStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (controlconfigurationStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	controlconfiguration := obj.(*softwarecomposition.ControlConfiguration)
	return validation.AlwaysValid(controlconfiguration)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (controlconfigurationStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (controlconfigurationStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (controlconfigurationStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (controlconfigurationStrategy) Canonicalize(obj runtime.Object) {
}

func (controlconfigurationStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (controlconfigurationStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}