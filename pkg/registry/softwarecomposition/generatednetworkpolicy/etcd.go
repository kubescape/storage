package generatednetworkpolicy

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// NewREST returns a read-only RESTStorage object that will work against API services.
// GeneratedNetworkPolicy objects are computed on the fly: watch and mutations are not
// supported, so the watch and mutating verbs are deliberately not advertised in discovery.
func NewREST(scheme *runtime.Scheme, storageImpl storage.Interface, optsGetter generic.RESTOptionsGetter) (*registry.ReadOnlyREST, error) {
	strategy := NewStrategy(scheme)

	dryRunnableStorage := genericregistry.DryRunnableStorage{Codec: nil, Storage: storageImpl}

	store := &genericregistry.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.GeneratedNetworkPolicy{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.GeneratedNetworkPolicyList{} },
		PredicateFunc:             MatchGeneratedNetworkPolicy,
		DefaultQualifiedResource:  softwarecomposition.Resource("generatednetworkpolicies"),
		SingularQualifiedResource: softwarecomposition.Resource("generatednetworkpolicy"),

		Storage: dryRunnableStorage,

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
		DeleteStrategy: strategy,

		// TODO: define table converter that exposes more than name/creation timestamp
		TableConvertor: rest.NewDefaultTableConvertor(softwarecomposition.Resource("generatednetworkpolicies")),
	}
	options := &generic.StoreOptions{RESTOptions: optsGetter, AttrFunc: GetAttrs}
	if err := store.CompleteWithOptions(options); err != nil {
		return nil, err
	}

	return registry.NewReadOnlyREST(store), nil
}
