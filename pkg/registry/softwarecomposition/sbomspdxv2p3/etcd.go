/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package sbomspdxv2p3

import (
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/registry/generic"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
)

// NewREST returns a RESTStorage object that will work against API services.
func NewREST(scheme *runtime.Scheme, optsGetter generic.RESTOptionsGetter) (*registry.REST, error) {
	strategy := NewStrategy(scheme)

	storageImpl := registry.NewStorageImpl()

	dryRunnableStorage := genericregistry.DryRunnableStorage{Codec: nil, Storage: storageImpl}

	store := &genericregistry.Store{
		NewFunc:                   func() runtime.Object { return &softwarecomposition.SBOMSPDXv2p3{} },
		NewListFunc:               func() runtime.Object { return &softwarecomposition.SBOMSPDXv2p3List{} },
		PredicateFunc:             MatchFlunder,
		DefaultQualifiedResource:  softwarecomposition.Resource("sbomspdxv2p3s"),
		SingularQualifiedResource: softwarecomposition.Resource("sbomspdxv2p3"),

		Storage: dryRunnableStorage,

		CreateStrategy: strategy,
		UpdateStrategy: strategy,
		DeleteStrategy: strategy,

		// TODO: define table converter that exposes more than name/creation timestamp
		TableConvertor: rest.NewDefaultTableConvertor(softwarecomposition.Resource("sbomspdxv2p3s")),
	}
	options := &generic.StoreOptions{RESTOptions: optsGetter, AttrFunc: GetAttrs}
	if err := store.CompleteWithOptions(options); err != nil {
		return nil, err
	}
	return &registry.REST{store}, nil
}
