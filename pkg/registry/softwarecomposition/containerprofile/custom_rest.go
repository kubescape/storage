package containerprofile

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// Persistent prefixes are plural. Startup migrates legacy singular prefixes
// before either REST implementation is constructed.

// CustomREST is a hand-written rest.Storage implementation for
// ContainerProfile, gated behind config.Config.CustomContainerProfileRestEnabled
// (see pkg/apiserver/apiserver.go). It talks directly to whatever
// storage.Interface it's given via NewCustomREST -- it does not reimplement
// SQLite/file access or the consolidation processor.
type CustomREST = genericrest.Store

// NewCustomREST returns the custom rest.Storage implementation for
// containerprofile. Its signature intentionally matches NewREST's so it can
// be wired in as a drop-in alternative in pkg/apiserver/apiserver.go,
// including being passed the same non-default containerProfileStorageImpl
// storage.Interface NewREST is wired with today.
func NewCustomREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*CustomREST, error) {
	strategy := NewStrategy(scheme)
	defaultQualifiedResource := softwarecomposition.Resource("containerprofiles")
	singularQualifiedResource := softwarecomposition.Resource("containerprofile")

	return genericrest.NewStore(storageImpl, optsGetter, genericrest.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.ContainerProfile{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.ContainerProfileList{} },
		PredicateFunc:             MatchContainerProfile,
		DefaultQualifiedResource:  defaultQualifiedResource,
		SingularQualifiedResource: singularQualifiedResource,
		Strategy:                  strategy,
		// TODO: define table converter that exposes more than name/creation timestamp
		// (preserved verbatim from etcd.go's NewREST -- not addressed by this migration).
		// Uses the plural qualified resource, matching etcd.go's own
		// TableConvertor construction exactly (etcd.go:48).
		TableConvertor: rest.NewDefaultTableConvertor(defaultQualifiedResource),
	})
}
