package sbomsyfts

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// Both REST implementations use plural persistent prefixes. Startup migrates
// legacy singular prefixes before constructing either implementation.
type CustomREST = genericrest.Store

// NewCustomREST returns the custom rest.Storage implementation for
// sbomsyfts. Its signature intentionally matches NewREST's so it can be
// wired in as a drop-in alternative in pkg/apiserver/apiserver.go.
func NewCustomREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*CustomREST, error) {
	strategy := NewStrategy(scheme)
	defaultQualifiedResource := softwarecomposition.Resource("sbomsyfts")
	singularQualifiedResource := softwarecomposition.Resource("sbomsyft")

	return genericrest.NewStore(storageImpl, optsGetter, genericrest.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.SBOMSyft{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.SBOMSyftList{} },
		PredicateFunc:             MatchWorkloadConfigurationScan,
		DefaultQualifiedResource:  defaultQualifiedResource,
		SingularQualifiedResource: singularQualifiedResource,
		Strategy:                  strategy,
		// TODO: define table converter that exposes more than name/creation timestamp
		// (preserved verbatim from etcd.go's NewREST -- not addressed by this migration).
		TableConvertor: rest.NewDefaultTableConvertor(defaultQualifiedResource),
	})
}
