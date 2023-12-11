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

// ControlConfigurationLister helps list ControlConfigurations.
// All objects returned here must be treated as read-only.
type ControlConfigurationLister interface {
	// List lists all ControlConfigurations in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.ControlConfiguration, err error)
	// ControlConfigurations returns an object that can list and get ControlConfigurations.
	ControlConfigurations(namespace string) ControlConfigurationNamespaceLister
	ControlConfigurationListerExpansion
}

// controlConfigurationLister implements the ControlConfigurationLister interface.
type controlConfigurationLister struct {
	indexer cache.Indexer
}

// NewControlConfigurationLister returns a new ControlConfigurationLister.
func NewControlConfigurationLister(indexer cache.Indexer) ControlConfigurationLister {
	return &controlConfigurationLister{indexer: indexer}
}

// List lists all ControlConfigurations in the indexer.
func (s *controlConfigurationLister) List(selector labels.Selector) (ret []*v1beta1.ControlConfiguration, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ControlConfiguration))
	})
	return ret, err
}

// ControlConfigurations returns an object that can list and get ControlConfigurations.
func (s *controlConfigurationLister) ControlConfigurations(namespace string) ControlConfigurationNamespaceLister {
	return controlConfigurationNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ControlConfigurationNamespaceLister helps list and get ControlConfigurations.
// All objects returned here must be treated as read-only.
type ControlConfigurationNamespaceLister interface {
	// List lists all ControlConfigurations in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.ControlConfiguration, err error)
	// Get retrieves the ControlConfiguration from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1beta1.ControlConfiguration, error)
	ControlConfigurationNamespaceListerExpansion
}

// controlConfigurationNamespaceLister implements the ControlConfigurationNamespaceLister
// interface.
type controlConfigurationNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ControlConfigurations in the indexer for a given namespace.
func (s controlConfigurationNamespaceLister) List(selector labels.Selector) (ret []*v1beta1.ControlConfiguration, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ControlConfiguration))
	})
	return ret, err
}

// Get retrieves the ControlConfiguration from the indexer for a given namespace and name.
func (s controlConfigurationNamespaceLister) Get(name string) (*v1beta1.ControlConfiguration, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1beta1.Resource("controlconfiguration"), name)
	}
	return obj.(*v1beta1.ControlConfiguration), nil
}
