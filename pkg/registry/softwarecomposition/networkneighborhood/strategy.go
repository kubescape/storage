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
	"github.com/kubescape/storage/pkg/registry/file/networkmatch"
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

	allErrors = append(allErrors, validateNetworkProfileEntries(&ap.Spec)...)

	return allErrors
}

// validateNetworkProfileEntries walks every NetworkNeighbor in the spec and
// validates each IPAddresses[] and DNSNames[] entry against the v0.0.2
// wildcard token grammar (spec §5.7, §5.8).
//
// This is the admission-time defence; runtime matchers also tolerate
// malformed entries so a misconfigured profile doesn't kill the
// detection path entirely.
func validateNetworkProfileEntries(spec *softwarecomposition.NetworkNeighborhoodSpec) field.ErrorList {
	var errs field.ErrorList
	specPath := field.NewPath("spec")
	// Ordered slice rather than a map: Go map iteration is non-deterministic,
	// and admission errors flow back to clients via the apiserver. Stable
	// ordering keeps error messages reproducible across requests and across
	// test runs.
	groups := []struct {
		name  string
		items []softwarecomposition.NetworkNeighborhoodContainer
	}{
		{name: "containers", items: spec.Containers},
		{name: "initContainers", items: spec.InitContainers},
		{name: "ephemeralContainers", items: spec.EphemeralContainers},
	}
	for _, g := range groups {
		groupPath := specPath.Child(g.name)
		for ci, c := range g.items {
			containerPath := groupPath.Index(ci)
			errs = append(errs, validateNeighborList(containerPath.Child("egress"), c.Egress)...)
			errs = append(errs, validateNeighborList(containerPath.Child("ingress"), c.Ingress)...)
		}
	}
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

	allErrors = append(allErrors, validateNetworkProfileEntries(&ap.Spec)...)

	return allErrors
}

// WarningsOnUpdate returns warnings for the given update.
func (NetworkNeighborhoodStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}
