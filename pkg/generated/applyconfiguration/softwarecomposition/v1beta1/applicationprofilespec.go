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

// ApplicationProfileSpecApplyConfiguration represents a declarative configuration of the ApplicationProfileSpec type for use
// with apply.
type ApplicationProfileSpecApplyConfiguration struct {
	Architectures       []string                                        `json:"architectures,omitempty"`
	Containers          []ApplicationProfileContainerApplyConfiguration `json:"containers,omitempty"`
	InitContainers      []ApplicationProfileContainerApplyConfiguration `json:"initContainers,omitempty"`
	EphemeralContainers []ApplicationProfileContainerApplyConfiguration `json:"ephemeralContainers,omitempty"`
}

// ApplicationProfileSpecApplyConfiguration constructs a declarative configuration of the ApplicationProfileSpec type for use with
// apply.
func ApplicationProfileSpec() *ApplicationProfileSpecApplyConfiguration {
	return &ApplicationProfileSpecApplyConfiguration{}
}

// WithArchitectures adds the given value to the Architectures field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Architectures field.
func (b *ApplicationProfileSpecApplyConfiguration) WithArchitectures(values ...string) *ApplicationProfileSpecApplyConfiguration {
	for i := range values {
		b.Architectures = append(b.Architectures, values[i])
	}
	return b
}

// WithContainers adds the given value to the Containers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Containers field.
func (b *ApplicationProfileSpecApplyConfiguration) WithContainers(values ...*ApplicationProfileContainerApplyConfiguration) *ApplicationProfileSpecApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithContainers")
		}
		b.Containers = append(b.Containers, *values[i])
	}
	return b
}

// WithInitContainers adds the given value to the InitContainers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the InitContainers field.
func (b *ApplicationProfileSpecApplyConfiguration) WithInitContainers(values ...*ApplicationProfileContainerApplyConfiguration) *ApplicationProfileSpecApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithInitContainers")
		}
		b.InitContainers = append(b.InitContainers, *values[i])
	}
	return b
}

// WithEphemeralContainers adds the given value to the EphemeralContainers field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the EphemeralContainers field.
func (b *ApplicationProfileSpecApplyConfiguration) WithEphemeralContainers(values ...*ApplicationProfileContainerApplyConfiguration) *ApplicationProfileSpecApplyConfiguration {
	for i := range values {
		if values[i] == nil {
			panic("nil value passed to WithEphemeralContainers")
		}
		b.EphemeralContainers = append(b.EphemeralContainers, *values[i])
	}
	return b
}
