package containerprofile

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// This file wires the resource-agnostic genericrest.Store (see
// pkg/registry/genericrest/store.go) with containerprofile-specific
// configuration, as a parallel, gated alternative to the
// genericregistry.Store based implementation in etcd.go (which remains the
// reference implementation for differential testing -- see
// custom_rest_test.go). This is Phase 4's third per-resource migration off
// genericregistry.Store (see .omc/plans/storage-locking-rewrite.md), and the
// first with: (a) the singular/plural qualified-resource inversion (see
// below), (b) real, non-trivial Strategy validation/update logic (the
// completed-profile immutability guard and NetworkNeighbor wildcard
// validation in strategy.go), and (c) a non-default storage.Interface
// wired with the consolidation processor (containerProfileStorageImpl,
// passed in by pkg/apiserver/apiserver.go, not the plain storageImpl the
// first two resources use).
//
// (b) and (c) require no special handling here: genericrest.Store.Create/
// Update invoke rest.BeforeCreate/rest.BeforeUpdate against r.Strategy at
// the same point genericregistry.Store does, so ContainerProfileStrategy's
// validation runs identically either way; and genericrest.Store.Storage is
// just whatever storage.Interface NewCustomREST is given, so wiring it with
// containerProfileStorageImpl (which has the Processor set via
// StorageImpl.processor / SetStorage) makes PreSave/AfterCreate's
// consolidation and time-series-write hooks run exactly as they do for the
// old genericregistry.Store-based NewREST today -- those hooks live inside
// StorageImpl.Create/GuaranteedUpdate themselves, not in either REST layer.
//
// (a) does need care: containerprofile's DefaultQualifiedResource is
// singular ("containerprofile") and SingularQualifiedResource is plural
// ("containerprofiles") -- inverted relative to normal convention, and
// relative to the first two migrated resources. This is not cosmetic:
// DefaultQualifiedResource feeds directly into the on-disk/SQLite key
// prefix (via optsGetter.GetRESTOptions -> SimpleStorageFactory.ResourcePrefix,
// vendor server/options/etcd.go:510-512, "resource.Group + "/" +
// resource.Resource""). Swapping these values would silently change the key
// prefix and make every existing ContainerProfile invisible until a data
// migration renamed on-disk keys -- so both values are copied verbatim from
// etcd.go's NewREST below, not "corrected".

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
	// Inverted relative to normal convention -- see the package doc comment
	// above for why these are copied verbatim from etcd.go's NewREST, not
	// "corrected".
	defaultQualifiedResource := softwarecomposition.Resource("containerprofile")
	singularQualifiedResource := softwarecomposition.Resource("containerprofiles")

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
		TableConvertor: rest.NewDefaultTableConvertor(singularQualifiedResource),
	})
}
