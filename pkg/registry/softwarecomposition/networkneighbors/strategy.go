package networkneighbors

import (
	"context"
	"fmt"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
)

// NewStrategy creates and returns a networkNeighborsStrategy instance
func NewStrategy(typer runtime.ObjectTyper) networkNeighborsStrategy {
	return networkNeighborsStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.NetworkNeighbors)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a NetworkNeighbors")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

func MatchNetworkNeighbor(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.NetworkNeighbors) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type networkNeighborsStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (networkNeighborsStrategy) NamespaceScoped() bool {
	return true
}

func (networkNeighborsStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (s networkNeighborsStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newNN := obj.(*softwarecomposition.NetworkNeighbors)
	oldNN := old.(*softwarecomposition.NetworkNeighbors)

	// completion status cannot be transitioned from 'complete' -> 'partial'
	// in such case, we reject status updates
	if oldNN.Annotations[helpers.CompletionMetadataKey] == helpers.Complete && newNN.Annotations[helpers.CompletionMetadataKey] == helpers.Partial {
		newNN.Annotations[helpers.CompletionMetadataKey] = helpers.Complete
		newNN.Annotations[helpers.StatusMetadataKey] = oldNN.Annotations[helpers.StatusMetadataKey]
	}
}

func (networkNeighborsStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	nn := obj.(*softwarecomposition.NetworkNeighbors)

	allErrors := field.ErrorList{}

	if err := utils.ValidateCompletionAnnotation(nn.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	if err := utils.ValidateStatusAnnotation(nn.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	return allErrors
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (networkNeighborsStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (networkNeighborsStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (networkNeighborsStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (networkNeighborsStrategy) Canonicalize(obj runtime.Object) {
}

func (networkNeighborsStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	nn := obj.(*softwarecomposition.NetworkNeighbors)

	allErrors := field.ErrorList{}

	if err := utils.ValidateCompletionAnnotation(nn.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	if err := utils.ValidateStatusAnnotation(nn.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	return allErrors
}

// WarningsOnUpdate returns warnings for the given update.
func (networkNeighborsStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
