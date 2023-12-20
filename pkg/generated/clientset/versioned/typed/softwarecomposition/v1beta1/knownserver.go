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

// Code generated by client-gen. DO NOT EDIT.

package v1beta1

import (
	"context"
	"time"

	v1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	scheme "github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	rest "k8s.io/client-go/rest"
)

// KnownServersGetter has a method to return a KnownServerInterface.
// A group's client should implement this interface.
type KnownServersGetter interface {
	KnownServers(namespace string) KnownServerInterface
}

// KnownServerInterface has methods to work with KnownServer resources.
type KnownServerInterface interface {
	Create(ctx context.Context, knownServer *v1beta1.KnownServer, opts v1.CreateOptions) (*v1beta1.KnownServer, error)
	Update(ctx context.Context, knownServer *v1beta1.KnownServer, opts v1.UpdateOptions) (*v1beta1.KnownServer, error)
	Delete(ctx context.Context, name string, opts v1.DeleteOptions) error
	DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error
	Get(ctx context.Context, name string, opts v1.GetOptions) (*v1beta1.KnownServer, error)
	List(ctx context.Context, opts v1.ListOptions) (*v1beta1.KnownServerList, error)
	Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error)
	Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta1.KnownServer, err error)
	KnownServerExpansion
}

// knownServers implements KnownServerInterface
type knownServers struct {
	client rest.Interface
	ns     string
}

// newKnownServers returns a KnownServers
func newKnownServers(c *SpdxV1beta1Client, namespace string) *knownServers {
	return &knownServers{
		client: c.RESTClient(),
		ns:     namespace,
	}
}

// Get takes name of the knownServer, and returns the corresponding knownServer object, and an error if there is any.
func (c *knownServers) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1beta1.KnownServer, err error) {
	result = &v1beta1.KnownServer{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("knownservers").
		Name(name).
		VersionedParams(&options, scheme.ParameterCodec).
		Do(ctx).
		Into(result)
	return
}

// List takes label and field selectors, and returns the list of KnownServers that match those selectors.
func (c *knownServers) List(ctx context.Context, opts v1.ListOptions) (result *v1beta1.KnownServerList, err error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	result = &v1beta1.KnownServerList{}
	err = c.client.Get().
		Namespace(c.ns).
		Resource("knownservers").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Do(ctx).
		Into(result)
	return
}

// Watch returns a watch.Interface that watches the requested knownServers.
func (c *knownServers) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	var timeout time.Duration
	if opts.TimeoutSeconds != nil {
		timeout = time.Duration(*opts.TimeoutSeconds) * time.Second
	}
	opts.Watch = true
	return c.client.Get().
		Namespace(c.ns).
		Resource("knownservers").
		VersionedParams(&opts, scheme.ParameterCodec).
		Timeout(timeout).
		Watch(ctx)
}

// Create takes the representation of a knownServer and creates it.  Returns the server's representation of the knownServer, and an error, if there is any.
func (c *knownServers) Create(ctx context.Context, knownServer *v1beta1.KnownServer, opts v1.CreateOptions) (result *v1beta1.KnownServer, err error) {
	result = &v1beta1.KnownServer{}
	err = c.client.Post().
		Namespace(c.ns).
		Resource("knownservers").
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(knownServer).
		Do(ctx).
		Into(result)
	return
}

// Update takes the representation of a knownServer and updates it. Returns the server's representation of the knownServer, and an error, if there is any.
func (c *knownServers) Update(ctx context.Context, knownServer *v1beta1.KnownServer, opts v1.UpdateOptions) (result *v1beta1.KnownServer, err error) {
	result = &v1beta1.KnownServer{}
	err = c.client.Put().
		Namespace(c.ns).
		Resource("knownservers").
		Name(knownServer.Name).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(knownServer).
		Do(ctx).
		Into(result)
	return
}

// Delete takes name of the knownServer and deletes it. Returns an error if one occurs.
func (c *knownServers) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	return c.client.Delete().
		Namespace(c.ns).
		Resource("knownservers").
		Name(name).
		Body(&opts).
		Do(ctx).
		Error()
}

// DeleteCollection deletes a collection of objects.
func (c *knownServers) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	var timeout time.Duration
	if listOpts.TimeoutSeconds != nil {
		timeout = time.Duration(*listOpts.TimeoutSeconds) * time.Second
	}
	return c.client.Delete().
		Namespace(c.ns).
		Resource("knownservers").
		VersionedParams(&listOpts, scheme.ParameterCodec).
		Timeout(timeout).
		Body(&opts).
		Do(ctx).
		Error()
}

// Patch applies the patch and returns the patched knownServer.
func (c *knownServers) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta1.KnownServer, err error) {
	result = &v1beta1.KnownServer{}
	err = c.client.Patch(pt).
		Namespace(c.ns).
		Resource("knownservers").
		Name(name).
		SubResource(subresources...).
		VersionedParams(&opts, scheme.ParameterCodec).
		Body(data).
		Do(ctx).
		Into(result)
	return
}