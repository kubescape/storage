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

// ScopedConfigurationScanSummaryLister helps list ScopedConfigurationScanSummaries.
// All objects returned here must be treated as read-only.
type ScopedConfigurationScanSummaryLister interface {
	// List lists all ScopedConfigurationScanSummaries in the indexer.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.ScopedConfigurationScanSummary, err error)
	// ScopedConfigurationScanSummaries returns an object that can list and get ScopedConfigurationScanSummaries.
	ScopedConfigurationScanSummaries(namespace string) ScopedConfigurationScanSummaryNamespaceLister
	ScopedConfigurationScanSummaryListerExpansion
}

// scopedConfigurationScanSummaryLister implements the ScopedConfigurationScanSummaryLister interface.
type scopedConfigurationScanSummaryLister struct {
	indexer cache.Indexer
}

// NewScopedConfigurationScanSummaryLister returns a new ScopedConfigurationScanSummaryLister.
func NewScopedConfigurationScanSummaryLister(indexer cache.Indexer) ScopedConfigurationScanSummaryLister {
	return &scopedConfigurationScanSummaryLister{indexer: indexer}
}

// List lists all ScopedConfigurationScanSummaries in the indexer.
func (s *scopedConfigurationScanSummaryLister) List(selector labels.Selector) (ret []*v1beta1.ScopedConfigurationScanSummary, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ScopedConfigurationScanSummary))
	})
	return ret, err
}

// ScopedConfigurationScanSummaries returns an object that can list and get ScopedConfigurationScanSummaries.
func (s *scopedConfigurationScanSummaryLister) ScopedConfigurationScanSummaries(namespace string) ScopedConfigurationScanSummaryNamespaceLister {
	return scopedConfigurationScanSummaryNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// ScopedConfigurationScanSummaryNamespaceLister helps list and get ScopedConfigurationScanSummaries.
// All objects returned here must be treated as read-only.
type ScopedConfigurationScanSummaryNamespaceLister interface {
	// List lists all ScopedConfigurationScanSummaries in the indexer for a given namespace.
	// Objects returned here must be treated as read-only.
	List(selector labels.Selector) (ret []*v1beta1.ScopedConfigurationScanSummary, err error)
	// Get retrieves the ScopedConfigurationScanSummary from the indexer for a given namespace and name.
	// Objects returned here must be treated as read-only.
	Get(name string) (*v1beta1.ScopedConfigurationScanSummary, error)
	ScopedConfigurationScanSummaryNamespaceListerExpansion
}

// scopedConfigurationScanSummaryNamespaceLister implements the ScopedConfigurationScanSummaryNamespaceLister
// interface.
type scopedConfigurationScanSummaryNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all ScopedConfigurationScanSummaries in the indexer for a given namespace.
func (s scopedConfigurationScanSummaryNamespaceLister) List(selector labels.Selector) (ret []*v1beta1.ScopedConfigurationScanSummary, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1beta1.ScopedConfigurationScanSummary))
	})
	return ret, err
}

// Get retrieves the ScopedConfigurationScanSummary from the indexer for a given namespace and name.
func (s scopedConfigurationScanSummaryNamespaceLister) Get(name string) (*v1beta1.ScopedConfigurationScanSummary, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1beta1.Resource("scopedconfigurationscansummary"), name)
	}
	return obj.(*v1beta1.ScopedConfigurationScanSummary), nil
}