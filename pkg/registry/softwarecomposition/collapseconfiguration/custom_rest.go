package collapseconfiguration

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// This file wires the resource-agnostic genericrest.Store (see
// pkg/registry/genericrest/store.go) with collapseconfigurations-specific
// configuration, as a parallel, gated alternative to the
// genericregistry.Store based implementation in etcd.go (which remains the
// reference implementation for differential testing -- see
// custom_rest_test.go). Cluster-scoped, and has real (not no-op) Strategy
// validation: validateCollapseConfigurationSpec (strategy.go) rejects
// negative global thresholds, an out-of-[1,32]-range NetworkCIDRFloorBits,
// and malformed/duplicate per-prefix CollapseConfigs entries. This requires
// no special handling here: genericrest.Store.Create/Update invoke
// rest.BeforeCreate/rest.BeforeUpdate against r.Strategy at the same point
// genericregistry.Store does, so this validation is exercised identically
// by both implementations without any extra wiring. Not inverted:
// DefaultQualifiedResource is plural, SingularQualifiedResource is
// singular, matching normal convention.
type CustomREST = genericrest.Store

// NewCustomREST returns the custom rest.Storage implementation for
// collapseconfigurations. Its signature intentionally matches NewREST's so
// it can be wired in as a drop-in alternative in pkg/apiserver/apiserver.go.
func NewCustomREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*CustomREST, error) {
	strategy := NewStrategy(scheme)
	qualifiedResource := softwarecomposition.Resource("collapseconfigurations")
	singularQualifiedResource := softwarecomposition.Resource("collapseconfiguration")

	return genericrest.NewStore(storageImpl, optsGetter, genericrest.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.CollapseConfiguration{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.CollapseConfigurationList{} },
		PredicateFunc:             MatchCollapseConfiguration,
		DefaultQualifiedResource:  qualifiedResource,
		SingularQualifiedResource: singularQualifiedResource,
		Strategy:                  strategy,
		TableConvertor:            rest.NewDefaultTableConvertor(qualifiedResource),
	})
}
