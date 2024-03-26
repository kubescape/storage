package workloadconfigurationscansummary

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

// NewStrategy creates and returns a vulnerabilityManifestSummaryStrategy instance
func NewStrategy(typer runtime.ObjectTyper) vulnerabilityManifestSummaryStrategy {
	return vulnerabilityManifestSummaryStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.WorkloadConfigurationScanSummary)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a WorkloadConfigurationScanSummary")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchWorkloadConfigurationScanSummary is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchWorkloadConfigurationScanSummary(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.WorkloadConfigurationScanSummary) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type vulnerabilityManifestSummaryStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (vulnerabilityManifestSummaryStrategy) NamespaceScoped() bool {
	return true
}

func (vulnerabilityManifestSummaryStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (vulnerabilityManifestSummaryStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (vulnerabilityManifestSummaryStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (vulnerabilityManifestSummaryStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (vulnerabilityManifestSummaryStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (vulnerabilityManifestSummaryStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (vulnerabilityManifestSummaryStrategy) Canonicalize(obj runtime.Object) {
}

func (vulnerabilityManifestSummaryStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (vulnerabilityManifestSummaryStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
