package networkneighborhood

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

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
)

// NewStrategy creates and returns a NetworkNeighborhoodStrategy instance
func NewStrategy(typer runtime.ObjectTyper) NetworkNeighborhoodStrategy {
	return NetworkNeighborhoodStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.NetworkNeighborhood)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a NetworkNeighborhood")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchNetworkNeighborhood is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchNetworkNeighborhood(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.NetworkNeighborhood) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type NetworkNeighborhoodStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (NetworkNeighborhoodStrategy) NamespaceScoped() bool {
	return true
}

func (NetworkNeighborhoodStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (NetworkNeighborhoodStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newAP := obj.(*softwarecomposition.NetworkNeighborhood)
	oldAP := old.(*softwarecomposition.NetworkNeighborhood)

	// completion status cannot be transitioned from 'complete' -> 'partial'
	// in such case, we reject status updates
	if oldAP.Annotations[helpers.CompletionMetadataKey] == helpers.Complete && newAP.Annotations[helpers.CompletionMetadataKey] == helpers.Partial {
		newAP.Annotations[helpers.CompletionMetadataKey] = helpers.Complete

		if v, ok := oldAP.Annotations[helpers.StatusMetadataKey]; ok {
			newAP.Annotations[helpers.StatusMetadataKey] = v
		} else {
			delete(newAP.Annotations, helpers.StatusMetadataKey)
		}
	}
}

func (NetworkNeighborhoodStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	ap := obj.(*softwarecomposition.NetworkNeighborhood)

	allErrors := field.ErrorList{}

	if err := utils.ValidateCompletionAnnotation(ap.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	if err := utils.ValidateStatusAnnotation(ap.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	return allErrors
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (NetworkNeighborhoodStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (NetworkNeighborhoodStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (NetworkNeighborhoodStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (NetworkNeighborhoodStrategy) Canonicalize(obj runtime.Object) {
}

func (NetworkNeighborhoodStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
	ap := obj.(*softwarecomposition.NetworkNeighborhood)

	allErrors := field.ErrorList{}

	if err := utils.ValidateCompletionAnnotation(ap.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	if err := utils.ValidateStatusAnnotation(ap.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	return allErrors
}

// WarningsOnUpdate returns warnings for the given update.
func (NetworkNeighborhoodStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
