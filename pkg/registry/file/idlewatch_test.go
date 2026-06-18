package file

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// assertStaysOpen asserts that w neither emits an event nor closes within chanWaitTimeout.
func assertStaysOpen(t *testing.T, w watch.Interface) {
	t.Helper()
	select {
	case ev, ok := <-w.ResultChan():
		if ok {
			t.Fatalf("idle watch must not emit events, got %v", ev)
		}
		t.Fatal("idle watch must not close before ctx cancellation or Stop")
	case <-time.After(chanWaitTimeout):
	}
}

// assertClosed asserts that w's result channel closes within chanWaitTimeout.
func assertClosed(t *testing.T, w watch.Interface) {
	t.Helper()
	select {
	case ev, ok := <-w.ResultChan():
		assert.False(t, ok, "idle watch must close without emitting events, got %v", ev)
	case <-time.After(chanWaitTimeout):
		t.Fatal("idle watch channel should be closed")
	}
}

func TestIdleWatchClosesOnCtxCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	w := newIdleWatch(ctx)
	assertStaysOpen(t, w)
	cancel()
	assertClosed(t, w)
}

func TestIdleWatchStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	w := newIdleWatch(ctx)
	// Stop before ctx cancellation must close the channel (a fast-disconnecting client
	// reaches the handler's deferred Stop before the request context unwinds).
	w.Stop()
	assertClosed(t, w)
	// Double Stop and a late ctx cancellation must both be safe.
	w.Stop()
	cancel()
	assertClosed(t, w)
}

// TestStorageImplWatchNamespacedKeyIsIdle covers the namespaced-key path of
// StorageImpl.Watch, which used to return a pre-closed watch.NewEmptyWatch() and send
// reflectors into a "very short watch" tight retry loop (issue #318).
func TestStorageImplWatchNamespacedKeyIsIdle(t *testing.T) {
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, nil, nil, nil)
	ctx, cancel := context.WithCancel(context.TODO())
	w, err := s.Watch(ctx, "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape", storage.ListOptions{})
	require.NoError(t, err)
	assertStaysOpen(t, w)
	cancel()
	assertClosed(t, w)
}

// TestImmutableStorageWatchIsIdle covers immutableStorage.Watch (ConfigurationScanSummary,
// VulnerabilitySummary, GeneratedNetworkPolicy), the other pre-closed watch site of #318.
func TestImmutableStorageWatchIsIdle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	w, err := immutableStorage{}.Watch(ctx, "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries", storage.ListOptions{})
	require.NoError(t, err)
	assertStaysOpen(t, w)
	cancel()
	assertClosed(t, w)
}

// TestIdleWatchSpawnsNoGoroutines proves the zero-goroutine property: hundreds of concurrent
// idle watches (Rancher steve watches every GVK) must not inflate the goroutine count, and
// cancellation must return to baseline without leaks.
func TestIdleWatchSpawnsNoGoroutines(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	const n = 200
	baseline := runtime.NumGoroutine()
	cancels := make([]context.CancelFunc, 0, n)
	watches := make([]*idleWatch, 0, n)
	for range n {
		ctx, cancel := context.WithCancel(context.TODO())
		watches = append(watches, newIdleWatch(ctx))
		cancels = append(cancels, cancel)
	}
	steady := runtime.NumGoroutine()
	assert.LessOrEqual(t, steady, baseline+2, "idle watches must not spawn goroutines")

	for _, cancel := range cancels {
		cancel()
	}
	for _, w := range watches {
		assertClosed(t, w)
	}
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baseline+2
	}, time.Second, 10*time.Millisecond, "goroutine count must return to baseline after cancellation")
}
