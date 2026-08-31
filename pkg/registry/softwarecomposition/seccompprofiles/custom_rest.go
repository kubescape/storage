package seccompprofiles

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// This file wires the resource-agnostic genericrest.Store (see
// pkg/registry/genericrest/store.go) with seccompprofiles-specific
// configuration, as a parallel, gated alternative to the
// genericregistry.Store based implementation in etcd.go (which remains the
// reference implementation for differential testing -- see
// custom_rest_test.go). Namespaced, no-op Strategy.
// DefaultQualifiedResource/SingularQualifiedResource are inverted relative
// to normal convention (singular/plural swapped) -- this directly
// determines the on-disk key prefix (see
// pkg/registry/softwarecomposition/containerprofile/custom_rest.go's doc
// comment for why), so both values are copied verbatim from etcd.go's
// NewREST, not "corrected".
type CustomREST = genericrest.Store

// NewCustomREST returns the custom rest.Storage implementation for
// seccompprofiles. Its signature intentionally matches NewREST's so it can
// be wired in as a drop-in alternative in pkg/apiserver/apiserver.go.
func NewCustomREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*CustomREST, error) {
	strategy := NewStrategy(scheme)
	defaultQualifiedResource := softwarecomposition.Resource("seccompprofile")
	singularQualifiedResource := softwarecomposition.Resource("seccompprofiles")

	return genericrest.NewStore(storageImpl, optsGetter, genericrest.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.SeccompProfile{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.SeccompProfileList{} },
		PredicateFunc:             MatchWorkloadConfigurationScan,
		DefaultQualifiedResource:  defaultQualifiedResource,
		SingularQualifiedResource: singularQualifiedResource,
		Strategy:                  strategy,
		// TODO: define table converter that exposes more than name/creation timestamp
		// (preserved verbatim from etcd.go's NewREST -- not addressed by this migration).
		TableConvertor: rest.NewDefaultTableConvertor(singularQualifiedResource),
	})
}
