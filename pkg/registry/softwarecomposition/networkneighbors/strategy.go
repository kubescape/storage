package networkneighbors

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

// NewStrategy creates and returns a networkNeighborsStrategy instance
func NewStrategy(typer runtime.ObjectTyper) networkNeighborsStrategy {
	return networkNeighborsStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given runtime.Object is not a Flunder
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	apiserver, ok := obj.(*softwarecomposition.NetworkNeighbors)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a Flunder")
	}
	return labels.Set(apiserver.ObjectMeta.Labels), SelectableFields(apiserver), nil
}

// MatchApplicationProfileSummary is the filter used by the generic etcd backend to watch events
// from etcd to clients of the apiserver only interested in specific labels/fields.
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

func (networkNeighborsStrategy) PrepareForUpdate(ctx context.Context, obj, old runtime.Object) {
}

func (networkNeighborsStrategy) Validate(ctx context.Context, obj runtime.Object) field.ErrorList {
	networkNeighbors := obj.(*softwarecomposition.NetworkNeighbors)

	return validatePorts(networkNeighbors)
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
	networkNeighbors := obj.(*softwarecomposition.NetworkNeighbors)

	return validatePorts(networkNeighbors)
}

// WarningsOnUpdate returns warnings for the given update.
func (networkNeighborsStrategy) WarningsOnUpdate(ctx context.Context, obj, old runtime.Object) []string {
	return nil
}

func validatePorts(networkNeighbors *softwarecomposition.NetworkNeighbors) field.ErrorList {
	for _, ingress := range networkNeighbors.Spec.Ingress {
		for _, networkPort := range ingress.Ports {
			if *networkPort.Port < 0 || *networkPort.Port > 65535 {
				return field.ErrorList{field.Invalid(field.NewPath("spec").Child("ingress").Child("ports"), *networkPort.Port, "port must be in range 0-65535")}
			}

			expectedPortName := fmt.Sprintf("%s-%d", networkPort.Protocol, *networkPort.Port)
			if networkPort.Name != expectedPortName {
				return field.ErrorList{field.Invalid(field.NewPath("spec").Child("ingress").Child("ports"), *networkPort.Port, fmt.Sprintf("port name must be in format {protocol}-{port}, expected name: %s", expectedPortName))}
			}
		}
	}

	for _, egress := range networkNeighbors.Spec.Egress {
		for _, networkPort := range egress.Ports {
			if *networkPort.Port < 0 || *networkPort.Port > 65535 {
				return field.ErrorList{field.Invalid(field.NewPath("spec").Child("egress").Child("ports"), *networkPort.Port, "port must be in range 0-65535")}
			}

			expectedPortName := fmt.Sprintf("%s-%d", networkPort.Protocol, *networkPort.Port)
			if networkPort.Name != expectedPortName {
				return field.ErrorList{field.Invalid(field.NewPath("spec").Child("egress").Child("ports"), *networkPort.Port, fmt.Sprintf("port name must be in format {protocol}-{port}, expected name: %s", expectedPortName))}
			}
		}
	}
	return field.ErrorList{}
}
