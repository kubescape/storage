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
func NewStrategy(typer runtime.ObjectTyper) GeneratedNetworkPolicyStrategy {
	return GeneratedNetworkPolicyStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a GeneratedNetworkPolicy
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.GeneratedNetworkPolicy)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a GeneratedNetworkPolicy")
	}
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
}

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

type GeneratedNetworkPolicyStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (GeneratedNetworkPolicyStrategy) NamespaceScoped() bool {
	return true
}

func (GeneratedNetworkPolicyStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (GeneratedNetworkPolicyStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {
}

func (GeneratedNetworkPolicyStrategy) Validate(_ context.Context, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (GeneratedNetworkPolicyStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (GeneratedNetworkPolicyStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (GeneratedNetworkPolicyStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (GeneratedNetworkPolicyStrategy) Canonicalize(_ runtime.Object) {
}

func (GeneratedNetworkPolicyStrategy) ValidateUpdate(_ context.Context, _, _ runtime.Object) field.ErrorList {
	return field.ErrorList{}
}

// WarningsOnUpdate returns warnings for the given update.
func (GeneratedNetworkPolicyStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
