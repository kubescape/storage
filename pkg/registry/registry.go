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
	"fmt"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	genericregistry "k8s.io/apiserver/pkg/registry/generic/registry"
)

// REST implements a RESTStorage for API services against etcd
type REST struct {
	*genericregistry.Store
}

// RESTInPeace is just a simple function that panics on error.
// Otherwise returns the given storage object. It is meant to be
// a wrapper for wardle registries.
func RESTInPeace(storage *REST, err error) *REST {
	if err != nil {
		err = fmt.Errorf("unable to create REST storage for a resource due to %v, will die", err)
		panic(err)
	}
	return storage
}

// NewCodec returns a runtime.Codec able to encode/decode this API group's
// internal object types via their external v1beta1 representation.
//
// It exists to give genericregistry.DryRunnableStorage a real, non-nil Codec.
// Every softwarecomposition NewREST constructor pre-sets
// DryRunnableStorage.Storage to a non-nil, already-constructed storageImpl
// before calling Store.CompleteWithOptions -- and CompleteWithOptions only
// assigns opts.StorageConfig.Codec into e.Storage.Codec when
// e.Storage.Storage is nil (vendor store.go's CompleteWithOptions), so a
// literal Codec: nil here is never overwritten and survives into production.
// DryRunnableStorage's dry-run branches (Create/GuaranteedUpdate) then call
// runtime.Encode(s.Codec, in) / s.Codec.Decode(...) -- a nil-interface method
// call that panics on any dry-run request, surfaced to clients as an opaque
// 500 by the apiserver's panic-recovery middleware.
func NewCodec(scheme *runtime.Scheme) runtime.Codec {
	return serializer.NewCodecFactory(scheme).LegacyCodec(v1beta1.SchemeGroupVersion)
}
