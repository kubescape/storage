package networkneighborhood

import (
	"context"
	"fmt"

	logHelpers "github.com/kubescape/go-logger/helpers"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/common"
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
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
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

func (NetworkNeighborhoodStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (NetworkNeighborhoodStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newAP := obj.(*softwarecomposition.NetworkNeighborhood)
	oldAP := old.(*softwarecomposition.NetworkNeighborhood)

	// if we have an network neighborhood that is marked as completed, we do not allow any updates
	if common.IsComplete(oldAP.Annotations, newAP.Annotations) {
		logger.L().Debug("network neighborhood is marked as completed, rejecting update",
			logHelpers.String("name", oldAP.Name),
			logHelpers.String("namespace", oldAP.Namespace))
		*newAP = *oldAP // reset the new object to the old object
		return
	}

	// completion status cannot be transitioned from 'complete' -> 'partial'
	// in such case, we reject status updates
	if oldAP.Annotations[helpers.CompletionMetadataKey] == helpers.Full && newAP.Annotations[helpers.CompletionMetadataKey] == helpers.Partial {
		logger.L().Debug("network neighborhood completion status cannot be transitioned from 'complete' to 'partial', rejecting status updates",
			logHelpers.String("name", oldAP.Name),
			logHelpers.String("namespace", oldAP.Namespace))

		newAP.Annotations[helpers.CompletionMetadataKey] = helpers.Full

		if v, ok := oldAP.Annotations[helpers.StatusMetadataKey]; ok {
			newAP.Annotations[helpers.StatusMetadataKey] = v
		} else {
			delete(newAP.Annotations, helpers.StatusMetadataKey)
		}
	}
}

func (NetworkNeighborhoodStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
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
func (NetworkNeighborhoodStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (NetworkNeighborhoodStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (NetworkNeighborhoodStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (NetworkNeighborhoodStrategy) Canonicalize(_ runtime.Object) {
}

func (NetworkNeighborhoodStrategy) ValidateUpdate(_ context.Context, obj, _ runtime.Object) field.ErrorList {
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
func (NetworkNeighborhoodStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
