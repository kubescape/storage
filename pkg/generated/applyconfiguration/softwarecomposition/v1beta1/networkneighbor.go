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

// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1beta1

import (
	softwarecompositionv1beta1 "github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	v1 "k8s.io/client-go/applyconfigurations/meta/v1"
)

// NetworkNeighborApplyConfiguration represents a declarative configuration of the NetworkNeighbor type for use
// with apply.
type NetworkNeighborApplyConfiguration struct {
	Identifier        *string                                       `json:"identifier,omitempty"`
	Type              *softwarecompositionv1beta1.CommunicationType `json:"type,omitempty"`
	DNS               *string                                       `json:"dns,omitempty"`
	DNSNames          []string                                      `json:"dnsNames,omitempty"`
	Ports             []NetworkPortApplyConfiguration               `json:"ports,omitempty"`
	PodSelector       *v1.LabelSelectorApplyConfiguration           `json:"podSelector,omitempty"`
	NamespaceSelector *v1.LabelSelectorApplyConfiguration           `json:"namespaceSelector,omitempty"`
	IPAddress         *string                                       `json:"ipAddress,omitempty"`
}

// NetworkNeighborApplyConfiguration constructs a declarative configuration of the NetworkNeighbor type for use with
// apply.
func NetworkNeighbor() *NetworkNeighborApplyConfiguration {
	return &NetworkNeighborApplyConfiguration{}
}

// WithIdentifier sets the Identifier field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Identifier field is set to the value of the last call.
func (b *NetworkNeighborApplyConfiguration) WithIdentifier(value string) *NetworkNeighborApplyConfiguration {
	b.Identifier = &value
	return b
}

// WithType sets the Type field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Type field is set to the value of the last call.
func (b *NetworkNeighborApplyConfiguration) WithType(value softwarecompositionv1beta1.CommunicationType) *NetworkNeighborApplyConfiguration {
	b.Type = &value
	return b
}

// WithDNS sets the DNS field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the DNS field is set to the value of the last call.
func (b *NetworkNeighborApplyConfiguration) WithDNS(value string) *NetworkNeighborApplyConfiguration {
	b.DNS = &value
	return b
}

// WithDNSNames adds the given value to the DNSNames field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the DNSNames field.
func (b *NetworkNeighborApplyConfiguration) WithDNSNames(values ...string) *NetworkNeighborApplyConfiguration {
	for i := range values {
		b.DNSNames = append(b.DNSNames, values[i])
	}
	return b
}

// WithPorts adds the given value to the Ports field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Ports field.
func (b *NetworkNeighborApplyConfiguration) WithPorts(values ...*NetworkPortApplyConfiguration) *NetworkNeighborApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithPorts")
		}
		b.Ports = append(b.Ports, *values[i])
	}
	return b
}

// WithPodSelector sets the PodSelector field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PodSelector field is set to the value of the last call.
func (b *NetworkNeighborApplyConfiguration) WithPodSelector(value *v1.LabelSelectorApplyConfiguration) *NetworkNeighborApplyConfiguration {
	b.PodSelector = value
	return b
}

// WithNamespaceSelector sets the NamespaceSelector field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the NamespaceSelector field is set to the value of the last call.
func (b *NetworkNeighborApplyConfiguration) WithNamespaceSelector(value *v1.LabelSelectorApplyConfiguration) *NetworkNeighborApplyConfiguration {
	b.NamespaceSelector = value
	return b
}

// WithIPAddress sets the IPAddress field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the IPAddress field is set to the value of the last call.
func (b *NetworkNeighborApplyConfiguration) WithIPAddress(value string) *NetworkNeighborApplyConfiguration {
	b.IPAddress = &value
	return b
}
