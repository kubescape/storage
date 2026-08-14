package containerprofile

import (
	"context"
	"fmt"

	"github.com/kubescape/go-logger"
	logHelpers "github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/networkmatch"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/common"
	"github.com/kubescape/storage/pkg/utils"
)

// NewStrategy creates and returns a ContainerProfileStrategy instance
func NewStrategy(typer runtime.ObjectTyper) ContainerProfileStrategy {
	return ContainerProfileStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a ContainerProfile
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.ContainerProfile)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not an ContainerProfile")
	}
	return apiserver.ObjectMeta.Labels, SelectableFields(apiserver), nil
}

// MatchContainerProfile is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
func MatchContainerProfile(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
func SelectableFields(obj *softwarecomposition.ContainerProfile) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, true)
}

type ContainerProfileStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

func (ContainerProfileStrategy) NamespaceScoped() bool {
	return true
}

func (ContainerProfileStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (ContainerProfileStrategy) PrepareForUpdate(_ context.Context, obj, old runtime.Object) {
	newCP := obj.(*softwarecomposition.ContainerProfile)
	oldCP := old.(*softwarecomposition.ContainerProfile)

	// A container profile that is marked as completed is immutable: reset any
	// incoming update to the stored object. (User-authored profiles carry no
	// lifecycle annotations, so they are never caught by this guard and stay
	// editable.) The deleted ApplicationProfile/NetworkNeighborhood strategies
	// enforced the same contract at this boundary.
	if common.IsComplete(oldCP.Annotations, newCP.Annotations) {
		logger.L().Debug("container profile is marked as completed, rejecting update",
			logHelpers.String("name", oldCP.Name),
			logHelpers.String("namespace", oldCP.Namespace))
		*newCP = *oldCP // reset the new object to the old object
		return
	}

	// completion status cannot be transitioned from 'complete' -> 'partial'
	// in such case, we reject status updates
	if oldCP.Annotations[helpers.CompletionMetadataKey] == helpers.Full && newCP.Annotations[helpers.CompletionMetadataKey] == helpers.Partial {
		logger.L().Debug("container profile completion status cannot be transitioned from 'complete' to 'partial', rejecting status updates",
			logHelpers.String("name", oldCP.Name),
			logHelpers.String("namespace", oldCP.Namespace))

		newCP.Annotations[helpers.CompletionMetadataKey] = helpers.Full

		if v, ok := oldCP.Annotations[helpers.StatusMetadataKey]; ok {
			newCP.Annotations[helpers.StatusMetadataKey] = v
		} else {
			delete(newCP.Annotations, helpers.StatusMetadataKey)
		}
	}
}

func (ContainerProfileStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	cp := obj.(*softwarecomposition.ContainerProfile)

	allErrors := field.ErrorList{}

	if err := utils.ValidateCompletionAnnotation(cp.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	if err := utils.ValidateStatusAnnotation(cp.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	allErrors = append(allErrors, validateNetworkProfileEntries(&cp.Spec)...)

	return allErrors
}

// validateNetworkProfileEntries walks every NetworkNeighbor in the spec and
// validates each IPAddresses[] and DNSNames[] entry against the wildcard token
// grammar (see pkg/registry/file/networkmatch).
//
// This is the admission-time defence; runtime matchers also tolerate malformed
// entries so a misconfigured profile doesn't kill the detection path entirely.
func validateNetworkProfileEntries(spec *softwarecomposition.ContainerProfileSpec) field.ErrorList {
	var errs field.ErrorList
	specPath := field.NewPath("spec")
	errs = append(errs, validateNeighborList(specPath.Child("ingress"), spec.Ingress)...)
	errs = append(errs, validateNeighborList(specPath.Child("egress"), spec.Egress)...)
	return errs
}

func validateNeighborList(parent *field.Path, list []softwarecomposition.NetworkNeighbor) field.ErrorList {
	var errs field.ErrorList
	for ni, n := range list {
		nPath := parent.Index(ni)
		ipsPath := nPath.Child("ipAddresses")
		for ei, e := range n.IPAddresses {
			if err := networkmatch.ValidateIPEntry(e); err != nil {
				errs = append(errs, field.Invalid(ipsPath.Index(ei), e, err.Error()))
			}
		}
		// Deprecated singular IPAddress is still accepted; validate it too
		// so malformed values can't slip past admission via the old form.
		if n.IPAddress != "" {
			if err := networkmatch.ValidateIPEntry(n.IPAddress); err != nil {
				errs = append(errs, field.Invalid(nPath.Child("ipAddress"), n.IPAddress, err.Error()))
			}
		}
		dnsPath := nPath.Child("dnsNames")
		for ei, e := range n.DNSNames {
			if err := networkmatch.ValidateDNSEntry(e); err != nil {
				errs = append(errs, field.Invalid(dnsPath.Index(ei), e, err.Error()))
			}
		}
		// Deprecated singular DNS is still accepted; validate it too,
		// mirroring the IPAddress pattern above.
		if n.DNS != "" {
			if err := networkmatch.ValidateDNSEntry(n.DNS); err != nil {
				errs = append(errs, field.Invalid(nPath.Child("dns"), n.DNS, err.Error()))
			}
		}
	}
	return errs
}

// WarningsOnCreate returns warnings for the creation of the given object.
func (ContainerProfileStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (ContainerProfileStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (ContainerProfileStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (ContainerProfileStrategy) Canonicalize(_ runtime.Object) {
}

func (ContainerProfileStrategy) ValidateUpdate(_ context.Context, obj, _ runtime.Object) field.ErrorList {
	cp := obj.(*softwarecomposition.ContainerProfile)

	allErrors := field.ErrorList{}

	if err := utils.ValidateCompletionAnnotation(cp.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	if err := utils.ValidateStatusAnnotation(cp.Annotations); err != nil {
		allErrors = append(allErrors, err)
	}

	allErrors = append(allErrors, validateNetworkProfileEntries(&cp.Spec)...)

	return allErrors
}

// WarningsOnUpdate returns warnings for the given update.
func (ContainerProfileStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
