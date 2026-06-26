/*
Copyright 2024 The Kubescape Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package file

import (
	"context"
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// fakeCollapseStorage is the minimal storage.Interface that NewCRDCollapseSettingsProvider
// exercises — Get only. Everything else returns "not implemented" so that any
// accidental dependency surfaces immediately rather than silently no-oping.
type fakeCollapseStorage struct {
	storage.Interface // nil — panics on unimplemented methods if called
	stored            map[string]runtime.Object
	getErr            error
}

func (f *fakeCollapseStorage) Get(_ context.Context, key string, opts storage.GetOptions, out runtime.Object) error {
	if f.getErr != nil {
		return f.getErr
	}
	obj, ok := f.stored[key]
	if !ok {
		if opts.IgnoreNotFound {
			// Mimic the real storage IgnoreNotFound contract: zero the out and
			// return nil. Caller must distinguish "not found" via empty
			// ObjectMeta.Name.
			return nil
		}
		return storage.NewKeyNotFoundError(key, 0)
	}
	// Copy into out via reflect to satisfy the Get(out runtime.Object) contract.
	switch dst := out.(type) {
	case *softwarecomposition.CollapseConfiguration:
		src := obj.(*softwarecomposition.CollapseConfiguration)
		*dst = *src
	default:
		return fmt.Errorf("fakeCollapseStorage: unhandled out type %T", dst)
	}
	return nil
}

// Watch is required by storage.Interface but not exercised here.
func (f *fakeCollapseStorage) Watch(_ context.Context, _ string, _ storage.ListOptions) (watch.Interface, error) {
	return nil, fmt.Errorf("fakeCollapseStorage: Watch not implemented")
}

// TestCollapseConfigurationKey_MatchesClusterScopedRESTKey pins the exact
// in-storage key for the cluster-scoped CollapseConfiguration/default CR.
// It MUST equal the key the apiserver's genericregistry REST endpoint
// writes the CR under — NoNamespaceKeyFunc, i.e. /<root>/<resource>/<name>
// with NO namespace segment. A stray empty namespace segment (the
// historical bug: /<root>/<resource>//<name>) made the provider's Get miss
// the applied CR and silently fall back to defaults.
//
// The other provider tests store and read through this same helper, so
// they are self-consistent and cannot catch a key-shape regression — this
// test asserts the literal string instead.
func TestCollapseConfigurationKey_MatchesClusterScopedRESTKey(t *testing.T) {
	key := collapseConfigurationKey(DefaultCollapseConfigurationName)
	assert.Equal(t,
		"/spdx.softwarecomposition.kubescape.io/collapseconfigurations/default",
		key,
		"cluster-scoped key must have no namespace segment")
	assert.NotContains(t, key, "//",
		"key must not contain an empty (namespace) segment")
}

// TestNewCRDCollapseSettingsProvider_FallsBackOnAbsentCR pins matthyx's
// blocker fix: the provider must fall back to DefaultCollapseSettings when
// the CollapseConfiguration/default CR is not present in storage, so a
// fresh cluster boots with working thresholds before any operator applies
// the manifest.
func TestNewCRDCollapseSettingsProvider_FallsBackOnAbsentCR(t *testing.T) {
	s := &fakeCollapseStorage{stored: map[string]runtime.Object{}}
	provider := NewCRDCollapseSettingsProvider(s)
	require.NotNil(t, provider)

	got := provider()
	want := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, want.OpenDynamicThreshold, got.OpenDynamicThreshold, "OpenDynamicThreshold falls back to default")
	assert.Equal(t, want.EndpointDynamicThreshold, got.EndpointDynamicThreshold, "EndpointDynamicThreshold falls back to default")
	assert.Equal(t, want.CollapseConfigs, got.CollapseConfigs, "CollapseConfigs falls back to default")
}

// TestNewCRDCollapseSettingsProvider_ReadsAppliedCR pins the core wiring
// contract matthyx asked for: when a CollapseConfiguration manifest IS
// applied, the deflate path's effective settings reflect the CR rather
// than the compiled-in defaults. Without this wiring the CRD endpoint
// would be a no-op (matthyx review on apiserver.go:164, 2026-05-27).
func TestNewCRDCollapseSettingsProvider_ReadsAppliedCR(t *testing.T) {
	applied := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: DefaultCollapseConfigurationName},
		Spec: softwarecomposition.CollapseConfigurationSpec{
			OpenDynamicThreshold:     1234,
			EndpointDynamicThreshold: 5678,
			CollapseConfigs: []softwarecomposition.CollapseConfigEntry{
				{Prefix: "/etc", Threshold: 9},
				{Prefix: "/app", Threshold: 1},
			},
		},
	}
	s := &fakeCollapseStorage{
		stored: map[string]runtime.Object{
			collapseConfigurationKey(DefaultCollapseConfigurationName): applied,
		},
	}

	provider := NewCRDCollapseSettingsProvider(s)
	got := provider()

	assert.Equal(t, 1234, got.OpenDynamicThreshold)
	assert.Equal(t, 5678, got.EndpointDynamicThreshold)
	require.Len(t, got.CollapseConfigs, 2)
	assert.Equal(t, "/etc", got.CollapseConfigs[0].Prefix)
	assert.Equal(t, 9, got.CollapseConfigs[0].Threshold)
	assert.Equal(t, "/app", got.CollapseConfigs[1].Prefix)
	assert.Equal(t, 1, got.CollapseConfigs[1].Threshold)
}

// TestNewCRDCollapseSettingsProvider_NilStorageReturnsDefault pins the
// defensive contract: if a caller wires a nil storage the provider must
// silently degrade to defaults rather than panic at deflate time.
func TestNewCRDCollapseSettingsProvider_NilStorageReturnsDefault(t *testing.T) {
	provider := NewCRDCollapseSettingsProvider(nil)
	require.NotNil(t, provider)

	got := provider()
	want := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, want.OpenDynamicThreshold, got.OpenDynamicThreshold)
	assert.Equal(t, want.EndpointDynamicThreshold, got.EndpointDynamicThreshold)
	assert.Equal(t, want.CollapseConfigs, got.CollapseConfigs)
}

// TestNewCRDCollapseSettingsProvider_GetErrorFallsBackToDefault pins
// that transient storage errors do not crash the deflate path — the
// provider returns the compiled-in defaults so compaction continues.
func TestNewCRDCollapseSettingsProvider_GetErrorFallsBackToDefault(t *testing.T) {
	s := &fakeCollapseStorage{
		stored: map[string]runtime.Object{},
		getErr: fmt.Errorf("simulated read error"),
	}
	provider := NewCRDCollapseSettingsProvider(s)

	got := provider()
	want := dynamicpathdetector.DefaultCollapseSettings()
	assert.Equal(t, want.OpenDynamicThreshold, got.OpenDynamicThreshold)
}

// TestNewCRDCollapseSettingsProvider_LiveUpdate pins the no-cache
// design: edits to the CR take effect on the very next provider call,
// without restart or manual invalidation. bobctl autotune relies on
// this when it pushes tuned thresholds back into the cluster.
func TestNewCRDCollapseSettingsProvider_LiveUpdate(t *testing.T) {
	v1 := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: DefaultCollapseConfigurationName},
		Spec:       softwarecomposition.CollapseConfigurationSpec{OpenDynamicThreshold: 100},
	}
	s := &fakeCollapseStorage{
		stored: map[string]runtime.Object{
			collapseConfigurationKey(DefaultCollapseConfigurationName): v1,
		},
	}
	provider := NewCRDCollapseSettingsProvider(s)

	assert.Equal(t, 100, provider().OpenDynamicThreshold)

	// Operator edits the CR (or bobctl autotune writes a new value).
	s.stored[collapseConfigurationKey(DefaultCollapseConfigurationName)] = &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: DefaultCollapseConfigurationName},
		Spec:       softwarecomposition.CollapseConfigurationSpec{OpenDynamicThreshold: 200},
	}

	assert.Equal(t, 200, provider().OpenDynamicThreshold, "next call reflects the CR edit without invalidation")
}
