package applicationprofile

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

// NewStrategy creates and returns a applicationProfileStrategy instance
func NewStrategy(typer runtime.ObjectTyper) ApplicationProfileStrategy {
	return ApplicationProfileStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ApplicationProfile)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
}

// MatchApplicationProfile is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchApplicationProfile(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.ApplicationProfile) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type ApplicationProfileStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (ApplicationProfileStrategy) NamespaceScoped() bool {
	return true
}

func (ApplicationProfileStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (ApplicationProfileStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newAP := obj.(*softwarecomposition.ApplicationProfile)
	oldAP := old.(*softwarecomposition.ApplicationProfile)

	// if we have an application profile that is marked as complete and completed, we do not allow any updates
	if common.IsComplete(oldAP.Annotations, newAP.Annotations) {
		logger.L().Debug("application profile is marked as complete and completed, rejecting update",
			logHelpers.String("name", oldAP.Name),
			logHelpers.String("namespace", oldAP.Namespace))
		*newAP = *oldAP // reset the new object to the old object
		return
	}

	// completion status cannot be transitioned from 'complete' -> 'partial'
	// in such case, we reject status updates
	if oldAP.Annotations[helpers.CompletionMetadataKey] == helpers.Complete && newAP.Annotations[helpers.CompletionMetadataKey] == helpers.Partial {
		logger.L().Debug("application profile completion status cannot be transitioned from 'complete' to 'partial', rejecting status updates",
			logHelpers.String("name", oldAP.Name),
			logHelpers.String("namespace", oldAP.Namespace))

		newAP.Annotations[helpers.CompletionMetadataKey] = helpers.Complete

		if v, ok := oldAP.Annotations[helpers.StatusMetadataKey]; ok {
			newAP.Annotations[helpers.StatusMetadataKey] = v
		} else {
			delete(newAP.Annotations, helpers.StatusMetadataKey)
		}
	}
}

func (ApplicationProfileStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	ap := obj.(*softwarecomposition.ApplicationProfile)

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
func (ApplicationProfileStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (ApplicationProfileStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (ApplicationProfileStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (ApplicationProfileStrategy) Canonicalize(_ runtime.Object) {
}

func (ApplicationProfileStrategy) ValidateUpdate(_ context.Context, obj, _ runtime.Object) field.ErrorList {
	ap := obj.(*softwarecomposition.ApplicationProfile)

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
func (ApplicationProfileStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
