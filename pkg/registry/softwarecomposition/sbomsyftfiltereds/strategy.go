package sbomsyftfiltereds

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

// NewStrategy creates and returns a sbomSyftStrategy instance
func NewStrategy(typer runtime.ObjectTyper) sbomSyftStrategy {
	return sbomSyftStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.SBOMSyftFiltered)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not an SBOMSyftFiltered")
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
func SelectableFields(obj *softwarecomposition.SBOMSyftFiltered) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type sbomSyftStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (sbomSyftStrategy) NamespaceScoped() bool {
	return true
}

func (sbomSyftStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (sbomSyftStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (sbomSyftStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (sbomSyftStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (sbomSyftStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (sbomSyftStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (sbomSyftStrategy) Canonicalize(obj runtime.Object) {
}

func (sbomSyftStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (sbomSyftStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
