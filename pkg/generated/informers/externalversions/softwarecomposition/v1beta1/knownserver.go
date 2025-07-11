/*
Copyright The Kubernetes Authors.

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

// Code generated by informer-gen. DO NOT EDIT.

package v1beta1

import (
	context "context"
	time "time"

	apissoftwarecompositionv1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	versioned "github.com/kubescape/storage/pkg/generated/clientset/versioned"
	internalinterfaces "github.com/kubescape/storage/pkg/generated/informers/externalversions/internalinterfaces"
	softwarecompositionv1beta1 "github.com/kubescape/storage/pkg/generated/listers/softwarecomposition/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtime "k8s.io/apimachinery/pkg/runtime"
	watch "k8s.io/apimachinery/pkg/watch"
	cache "k8s.io/client-go/tools/cache"
)

// KnownServerInformer provides access to a shared informer and lister for
// KnownServers.
type KnownServerInformer interface {
	Informer() cache.SharedIndexInformer
	Lister() softwarecompositionv1beta1.KnownServerLister
}

type knownServerInformer struct {
	factory          internalinterfaces.SharedInformerFactory
	tweakListOptions internalinterfaces.TweakListOptionsFunc
	namespace        string
}

// NewKnownServerInformer constructs a new informer for KnownServer type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewKnownServerInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers) cache.SharedIndexInformer {
	return NewFilteredKnownServerInformer(client, namespace, resyncPeriod, indexers, nil)
}

// NewFilteredKnownServerInformer constructs a new informer for KnownServer type.
// Always prefer using an informer factory to get a shared informer instead of getting an independent
// one. This reduces memory footprint and number of connections to the server.
func NewFilteredKnownServerInformer(client versioned.Interface, namespace string, resyncPeriod time.Duration, indexers cache.Indexers, tweakListOptions internalinterfaces.TweakListOptionsFunc) cache.SharedIndexInformer {
	return cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SpdxV1beta1().KnownServers(namespace).List(context.Background(), options)
			},
			WatchFunc: func(options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SpdxV1beta1().KnownServers(namespace).Watch(context.Background(), options)
			},
			ListWithContextFunc: func(ctx context.Context, options v1.ListOptions) (runtime.Object, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SpdxV1beta1().KnownServers(namespace).List(ctx, options)
			},
			WatchFuncWithContext: func(ctx context.Context, options v1.ListOptions) (watch.Interface, error) {
				if tweakListOptions != nil {
					tweakListOptions(&options)
				}
				return client.SpdxV1beta1().KnownServers(namespace).Watch(ctx, options)
			},
		},
		&apissoftwarecompositionv1beta1.KnownServer{},
		resyncPeriod,
		indexers,
	)
}

func (f *knownServerInformer) defaultInformer(client versioned.Interface, resyncPeriod time.Duration) cache.SharedIndexInformer {
	return NewFilteredKnownServerInformer(client, f.namespace, resyncPeriod, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc}, f.tweakListOptions)
}

func (f *knownServerInformer) Informer() cache.SharedIndexInformer {
	return f.factory.InformerFor(&apissoftwarecompositionv1beta1.KnownServer{}, f.defaultInformer)
}

func (f *knownServerInformer) Lister() softwarecompositionv1beta1.KnownServerLister {
	return softwarecompositionv1beta1.NewKnownServerLister(f.Informer().GetIndexer())
}
