package generatednetworkpolicy

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

// NewStrategy creates and returns a generatedNetworkPolicyStrategy instance
func NewStrategy(typer runtime.ObjectTyper) generatedNetworkPolicyStrategy {
	return generatedNetworkPolicyStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a GeneratedNetworkPolicy
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.GeneratedNetworkPolicy)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a GeneratedNetworkPolicy")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchApplicationProfileSummary is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchGeneratedNetworkPolicy(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.GeneratedNetworkPolicy) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type generatedNetworkPolicyStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (generatedNetworkPolicyStrategy) NamespaceScoped() bool {
	return true
}

func (generatedNetworkPolicyStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (generatedNetworkPolicyStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (generatedNetworkPolicyStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (generatedNetworkPolicyStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (generatedNetworkPolicyStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (generatedNetworkPolicyStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (generatedNetworkPolicyStrategy) Canonicalize(obj runtime.Object) {
}

func (generatedNetworkPolicyStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (generatedNetworkPolicyStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
