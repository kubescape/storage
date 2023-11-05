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

// Code generated by lister-gen. DO NOT EDIT.

package v1beta1

import (
	v1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// KnownServerLister helps list KnownServers.
// All objects returned here must be treated as read-only.
type KnownServerLister interface {
	// List lists all KnownServers in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.KnownServer, err error)
	// KnownServers returns an object that can list and get KnownServers.
	KnownServers(namespace string) KnownServerNamespaceLister
	KnownServerListerExpansion
}

// knownServerLister implements the KnownServerLister interface.
type knownServerLister struct {
	indexer cache.Indexer
}

// NewKnownServerLister returns a new KnownServerLister.
func NewKnownServerLister(indexer cache.Indexer) KnownServerLister {
	return &knownServerLister{indexer: indexer}
}

// List lists all KnownServers in the indexer.
func (s *knownServerLister) List(selector labels.Selector) (ret []*v1beta1.KnownServer, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.KnownServer))
	})
	return ret, err
}

// KnownServers returns an object that can list and get KnownServers.
func (s *knownServerLister) KnownServers(namespace string) KnownServerNamespaceLister {
	return knownServerNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// KnownServerNamespaceLister helps list and get KnownServers.
// All objects returned here must be treated as read-only.
type KnownServerNamespaceLister interface {
	// List lists all KnownServers in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.KnownServer, err error)
	// Get retrieves the KnownServer from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1beta1.KnownServer, error)
	KnownServerNamespaceListerExpansion
}

// knownServerNamespaceLister implements the KnownServerNamespaceLister
// interface.
type knownServerNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all KnownServers in the indexer for a given namespace.
func (s knownServerNamespaceLister) List(selector labels.Selector) (ret []*v1beta1.KnownServer, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.KnownServer))
	})
	return ret, err
}

// Get retrieves the KnownServer from the indexer for a given namespace and name.
func (s knownServerNamespaceLister) Get(name string) (*v1beta1.KnownServer, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1beta1.Resource("knownserver"), name)
	}
	return obj.(*v1beta1.KnownServer), nil
}
