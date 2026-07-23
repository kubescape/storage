/*
Copyright 2024 The Kubescape Authors.

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

package collapseconfiguration

import (
	"context"
	"fmt"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apiserver/pkg/storage/names"
)

// NewStrategy creates and returns a CollapseConfigurationStrategy instance.
func NewStrategy(typer runtime.ObjectTyper) CollapseConfigurationStrategy {
	return CollapseConfigurationStrategy{typer, names.SimpleNameGenerator}
}

// GetAttrs returns labels.Set, fields.Set, and error in case the given
// runtime.Object is not a CollapseConfiguration.
func GetAttrs(obj runtime.Object) (labels.Set, fields.Set, error) {
	cc, ok := obj.(*softwarecomposition.CollapseConfiguration)
	if !ok {
		return nil, nil, fmt.Errorf("given object is not a CollapseConfiguration")
	}
	return cc.Labels, SelectableFields(cc), nil
}

// MatchCollapseConfiguration returns a generic SelectionPredicate that pairs
// the supplied label/field selectors with the type's GetAttrs.
func MatchCollapseConfiguration(label labels.Selector, field fields.Selector) storage.SelectionPredicate {
	return storage.SelectionPredicate{
		Label:    label,
		Field:    field,
		GetAttrs: GetAttrs,
	}
}

// SelectableFields returns a field set that represents the object.
// CollapseConfiguration is cluster-scoped, so the namespaceScoped flag
// is false — `metadata.namespace` is intentionally absent from the
// selectable set.
func SelectableFields(obj *softwarecomposition.CollapseConfiguration) fields.Set {
	return generic.ObjectMetaFieldsSet(&obj.ObjectMeta, false)
}

// CollapseConfigurationStrategy carries the per-object lifecycle hooks the
// generic registry calls during Create/Update/Delete. CollapseConfiguration
// is cluster-scoped, has no immutable fields, and validates that each
// per-prefix entry has a non-empty Prefix and a positive Threshold.
type CollapseConfigurationStrategy struct {
	runtime.ObjectTyper
	names.NameGenerator
}

// NamespaceScoped declares the resource as cluster-scoped.
func (CollapseConfigurationStrategy) NamespaceScoped() bool {
	return false
}

func (CollapseConfigurationStrategy) PrepareForCreate(_ context.Context, _ runtime.Object) {
}

func (CollapseConfigurationStrategy) PrepareForUpdate(_ context.Context, _, _ runtime.Object) {
}

// Validate runs spec-level checks on a Create. Returns an empty list when the
// object is well-formed.
func (CollapseConfigurationStrategy) Validate(_ context.Context, obj runtime.Object) field.ErrorList {
	cc, ok := obj.(*softwarecomposition.CollapseConfiguration)
	if !ok {
		return field.ErrorList{field.InternalError(field.NewPath(""), fmt.Errorf("expected *CollapseConfiguration"))}
	}
	return validateCollapseConfigurationSpec(&cc.Spec, field.NewPath("spec"))
}

func (CollapseConfigurationStrategy) WarningsOnCreate(_ context.Context, _ runtime.Object) []string {
	return nil
}

func (CollapseConfigurationStrategy) AllowCreateOnUpdate() bool {
	return false
}

func (CollapseConfigurationStrategy) AllowUnconditionalUpdate() bool {
	return false
}

func (CollapseConfigurationStrategy) Canonicalize(_ runtime.Object) {
}

// ValidateUpdate runs the same spec-level checks as Validate; the spec is
// fully mutable on update.
func (CollapseConfigurationStrategy) ValidateUpdate(_ context.Context, obj, _ runtime.Object) field.ErrorList {
	cc, ok := obj.(*softwarecomposition.CollapseConfiguration)
	if !ok {
		return field.ErrorList{field.InternalError(field.NewPath(""), fmt.Errorf("expected *CollapseConfiguration"))}
	}
	return validateCollapseConfigurationSpec(&cc.Spec, field.NewPath("spec"))
}

func (CollapseConfigurationStrategy) WarningsOnUpdate(_ context.Context, _, _ runtime.Object) []string {
	return nil
}

// validateCollapseConfigurationSpec enforces the per-entry invariants and
// rejects duplicate prefixes (which would silently produce a non-deterministic
// longest-prefix-wins outcome at runtime).
//
// The global thresholds (OpenDynamicThreshold, EndpointDynamicThreshold,
// NetworkIPGroupThreshold) are optional and only rejected when negative: 0
// (or an omitted field) is a valid "use the compiled-in default" sentinel,
// honored by CollapseSettingsFromCRD. This is deliberately more permissive
// than the per-prefix entries (which require >= 1): a per-prefix entry is an
// explicit override and has no default to fall back to, whereas an omitted
// global threshold must not be treated as a literal 0 that collapses
// everything. NetworkCIDRFloorBits shares the same 0-means-default sentinel
// but additionally caps the valid non-zero range at [1,32], since it encodes
// a CIDR prefix length rather than an unbounded count.
func validateCollapseConfigurationSpec(spec *softwarecomposition.CollapseConfigurationSpec, fp *field.Path) field.ErrorList {
	var errs field.ErrorList
	if spec.OpenDynamicThreshold < 0 {
		errs = append(errs, field.Invalid(fp.Child("openDynamicThreshold"), spec.OpenDynamicThreshold, "must be >= 0 (0 means use the compiled-in default)"))
	}
	if spec.EndpointDynamicThreshold < 0 {
		errs = append(errs, field.Invalid(fp.Child("endpointDynamicThreshold"), spec.EndpointDynamicThreshold, "must be >= 0 (0 means use the compiled-in default)"))
	}
	if spec.NetworkIPGroupThreshold < 0 {
		errs = append(errs, field.Invalid(fp.Child("networkIPGroupThreshold"), spec.NetworkIPGroupThreshold, "must be >= 0 (0 means use the compiled-in default)"))
	}
	if spec.NetworkCIDRFloorBits < 0 || spec.NetworkCIDRFloorBits > 32 {
		errs = append(errs, field.Invalid(fp.Child("networkCIDRFloorBits"), spec.NetworkCIDRFloorBits, "must be 0 (use the compiled-in default) or in the range [1,32]"))
	}
	seen := make(map[string]int, len(spec.CollapseConfigs))
	cfgsPath := fp.Child("collapseConfigs")
	for i, e := range spec.CollapseConfigs {
		ip := cfgsPath.Index(i)
		if e.Prefix == "" {
			errs = append(errs, field.Required(ip.Child("prefix"), "prefix must not be empty"))
		} else if !strings.HasPrefix(e.Prefix, "/") {
			errs = append(errs, field.Invalid(ip.Child("prefix"), e.Prefix, "prefix must begin with /"))
		}
		if e.Threshold < 1 {
			errs = append(errs, field.Invalid(ip.Child("threshold"), e.Threshold, "must be >= 1"))
		}
		if dup, ok := seen[e.Prefix]; ok {
			errs = append(errs, field.Duplicate(ip.Child("prefix"), fmt.Sprintf("%s (also at index %d)", e.Prefix, dup)))
		} else {
			seen[e.Prefix] = i
		}
	}
	return errs
}
