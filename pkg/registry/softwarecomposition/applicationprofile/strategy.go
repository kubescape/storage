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
	"github.com/kubescape/storage/pkg/utils"
)

// NewStrategy creates and returns a applicationProfileStrategy instance
func NewStrategy(typer runtime.ObjectTyper) applicationProfileStrategy {
	return applicationProfileStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ApplicationProfile)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
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

type applicationProfileStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (applicationProfileStrategy) NamespaceScoped() bool {
	return true
}

func (applicationProfileStrategy) PrepareForCreate(ctx context.Context, obj runtime.Object) {
}

func (applicationProfileStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
	newAP := obj.(*softwarecomposition.ApplicationProfile)
	oldAP := old.(*softwarecomposition.ApplicationProfile)

	// if we have an application profile that is marked as complete and completed, we do not allow any updates
	if oldAP.Annotations[helpers.CompletionMetadataKey] == helpers.Complete && oldAP.Annotations[helpers.StatusMetadataKey] == helpers.Completed {
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

func (applicationProfileStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
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
func (applicationProfileStrategy) WarningsOnCreate(ctx context.Context, obj runtime.Object) []string {
	return nil
}

func (applicationProfileStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (applicationProfileStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (applicationProfileStrategy) Canonicalize(obj runtime.Object) {
}

func (applicationProfileStrategy) ValidateUpdate(ctx context.Context, obj, old runtime.Object) field.ErrorList {
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
func (applicationProfileStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}
