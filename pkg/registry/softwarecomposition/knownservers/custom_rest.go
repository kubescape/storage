package knownservers

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// This file wires the resource-agnostic genericrest.Store (see
// pkg/registry/genericrest/store.go) with knownservers-specific
// configuration, as a parallel, gated alternative to the
// genericregistry.Store based implementation in etcd.go (which remains the
// reference implementation for differential testing -- see
// custom_rest_test.go).
//
// Everything that isn't knownservers-specific -- Get/List/Watch,
// Create/Update/Delete/DeleteCollection, dry-run, the BeforeUpdate
// carry-over rules, resourceVersion conflict handling, the
// finalizer/graceful-deletion state machine, and key-prefix derivation --
// lives in genericrest.Store and is shared with other resources (see
// pkg/registry/softwarecomposition/openvulnerabilityexchange for the second
// consumer). This file supplies only what's genuinely specific to
// KnownServer: New()/NewList(), the label/field selector predicate
// (MatchKnownServer), the qualified resource names, and the strategy value
// -- knownservers is cluster-scoped, so genericrest.Store's KeyFunc/KeyRootFunc
// resolve to genericregistry.NoNamespaceKeyFunc via KnownServerStrategy.NamespaceScoped()
// returning false.

// CustomREST is a hand-written rest.Storage implementation for KnownServer,
// gated behind config.Config.CustomKnownServersRestEnabled (see
// pkg/apiserver/apiserver.go). It talks directly to the shared StorageImpl
// via storage.Interface -- it does not reimplement SQLite/file access.
type CustomREST = genericrest.Store

// NewCustomREST returns the custom rest.Storage implementation for
// knownservers. Its signature intentionally matches NewREST's so it can be
// wired in as a drop-in alternative in pkg/apiserver/apiserver.go.
func NewCustomREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*CustomREST, error) {
	strategy := NewStrategy(scheme)
	qualifiedResource := softwarecomposition.Resource("knownservers")
	singularQualifiedResource := softwarecomposition.Resource("knownserver")

	return genericrest.NewStore(storageImpl, optsGetter, genericrest.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.KnownServer{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.KnownServerList{} },
		PredicateFunc:             MatchKnownServer,
		DefaultQualifiedResource:  qualifiedResource,
		SingularQualifiedResource: singularQualifiedResource,
		Strategy:                  strategy,
		// TODO: define table converter that exposes more than name/creation timestamp
		// (preserved verbatim from etcd.go's NewREST -- not addressed by this migration).
		TableConvertor: rest.NewDefaultTableConvertor(qualifiedResource),
	})
}
