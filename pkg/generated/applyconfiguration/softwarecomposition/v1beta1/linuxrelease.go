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
)

// LinuxReleaseApplyConfiguration represents a declarative configuration of the LinuxRelease type for use
// with apply.
type LinuxReleaseApplyConfiguration struct {
	PrettyName       *string                             `json:"prettyName,omitempty"`
	Name             *string                             `json:"name,omitempty"`
	ID               *string                             `json:"id,omitempty"`
	IDLike           *softwarecompositionv1beta1.IDLikes `json:"idLike,omitempty"`
	Version          *string                             `json:"version,omitempty"`
	VersionID        *string                             `json:"versionID,omitempty"`
	VersionCodename  *string                             `json:"versionCodename,omitempty"`
	BuildID          *string                             `json:"buildID,omitempty"`
	ImageID          *string                             `json:"imageID,omitempty"`
	ImageVersion     *string                             `json:"imageVersion,omitempty"`
	Variant          *string                             `json:"variant,omitempty"`
	VariantID        *string                             `json:"variantID,omitempty"`
	HomeURL          *string                             `json:"homeURL,omitempty"`
	SupportURL       *string                             `json:"supportURL,omitempty"`
	BugReportURL     *string                             `json:"bugReportURL,omitempty"`
	PrivacyPolicyURL *string                             `json:"privacyPolicyURL,omitempty"`
	CPEName          *string                             `json:"cpeName,omitempty"`
	SupportEnd       *string                             `json:"supportEnd,omitempty"`
}

// LinuxReleaseApplyConfiguration constructs a declarative configuration of the LinuxRelease type for use with
// apply.
func LinuxRelease() *LinuxReleaseApplyConfiguration {
	return &LinuxReleaseApplyConfiguration{}
}

// WithPrettyName sets the PrettyName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PrettyName field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithPrettyName(value string) *LinuxReleaseApplyConfiguration {
	b.PrettyName = &value
	return b
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithName(value string) *LinuxReleaseApplyConfiguration {
	b.Name = &value
	return b
}

// WithID sets the ID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ID field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithID(value string) *LinuxReleaseApplyConfiguration {
	b.ID = &value
	return b
}

// WithIDLike sets the IDLike field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the IDLike field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithIDLike(value softwarecompositionv1beta1.IDLikes) *LinuxReleaseApplyConfiguration {
	b.IDLike = &value
	return b
}

// WithVersion sets the Version field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Version field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithVersion(value string) *LinuxReleaseApplyConfiguration {
	b.Version = &value
	return b
}

// WithVersionID sets the VersionID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the VersionID field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithVersionID(value string) *LinuxReleaseApplyConfiguration {
	b.VersionID = &value
	return b
}

// WithVersionCodename sets the VersionCodename field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the VersionCodename field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithVersionCodename(value string) *LinuxReleaseApplyConfiguration {
	b.VersionCodename = &value
	return b
}

// WithBuildID sets the BuildID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BuildID field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithBuildID(value string) *LinuxReleaseApplyConfiguration {
	b.BuildID = &value
	return b
}

// WithImageID sets the ImageID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ImageID field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithImageID(value string) *LinuxReleaseApplyConfiguration {
	b.ImageID = &value
	return b
}

// WithImageVersion sets the ImageVersion field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the ImageVersion field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithImageVersion(value string) *LinuxReleaseApplyConfiguration {
	b.ImageVersion = &value
	return b
}

// WithVariant sets the Variant field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Variant field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithVariant(value string) *LinuxReleaseApplyConfiguration {
	b.Variant = &value
	return b
}

// WithVariantID sets the VariantID field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the VariantID field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithVariantID(value string) *LinuxReleaseApplyConfiguration {
	b.VariantID = &value
	return b
}

// WithHomeURL sets the HomeURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the HomeURL field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithHomeURL(value string) *LinuxReleaseApplyConfiguration {
	b.HomeURL = &value
	return b
}

// WithSupportURL sets the SupportURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SupportURL field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithSupportURL(value string) *LinuxReleaseApplyConfiguration {
	b.SupportURL = &value
	return b
}

// WithBugReportURL sets the BugReportURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the BugReportURL field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithBugReportURL(value string) *LinuxReleaseApplyConfiguration {
	b.BugReportURL = &value
	return b
}

// WithPrivacyPolicyURL sets the PrivacyPolicyURL field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the PrivacyPolicyURL field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithPrivacyPolicyURL(value string) *LinuxReleaseApplyConfiguration {
	b.PrivacyPolicyURL = &value
	return b
}

// WithCPEName sets the CPEName field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the CPEName field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithCPEName(value string) *LinuxReleaseApplyConfiguration {
	b.CPEName = &value
	return b
}

// WithSupportEnd sets the SupportEnd field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the SupportEnd field is set to the value of the last call.
func (b *LinuxReleaseApplyConfiguration) WithSupportEnd(value string) *LinuxReleaseApplyConfiguration {
	b.SupportEnd = &value
	return b
}
