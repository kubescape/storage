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

package registry

import (
	"context"
	"fmt"

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
	"k8s.io/apiserver/pkg/registry/rest"
)

// REST implements a RESTStorage for API services against etcd
type REST struct {
	*genericregistry.Store
}

// RESTInPeace is just a simple function that panics on error.
// Otherwise returns the given storage object. It is meant to be
// a wrapper for wardle registries.
func RESTInPeace[T any](storage T, err error) T {
	if err != nil {
		err = fmt.Errorf("unable to create REST storage for a resource due to %v, will die", err)
		panic(err)
	}
	return storage
}

// ReadOnlyREST exposes a genericregistry.Store as a read-only resource: it deliberately
// implements only get and list, so that discovery does not advertise watch (computed
// resources are generated on the fly — there is nothing to watch) nor the mutating verbs
// (the storage layer rejects all mutations anyway). The endpoints installer decides verbs
// by type assertion on this object, hence the Store is a field and not embedded: embedding
// would re-promote Watch and friends.
type ReadOnlyREST struct {
	store *genericregistry.Store
}

var (
	_ rest.Storage              = (*ReadOnlyREST)(nil)
	_ rest.Scoper               = (*ReadOnlyREST)(nil)
	_ rest.Getter               = (*ReadOnlyREST)(nil)
	_ rest.Lister               = (*ReadOnlyREST)(nil)
	_ rest.SingularNameProvider = (*ReadOnlyREST)(nil)
)

// NewReadOnlyREST wraps a completed genericregistry.Store in a read-only REST storage.
func NewReadOnlyREST(store *genericregistry.Store) *ReadOnlyREST {
	return &ReadOnlyREST{store: store}
}

// New implements rest.Storage
func (r *ReadOnlyREST) New() runtime.Object { return r.store.New() }

// Destroy implements rest.Storage
func (r *ReadOnlyREST) Destroy() { r.store.Destroy() }

// NamespaceScoped implements rest.Scoper
func (r *ReadOnlyREST) NamespaceScoped() bool { return r.store.NamespaceScoped() }

// GetSingularName implements rest.SingularNameProvider
func (r *ReadOnlyREST) GetSingularName() string { return r.store.GetSingularName() }

// Get implements rest.Getter
func (r *ReadOnlyREST) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	return r.store.Get(ctx, name, options)
}

// NewList implements rest.Lister
func (r *ReadOnlyREST) NewList() runtime.Object { return r.store.NewList() }

// List implements rest.Lister
func (r *ReadOnlyREST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	return r.store.List(ctx, options)
}

// ConvertToTable implements rest.Lister (via rest.TableConvertor)
func (r *ReadOnlyREST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	return r.store.ConvertToTable(ctx, object, tableOptions)
}
