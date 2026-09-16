/*
Copyright 2016 The Kubernetes Authors.

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

package apiserver

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestWatchEventObjectEncodesThroughApiserverScheme is a regression test for
// issue #405: the watch handler installed by k8s.io/apiserver
// (endpoints/handlers.WatchServer.HandleHTTP) encodes every watch.Event.Object
// through this package's Scheme/Codecs -- the very same ones used to build
// serverside REST storage in this file. That encode call fails with
// "no kind is registered for the type X" whenever X is not a type the Scheme
// knows about.
//
// pkg/registry/file's periodic cleanup loop used to dispatch delete events to
// watchers using file.PartialObjectMetadata, an internal helper type that
// only implements runtime.Object well enough to satisfy
// storage.Interface.Delete's metaOut parameter -- it was never registered
// with this Scheme, so a real HTTP watch client blew up the moment a
// non-user-managed resource with an active watcher aged out. This test
// exercises the exact encode call the apiserver's watch handler performs, so
// it fails the same way the production bug did if the dispatched object type
// regresses back to something unregistered.
func TestWatchEventObjectEncodesThroughApiserverScheme(t *testing.T) {
	mediaTypes := Codecs.SupportedMediaTypes()
	serializerInfo, ok := runtime.SerializerInfoForMediaType(mediaTypes, runtime.ContentTypeJSON)
	require.True(t, ok, "expected a JSON serializer to be registered")
	encoder := Codecs.EncoderForVersion(serializerInfo.Serializer, v1beta1.SchemeGroupVersion)

	t.Run("file.PartialObjectMetadata is not scheme-registered (documents the historical bug)", func(t *testing.T) {
		unregistered := &file.PartialObjectMetadata{
			ObjectMeta: metav1.ObjectMeta{Name: "some-scan", Namespace: "default"},
		}
		_, err := runtime.Encode(encoder, unregistered)
		require.Error(t, err, "file.PartialObjectMetadata must remain unregistered -- if this starts encoding, the assertion below is no longer testing anything")
		assert.Contains(t, err.Error(), "no kind is registered")
	})

	t.Run("WorkloadConfigurationScan (the type now dispatched for issue #405) encodes with a valid kind/version", func(t *testing.T) {
		obj := &softwarecomposition.WorkloadConfigurationScan{
			ObjectMeta: metav1.ObjectMeta{Name: "some-scan", Namespace: "default"},
		}
		data, err := runtime.Encode(encoder, obj)
		require.NoError(t, err, "the object dispatched to watchers on delete must encode through the real apiserver Scheme/Codecs, exactly as WatchServer.HandleHTTP does")

		decodedInto := &v1beta1.WorkloadConfigurationScan{}
		_, _, err = Codecs.UniversalDeserializer().Decode(data, nil, decodedInto)
		require.NoError(t, err)
		assert.Equal(t, "WorkloadConfigurationScan", decodedInto.Kind)
		assert.Equal(t, v1beta1.SchemeGroupVersion.String(), decodedInto.APIVersion)
		assert.Equal(t, "some-scan", decodedInto.Name)
		assert.Equal(t, "default", decodedInto.Namespace)
	})
}
