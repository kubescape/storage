package sbomspdxv2p3filtered

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry"
	"github.com/kubescape/storage/pkg/registry/cask"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
)

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(scheme *runtime.Scheme, optsGetter generic.RESTOptionsGetter) (*registry.REST, error) {
	strategy := NewStrategy(scheme)

	store := &cask.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.SBOMSPDXv2p3Filtered{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.SBOMSPDXv2p3FilteredList{} },
		PredicateFunc:             MatchFlunder,
		DefaultQualifiedResource:  softwarecomposition.Resource("sbomspdxv2p3filtereds"),
		SingularQualifiedResource: softwarecomposition.Resource("sbomspdxv2p3filtered"),

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
		DeleteStrategy: strategy,

		// TODO: define table converter that exposes more than name/creation timestamp
		TableConvertor: rest.NewDefaultTableConvertor(softwarecomposition.Resource("sbomspdxv2p3filtereds")),
	}
	options := &generic.StoreOptions{RESTOptions: optsGetter, AttrFunc: GetAttrs}
	if err := store.CompleteWithOptions(options); err != nil {
		return nil, err
	}
	return &registry.REST{store}, nil
}
