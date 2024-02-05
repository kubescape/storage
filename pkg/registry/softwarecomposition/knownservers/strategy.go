package knownservers

import (
	"context"
	"fmt"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
)

// NewStrategy creates and returns a KnownServerStrategy instance
func NewStrategy(typer runtime.ObjectTyper) KnownServerStrategy {
	return KnownServerStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a KnownServer
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.KnownServer)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a KnownServer")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

func MatchKnownServer(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.KnownServer) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type KnownServerStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (KnownServerStrategy) NamespaceScoped() bool {
	return false
}

func (KnownServerStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (KnownServerStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (KnownServerStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (KnownServerStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (KnownServerStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (KnownServerStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (KnownServerStrategy) Canonicalize(obj runtime.Object) {
}

func (KnownServerStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (KnownServerStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
