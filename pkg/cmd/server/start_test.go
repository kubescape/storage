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

// TestPersistentPreRunEKeepsWatchListEnabled pins the gate the file-based storage honours
// since https://github.com/kubescape/storage/issues/320: PersistentPreRunE leaves WatchList
// at its default, so the apiserver watch handler accepts sendInitialEvents watch requests
// and the storage replays the initial events followed by the initial-events-end bookmark.
func TestPersistentPreRunEKeepsWatchListEnabled(t *testing.T) {
	cmd := NewCommandStartWardleServer(context.TODO(), newTestOptions(), false)
	require.NoError(t, cmd.PersistentPreRunE(cmd, nil))
	assert.True(t, utilfeature.DefaultFeatureGate.Enabled(features.WatchList))

	opts := internalversion.ListOptions{
		Watch:                true,
		AllowWatchBookmarks:  true,
		SendInitialEvents:    ptr.To(true),
		ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
	}
	errs := metainternalversionvalidation.ValidateListOptions(&opts, utilfeature.DefaultFeatureGate.Enabled(features.WatchList))
	assert.Empty(t, errs, "sendInitialEvents watch requests must pass the handler-level validation")
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
