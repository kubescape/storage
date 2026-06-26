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

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"k8s.io/apiserver/pkg/storage"
)

// DefaultCollapseConfigurationName is the cluster-scoped CR name the
// deflate path reads to learn effective collapse thresholds. Operators
// (and the bobctl autotune flow) write/edit this CR; if it is absent
// the provider falls back to dynamicpathdetector.DefaultCollapseSettings.
const DefaultCollapseConfigurationName = "default"

// collapseConfigurationKey is the in-storage key for the cluster-scoped
// CollapseConfiguration/default CR. It must match exactly the key the
// apiserver's REST endpoint writes the CR under, otherwise the provider's
// Get misses the applied CR and silently falls back to defaults.
//
// CollapseConfiguration is cluster-scoped (NamespaceScoped() == false in
// pkg/registry/softwarecomposition/collapseconfiguration/strategy.go), so
// the genericregistry NoNamespaceKeyFunc keys it as
// /<root>/<resource>/<name> with NO namespace segment. We use the
// cluster-scoped key helper rather than K8sKeysToPath, whose unconditional
// namespace segment would yield a stray empty segment for a cluster-scoped
// kind (/<root>/<resource>//<name>) that does not match the stored key.
func collapseConfigurationKey(name string) string {
	return K8sClusterScopedKeysToPath("", "spdx.softwarecomposition.kubescape.io", "collapseconfigurations", name)
}

// NewCRDCollapseSettingsProvider returns a CollapseSettingsProvider
// closure that, on each invocation, looks up the cluster-scoped
// CollapseConfiguration/<DefaultCollapseConfigurationName> in storage
// and projects it via dynamicpathdetector.CollapseSettingsFromCRD. If
// the CR is missing, unreadable, or storage is nil, the provider
// returns dynamicpathdetector.DefaultCollapseSettings so the deflate
// path always has working thresholds.
//
// This is the wire between the apiserver's CRD endpoint (registered at
// /apis/.../collapseconfigurations in pkg/apiserver/apiserver.go) and
// the in-process application/container profile processors that perform
// compaction. Without this provider the CRD is stored but never
// consulted — applying a CollapseConfiguration manifest would be a
// no-op (matthyx review on pkg/apiserver/apiserver.go:164, 2026-05-27).
//
// The closure performs a storage Get per call rather than caching, so
// edits to the CR take effect on the next deflate without restart or
// manual invalidation. Deflate frequency is low compared to disk Get
// latency, so the simplicity wins; if benchmarks ever surface this
// as hot, wrap with a watched cache.
func NewCRDCollapseSettingsProvider(s storage.Interface) dynamicpathdetector.CollapseSettingsProvider {
	if s == nil {
		return dynamicpathdetector.DefaultCollapseSettings
	}
	key := collapseConfigurationKey(DefaultCollapseConfigurationName)
	return func() dynamicpathdetector.CollapseSettings {
		crd := &softwarecomposition.CollapseConfiguration{}
		// IgnoreNotFound returns the zero-valued CR with nil error when
		// the CR is missing — the operator hasn't applied a manifest
		// yet, which is the common bootstrap case. Distinguish by
		// checking ObjectMeta.Name (the storage layer only populates
		// it when a real CR was decoded).
		err := s.Get(context.Background(), key, storage.GetOptions{IgnoreNotFound: true}, crd)
		if err != nil || crd.Name == "" {
			return dynamicpathdetector.DefaultCollapseSettings()
		}
		return dynamicpathdetector.CollapseSettingsFromCRD(crd)
	}
}
