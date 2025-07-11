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

// ExecCallsApplyConfiguration represents a declarative configuration of the ExecCalls type for use
// with apply.
type ExecCallsApplyConfiguration struct {
	Path *string  `json:"path,omitempty"`
	Args []string `json:"args,omitempty"`
	Envs []string `json:"envs,omitempty"`
}

// ExecCallsApplyConfiguration constructs a declarative configuration of the ExecCalls type for use with
// apply.
func ExecCalls() *ExecCallsApplyConfiguration {
	return &ExecCallsApplyConfiguration{}
}

// WithPath sets the Path field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Path field is set to the value of the last call.
func (b *ExecCallsApplyConfiguration) WithPath(value string) *ExecCallsApplyConfiguration {
	b.Path = &value
	return b
}

// WithArgs adds the given value to the Args field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Args field.
func (b *ExecCallsApplyConfiguration) WithArgs(values ...string) *ExecCallsApplyConfiguration {
	for i := range values {
		b.Args = append(b.Args, values[i])
	}
	return b
}

// WithEnvs adds the given value to the Envs field in the declarative configuration
// and returns the receiver, so that objects can be build by chaining "With" function invocations.
// If called multiple times, values provided by each call will be appended to the Envs field.
func (b *ExecCallsApplyConfiguration) WithEnvs(values ...string) *ExecCallsApplyConfiguration {
	for i := range values {
		b.Envs = append(b.Envs, values[i])
	}
	return b
}
