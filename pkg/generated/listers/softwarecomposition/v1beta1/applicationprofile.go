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

// ApplicationProfileLister helps list ApplicationProfiles.
// All objects returned here must be treated as read-only.
type ApplicationProfileLister interface {
	// List lists all ApplicationProfiles in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.ApplicationProfile, err error)
	// ApplicationProfiles returns an object that can list and get ApplicationProfiles.
	ApplicationProfiles(namespace string) ApplicationProfileNamespaceLister
	ApplicationProfileListerExpansion
}

// applicationProfileLister implements the ApplicationProfileLister interface.
type applicationProfileLister struct {
	indexer cache.Indexer
}

// NewApplicationProfileLister returns a new ApplicationProfileLister.
func NewApplicationProfileLister(indexer cache.Indexer) ApplicationProfileLister {
	return &applicationProfileLister{indexer: indexer}
}

// List lists all ApplicationProfiles in the indexer.
func (s *applicationProfileLister) List(selector labels.Selector) (ret []*v1beta1.ApplicationProfile, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ApplicationProfile))
	})
	return ret, err
}

// ApplicationProfiles returns an object that can list and get ApplicationProfiles.
func (s *applicationProfileLister) ApplicationProfiles(namespace string) ApplicationProfileNamespaceLister {
	return applicationProfileNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ApplicationProfileNamespaceLister helps list and get ApplicationProfiles.
// All objects returned here must be treated as read-only.
type ApplicationProfileNamespaceLister interface {
	// List lists all ApplicationProfiles in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.ApplicationProfile, err error)
	// Get retrieves the ApplicationProfile from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1beta1.ApplicationProfile, error)
	ApplicationProfileNamespaceListerExpansion
}

// applicationProfileNamespaceLister implements the ApplicationProfileNamespaceLister
// interface.
type applicationProfileNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ApplicationProfiles in the indexer for a given namespace.
func (s applicationProfileNamespaceLister) List(selector labels.Selector) (ret []*v1beta1.ApplicationProfile, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ApplicationProfile))
	})
	return ret, err
}

// Get retrieves the ApplicationProfile from the indexer for a given namespace and name.
func (s applicationProfileNamespaceLister) Get(name string) (*v1beta1.ApplicationProfile, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1beta1.Resource("applicationprofile"), name)
	}
	return obj.(*v1beta1.ApplicationProfile), nil
}
