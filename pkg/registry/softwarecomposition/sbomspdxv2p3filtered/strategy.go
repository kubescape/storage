package sbomspdxv2p3filtered

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

// NewStrategy creates and returns an sbomSDPXv2p3FilteredStrategy instance
func NewStrategy(typer runtime.ObjectTyper) sbomSDPXv2p3FilteredStrategy {
	return sbomSDPXv2p3FilteredStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.SBOMSPDXv2p3Filtered)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchFlunder is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchFlunder(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.SBOMSPDXv2p3Filtered) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type sbomSDPXv2p3FilteredStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (sbomSDPXv2p3FilteredStrategy) NamespaceScoped() bool {
	return true
}

func (sbomSDPXv2p3FilteredStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (sbomSDPXv2p3FilteredStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (sbomSDPXv2p3FilteredStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	sbomSPDXv2p3Filtered := obj.(*softwarecomposition.SBOMSPDXv2p3Filtered)
	return validation.ValidateSBOMSPDXv2p3Filtered(sbomSPDXv2p3Filtered)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (sbomSDPXv2p3FilteredStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (sbomSDPXv2p3FilteredStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (sbomSDPXv2p3FilteredStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (sbomSDPXv2p3FilteredStrategy) Canonicalize(obj runtime.Object) {
}

func (sbomSDPXv2p3FilteredStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (sbomSDPXv2p3FilteredStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
