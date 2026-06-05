package server

import (
	"context"
	"io"
	"testing"

	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metainternalversionvalidation "k8s.io/apimachinery/pkg/apis/meta/internalversion/validation"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	basecompatibility "k8s.io/component-base/compatibility"
	"k8s.io/utils/ptr"
)

// newTestOptions returns WardleServerOptions with a fresh ComponentGlobalsRegistry so that
// each test can construct its own command: the registry's AddFlags closes its feature gates,
// so re-registering the Wardle component against the shared default registry would panic.
// Note that the kube component still maps to the process-global
// utilfeature.DefaultMutableFeatureGate, so gate state leaks between tests by design.
func newTestOptions() *WardleServerOptions {
	o := NewWardleServerOptions(io.Discard, io.Discard, afero.NewMemMapFs(), nil, config.Config{}, nil, nil)
	o.ComponentGlobalsRegistry = basecompatibility.NewComponentGlobalsRegistry()
	return o
}

// TestPersistentPreRunESkipLeavesGatesUntouched guards the contract relied upon by callers
// passing skipDefaultComponentGlobalsRegistrySet=true (e.g. integration tests): no feature
// gate is modified.
func TestPersistentPreRunESkipLeavesGatesUntouched(t *testing.T) {
	cmd := NewCommandStartWardleServer(context.TODO(), newTestOptions(), true)
	before := utilfeature.DefaultFeatureGate.Enabled(features.WatchList)
	require.NoError(t, cmd.PersistentPreRunE(cmd, nil))
	assert.Equal(t, before, utilfeature.DefaultFeatureGate.Enabled(features.WatchList))
}

// TestPersistentPreRunEDisablesWatchList is the regression test for the feature-gate
// ordering bug behind https://github.com/kubescape/storage/issues/318: the flag value used
// to be populated only after ComponentGlobalsRegistry.Set() had already run, so the
// WatchList override silently never applied. After the fix, PersistentPreRunE must succeed
// (boot safety) and the gate must be effective at request time, which makes the apiserver
// watch handler (k8s.io/apiserver/pkg/endpoints/handlers/get.go ListResource) reject
// sendInitialEvents watch requests before the stream opens (HTTP 422). WatchList-enabled
// clients (client-go >= v0.35 reflectors, Rancher) then use legacy list+watch instead of
// hanging while awaiting an initial-events-end bookmark that the file-based storage never
// sends.
func TestPersistentPreRunEDisablesWatchList(t *testing.T) {
	cmd := NewCommandStartWardleServer(context.TODO(), newTestOptions(), false)
	require.NoError(t, cmd.PersistentPreRunE(cmd, nil))
	assert.False(t, utilfeature.DefaultFeatureGate.Enabled(features.WatchList))

	// Assert the exact decision point the watch handler uses with the now-effective gate.
	opts := internalversion.ListOptions{
		Watch:                true,
		AllowWatchBookmarks:  true,
		SendInitialEvents:    ptr.To(true),
		ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
	}
	errs := metainternalversionvalidation.ValidateListOptions(&opts, utilfeature.DefaultFeatureGate.Enabled(features.WatchList))
	require.NotEmpty(t, errs, "sendInitialEvents watch requests must be rejected pre-stream when WatchList is disabled")
	assert.Contains(t, errs.ToAggregate().Error(), "sendInitialEvents is forbidden for watch")
}

// TestPersistentPreRunERejectsUnknownGate ensures a stale feature-gate token (such as
// ServerSideApply, which is GA and non-gated since Kubernetes 1.35) fails loudly at boot
// instead of being silently ignored.
//
// This test MUST remain the last one in this file: the rejected raw override is retained by
// the process-global kube feature gate and makes any subsequent NewCommandStartWardleServer
// call panic when registration re-validates the stored overrides.
func TestPersistentPreRunERejectsUnknownGate(t *testing.T) {
	cmd := NewCommandStartWardleServer(context.TODO(), newTestOptions(), false)
	require.NoError(t, cmd.Flags().Set("feature-gates", "ServerSideApply=false"))
	err := cmd.PersistentPreRunE(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unrecognized feature gate")
}
