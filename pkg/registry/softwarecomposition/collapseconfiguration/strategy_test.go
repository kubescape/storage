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
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// runtimeSchemeStub is the minimal ObjectTyper the strategy embeds; we never
// actually call its methods in these tests, so a plain new scheme suffices.
func newScheme() runtime.ObjectTyper {
	return runtime.NewScheme()
}

func TestNamespaceScoped(t *testing.T) {
	s := NewStrategy(newScheme())
	if s.NamespaceScoped() {
		t.Fatalf("CollapseConfiguration must be cluster-scoped")
	}
}

func TestValidate_Valid(t *testing.T) {
	s := NewStrategy(newScheme())
	cc := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "default"},
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     50,
			EndpointDynamicThreshold: 100,
			CollapseConfigs: []softwarecomposition.CollapseConfigEntry{
				{Prefix: "/etc", Threshold: 100},
				{Prefix: "/var/log", Threshold: 50},
				{Prefix: "/opt", Threshold: 50},
			},
		},
	}
	if errs := s.Validate(context.Background(), cc); len(errs) != 0 {
		t.Fatalf("expected no validation errors, got: %v", errs)
	}
}

func TestValidate_NegativeThresholds(t *testing.T) {
	s := NewStrategy(newScheme())
	cc := &softwarecomposition.CollapseConfiguration{
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     -1,
			EndpointDynamicThreshold: -1,
		},
	}
	errs := s.Validate(context.Background(), cc)
	if len(errs) != 2 {
		t.Fatalf("expected 2 errors for the two negative defaults, got %d: %v", len(errs), errs)
	}
}

func TestValidate_EntryRules(t *testing.T) {
	s := NewStrategy(newScheme())
	cc := &softwarecomposition.CollapseConfiguration{
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     50,
			EndpointDynamicThreshold: 100,
			CollapseConfigs: []softwarecomposition.CollapseConfigEntry{
				{Prefix: "", Threshold: 50},          // empty prefix
				{Prefix: "etc", Threshold: 50},       // missing leading slash
				{Prefix: "/opt", Threshold: 0},       // threshold below 1
				{Prefix: "/etc", Threshold: 100},     // first /etc
				{Prefix: "/etc", Threshold: 50},      // duplicate /etc
			},
		},
	}
	errs := s.Validate(context.Background(), cc)
	// We expect 4 errors: empty prefix, missing leading slash, threshold<1,
	// duplicate. (The first /etc entry is fine on its own.)
	if len(errs) < 4 {
		t.Fatalf("expected at least 4 entry-level errors, got %d: %v", len(errs), errs)
	}
}

func TestValidate_RejectsNonCC(t *testing.T) {
	s := NewStrategy(newScheme())
	// Pass a different type to confirm the type assertion fails cleanly.
	notACC := &softwarecomposition.ApplicationProfile{}
	errs := s.Validate(context.Background(), notACC)
	if len(errs) != 1 {
		t.Fatalf("expected 1 internal error for type mismatch, got: %v", errs)
	}
}

func TestValidateUpdate_SameRules(t *testing.T) {
	s := NewStrategy(newScheme())
	old := &softwarecomposition.CollapseConfiguration{Spec: softwarecomposition.CollapseConfigurationSpec{OpenDynamicThreshold: 50}}
	updated := &softwarecomposition.CollapseConfiguration{
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold: 50,
			CollapseConfigs: []softwarecomposition.CollapseConfigEntry{
				{Prefix: "/etc", Threshold: -1}, // bad threshold on update
			},
		},
	}
	errs := s.ValidateUpdate(context.Background(), updated, old)
	if len(errs) == 0 {
		t.Fatalf("expected ValidateUpdate to flag threshold < 1")
	}
}

func TestSelectableFieldsAndAttrs(t *testing.T) {
	cc := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Labels: map[string]string{"k": "v"}},
	}
	lbl, _, err := GetAttrs(cc)
	if err != nil {
		t.Fatalf("GetAttrs: %v", err)
	}
	if lbl.Get("k") != "v" {
		t.Fatalf("labels round-trip broken: got %q", lbl.Get("k"))
	}
	// Sanity: SelectableFields includes the name.
	fs := SelectableFields(cc)
	if fs.Get("metadata.name") != "default" {
		t.Fatalf("SelectableFields name = %q, want %q", fs.Get("metadata.name"), "default")
	}
}

func TestGetAttrs_RejectsNonCC(t *testing.T) {
	notACC := &softwarecomposition.ApplicationProfile{}
	_, _, err := GetAttrs(notACC)
	if err == nil {
		t.Fatalf("GetAttrs should reject non-CollapseConfiguration objects")
	}
}
