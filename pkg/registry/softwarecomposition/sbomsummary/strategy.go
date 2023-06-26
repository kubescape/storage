package sbomsummary

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
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/validation"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
)

// NewStrategy creates and returns a sbomSummaryStrategy instance
func NewStrategy(typer runtime.ObjectTyper) sbomSummaryStrategy {
	return sbomSummaryStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.SBOMSummary)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not an SBOMSummary")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchSBOMSummary is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchSBOMSummary(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.SBOMSummary) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type sbomSummaryStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (sbomSummaryStrategy) NamespaceScoped() bool {
	return true
}

func (sbomSummaryStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (sbomSummaryStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (sbomSummaryStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	sbomSummary := obj.(*softwarecomposition.SBOMSummary)
	return validation.ValidateSBOMSummary(sbomSummary)
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (sbomSummaryStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string { return nil }

func (sbomSummaryStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (sbomSummaryStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (sbomSummaryStrategy) Canonicalize(obj runtime.Object) {
}

func (sbomSummaryStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (sbomSummaryStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
