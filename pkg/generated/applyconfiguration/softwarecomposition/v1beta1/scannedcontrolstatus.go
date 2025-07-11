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

// ScannedControlStatusApplyConfiguration represents a declarative configuration of the ScannedControlStatus type for use
// with apply.
type ScannedControlStatusApplyConfiguration struct {
	Status    *string `json:"status,omitempty"`
	SubStatus *string `json:"subStatus,omitempty"`
	Info      *string `json:"info,omitempty"`
}

// ScannedControlStatusApplyConfiguration constructs a declarative configuration of the ScannedControlStatus type for use with
// apply.
func ScannedControlStatus() *ScannedControlStatusApplyConfiguration {
	return &ScannedControlStatusApplyConfiguration{}
}

// WithStatus sets the Status field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Status field is set to the value of the last call.
func (b *ScannedControlStatusApplyConfiguration) WithStatus(value string) *ScannedControlStatusApplyConfiguration {
	b.Status = &value
	return b
}

// WithSubStatus sets the SubStatus field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SubStatus field is set to the value of the last call.
func (b *ScannedControlStatusApplyConfiguration) WithSubStatus(value string) *ScannedControlStatusApplyConfiguration {
	b.SubStatus = &value
	return b
}

// WithInfo sets the Info field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Info field is set to the value of the last call.
func (b *ScannedControlStatusApplyConfiguration) WithInfo(value string) *ScannedControlStatusApplyConfiguration {
	b.Info = &value
	return b
}
