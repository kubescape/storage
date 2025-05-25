package sbomsyfts

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
func NewStrategy(typer runtime.ObjectTyper) SbomSyftStrategy {
	return SbomSyftStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.SBOMSyft)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not an SBOMSyft")
	}
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
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
func SelectableFields(obj *softwarecomposition.SBOMSyft) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type SbomSyftStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (SbomSyftStrategy) NamespaceScoped() bool {
	return true
}

func (SbomSyftStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (SbomSyftStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {
}

func (SbomSyftStrategy) Validate(_ context.Context, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (SbomSyftStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (SbomSyftStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (SbomSyftStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (SbomSyftStrategy) Canonicalize(_ runtime.Object) {
}

func (SbomSyftStrategy) ValidateUpdate(_ context.Context, _, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (SbomSyftStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
