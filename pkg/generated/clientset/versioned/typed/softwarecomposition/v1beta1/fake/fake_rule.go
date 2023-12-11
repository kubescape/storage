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

package fake

import (
	"context"

	v1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	labels "k8s.io/apimachinery/pkg/labels"
	types "k8s.io/apimachinery/pkg/types"
	watch "k8s.io/apimachinery/pkg/watch"
	testing "k8s.io/client-go/testing"
)

// FakeRules implements RuleInterface
type FakeRules struct {
	Fake *FakeSpdxV1beta1
	ns   string
}

var rulesResource = v1beta1.SchemeGroupVersion.WithResource("rules")

var rulesKind = v1beta1.SchemeGroupVersion.WithKind("Rule")

// Get takes name of the rule, and returns the corresponding rule object, and an error if there is any.
func (c *FakeRules) Get(ctx context.Context, name string, options v1.GetOptions) (result *v1beta1.Rule, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewGetAction(rulesResource, c.ns, name), &v1beta1.Rule{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.Rule), err
}

// List takes label and field selectors, and returns the list of Rules that match those selectors.
func (c *FakeRules) List(ctx context.Context, opts v1.ListOptions) (result *v1beta1.RuleList, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewListAction(rulesResource, rulesKind, c.ns, opts), &v1beta1.RuleList{})

	if obj == nil {
		return nil, err
	}

	label, _, _ := testing.ExtractFromListOptions(opts)
	if label == nil {
		label = labels.Everything()
	}
	list := &v1beta1.RuleList{ListMeta: obj.(*v1beta1.RuleList).ListMeta}
	for _, item := range obj.(*v1beta1.RuleList).Items {
		if label.Matches(labels.Set(item.Labels)) {
			list.Items = append(list.Items, item)
		}
	}
	return list, err
}

// Watch returns a watch.Interface that watches the requested rules.
func (c *FakeRules) Watch(ctx context.Context, opts v1.ListOptions) (watch.Interface, error) {
	return c.Fake.
		InvokesWatch(testing.NewWatchAction(rulesResource, c.ns, opts))

}

// Create takes the representation of a rule and creates it.  Returns the server's representation of the rule, and an error, if there is any.
func (c *FakeRules) Create(ctx context.Context, rule *v1beta1.Rule, opts v1.CreateOptions) (result *v1beta1.Rule, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewCreateAction(rulesResource, c.ns, rule), &v1beta1.Rule{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.Rule), err
}

// Update takes the representation of a rule and updates it. Returns the server's representation of the rule, and an error, if there is any.
func (c *FakeRules) Update(ctx context.Context, rule *v1beta1.Rule, opts v1.UpdateOptions) (result *v1beta1.Rule, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewUpdateAction(rulesResource, c.ns, rule), &v1beta1.Rule{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.Rule), err
}

// Delete takes name of the rule and deletes it. Returns an error if one occurs.
func (c *FakeRules) Delete(ctx context.Context, name string, opts v1.DeleteOptions) error {
	_, err := c.Fake.
		Invokes(testing.NewDeleteActionWithOptions(rulesResource, c.ns, name, opts), &v1beta1.Rule{})

	return err
}

// DeleteCollection deletes a collection of objects.
func (c *FakeRules) DeleteCollection(ctx context.Context, opts v1.DeleteOptions, listOpts v1.ListOptions) error {
	action := testing.NewDeleteCollectionAction(rulesResource, c.ns, listOpts)

	_, err := c.Fake.Invokes(action, &v1beta1.RuleList{})
	return err
}

// Patch applies the patch and returns the patched rule.
func (c *FakeRules) Patch(ctx context.Context, name string, pt types.PatchType, data []byte, opts v1.PatchOptions, subresources ...string) (result *v1beta1.Rule, err error) {
	obj, err := c.Fake.
		Invokes(testing.NewPatchSubresourceAction(rulesResource, c.ns, name, pt, data, subresources...), &v1beta1.Rule{})

	if obj == nil {
		return nil, err
	}
	return obj.(*v1beta1.Rule), err
}
