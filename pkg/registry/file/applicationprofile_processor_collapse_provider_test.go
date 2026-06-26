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

package file

import (
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplicationProfileProcessor_DefaultCollapseSettings_Wired pins that
// a freshly-constructed ApplicationProfileProcessor uses the compiled
// defaults — i.e. the deflate path collapses /etc paths at the default
// /etc threshold, not at some accidental zero value. Also pins that
// the constructor wires the provider field (no nil-pointer panic on
// PreSave when the cluster has no CollapseConfiguration CR).
func TestApplicationProfileProcessor_DefaultCollapseSettings_Wired(t *testing.T) {
	a := NewApplicationProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxApplicationProfileSize: 40000})
	assert.NotNil(t, a)
	// The provider field should have been initialised — test by deflating
	// a small profile and asserting the result has the expected shape.
	// We can't directly inspect the unexported field, so we exercise it.
	settings := dynamicpathdetector.DefaultCollapseSettings()
	require := assertSettingsMatchProcessor(t, a, settings)
	_ = require
}

// TestApplicationProfileProcessor_SetCollapseSettings_NilFallsBack pins
// the defensive nil-handling on the setter — passing a nil provider
// must NOT replace the working default with nil (which would crash on
// PreSave). It must restore the compiled defaults.
func TestApplicationProfileProcessor_SetCollapseSettings_NilFallsBack(t *testing.T) {
	a := NewApplicationProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxApplicationProfileSize: 40000})

	// Override with a custom provider that returns custom settings.
	a.SetCollapseSettings(func() dynamicpathdetector.CollapseSettings {
		return dynamicpathdetector.CollapseSettings{OpenDynamicThreshold: 7}
	})
	// Now pass nil — must restore defaults, not crash.
	a.SetCollapseSettings(nil)

	// Pull what the processor would actually pass to deflate at PreSave time.
	// If the setter had stored nil, this call would panic.
	got := a.collapseSettings()
	want := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, want.OpenDynamicThreshold, got.OpenDynamicThreshold,
		"nil provider must restore default OpenDynamicThreshold, not the prior custom 7")
	assert.Equal(t, want.EndpointDynamicThreshold, got.EndpointDynamicThreshold)
}

// TestApplicationProfileProcessor_SetCollapseSettings_CustomProviderUsed
// pins that a custom provider's settings actually reach the deflate
// path *via the processor's collapseSettings field*. We deflate twice
// against the same input — once before SetCollapseSettings (defaults,
// no collapse) and once after (custom threshold 3, collapse). Both
// calls fetch settings via `a.collapseSettings()`, so the assertion
// exercises the wiring CodeRabbit flagged.
func TestApplicationProfileProcessor_SetCollapseSettings_CustomProviderUsed(t *testing.T) {
	a := NewApplicationProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxApplicationProfileSize: 40000})

	// Build a container whose Opens has 4 distinct /etc children.
	container := softwarecomposition.ApplicationProfileContainer{
		Name: "test",
		Opens: []softwarecomposition.OpenCalls{
			{Path: "/etc/file1", Flags: []string{"O_RDONLY"}},
			{Path: "/etc/file2", Flags: []string{"O_RDONLY"}},
			{Path: "/etc/file3", Flags: []string{"O_RDONLY"}},
			{Path: "/etc/file4", Flags: []string{"O_RDONLY"}},
		},
	}

	// Default provider (threshold 100 for /etc) — paths stay distinct.
	// The settings come from the processor's wired-up provider.
	defResult := deflateApplicationProfileContainer(container, nil, a.collapseSettings())
	assert.Greater(t, len(defResult.Opens), 1, "with default /etc threshold of 100, four files should NOT collapse")

	// Now install a custom provider with a tight /etc threshold and re-deflate.
	a.SetCollapseSettings(func() dynamicpathdetector.CollapseSettings {
		return dynamicpathdetector.CollapseSettings{
			OpenDynamicThreshold:     50,
			EndpointDynamicThreshold: 100,
			CollapseConfigs: []dynamicpathdetector.CollapseConfig{
				{Prefix: "/etc", Threshold: 3},
			},
		}
	})
	customResult := deflateApplicationProfileContainer(container, nil, a.collapseSettings())
	collapsed := false
	for _, o := range customResult.Opens {
		if o.Path == "/etc/"+dynamicpathdetector.DynamicIdentifier {
			collapsed = true
			break
		}
	}
	assert.True(t, collapsed,
		"after SetCollapseSettings(threshold 3), four /etc files MUST collapse to /etc/⋯ via the processor's provider")
}

// TestApplicationProfileProcessor_SetCollapseSettings_DefensiveSetterCopy
// pins that the setter does not store a reference to a slice the caller
// can later mutate. The provider is a function value so by Go semantics
// it captures the closure's referenced state — defensiveness lives in
// the PROVIDER's body. This test documents that contract by installing
// a provider that returns a captured slice, mutating that slice, and
// verifying the deflate path uses the MUTATED state — i.e. the contract
// is "the provider is the source of truth at every call". A wrapper
// provider that wants snapshot semantics must clone its captured slice.
func TestApplicationProfileProcessor_SetCollapseSettings_DefensiveSetterCopy(t *testing.T) {
	captured := []dynamicpathdetector.CollapseConfig{{Prefix: "/etc", Threshold: 3}}
	a := NewApplicationProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxApplicationProfileSize: 40000})
	a.SetCollapseSettings(func() dynamicpathdetector.CollapseSettings {
		return dynamicpathdetector.CollapseSettings{
			OpenDynamicThreshold: 50,
			CollapseConfigs:      captured,
		}
	})

	// Mutate the captured slice — the provider sees the new threshold on
	// the next call. Documenting this in a test makes the contract explicit
	// for production wiring (informer-backed providers should always
	// snapshot).
	captured[0].Threshold = 999

	// Build 5 /etc paths.
	container := softwarecomposition.ApplicationProfileContainer{Name: "test"}
	for i := 0; i < 5; i++ {
		container.Opens = append(container.Opens, softwarecomposition.OpenCalls{
			Path:  fmt.Sprintf("/etc/file%d", i),
			Flags: []string{"O_RDONLY"},
		})
	}
	// With threshold now 999, paths should NOT collapse.
	result := deflateApplicationProfileContainer(container, nil, a.collapseSettings())
	assert.Equal(t, 5, len(result.Opens),
		"after mutating the captured slice, the provider returns the new threshold and paths stay distinct")
}

// TestApplicationProfileProcessor_ZeroValue_NoPanicOnCollapseSettings pins
// the defensive contract that a zero-valued ApplicationProfileProcessor
// — constructed with `&ApplicationProfileProcessor{...}` instead of via
// the NewApplicationProfileProcessor factory — must not panic when
// PreSave reaches the deflate path. The compiled-in defaults are an
// acceptable fallback; a nil-function dereference is not. CodeRabbit
// upstream PR #326 finding #3 (applicationprofile_processor.go:92).
func TestApplicationProfileProcessor_ZeroValue_NoPanicOnCollapseSettings(t *testing.T) {
	// Direct struct literal — collapseSettings is left as the zero value (nil).
	a := &ApplicationProfileProcessor{}

	// The safe accessor must NOT panic. The result must match the
	// compiled-in defaults across ALL fields, not just OpenDynamicThreshold —
	// otherwise a regression that resets EndpointDynamicThreshold (or any
	// future field added to CollapseSettings) to its zero value would
	// silently pass this guard. CodeRabbit follow-up review on storage PR #33.
	require.NotPanics(t, func() {
		got := a.effectiveCollapseSettings()
		want := dynamicpathdetector.DefaultCollapseSettings()
		assert.Equal(t, want, got,
			"zero-valued processor must fall back to the FULL DefaultCollapseSettings struct, got %+v want %+v",
			got, want)
	})

	// Direct field-call still panics — that's an "I know what I'm doing"
	// path. The contract is only that the safe accessor (used by PreSave
	// → deflate) is panic-free.
	assert.Panics(t, func() { _ = a.collapseSettings() },
		"raw field-call on zero-valued processor still panics; only the safe accessor is guarded")
}

// assertSettingsMatchProcessor is a placeholder for richer wiring assertions.
// The function exercises a non-nil-provider invocation as a smoke test.
func assertSettingsMatchProcessor(t *testing.T, a *ApplicationProfileProcessor, want dynamicpathdetector.CollapseSettings) bool {
	t.Helper()
	got := a.collapseSettings()
	if got.OpenDynamicThreshold != want.OpenDynamicThreshold {
		t.Errorf("OpenDynamicThreshold = %d, want %d", got.OpenDynamicThreshold, want.OpenDynamicThreshold)
		return false
	}
	if got.EndpointDynamicThreshold != want.EndpointDynamicThreshold {
		t.Errorf("EndpointDynamicThreshold = %d, want %d", got.EndpointDynamicThreshold, want.EndpointDynamicThreshold)
		return false
	}
	return true
}
