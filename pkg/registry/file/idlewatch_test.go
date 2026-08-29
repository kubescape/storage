package file

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
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

// TestStorageImplWatchNamespacedKeyDeliversRealEvents covers the namespaced-key path of
// StorageImpl.Watch. It used to reject namespaced keys outright (first with an error, later
// with a pre-closed watch.NewEmptyWatch() that sent reflectors into a "very short watch" tight
// retry loop, issue #318) as a workaround for namespaces getting stuck in Terminating.
// WatchDispatcher.Register/notify (watch.go) match purely on path-prefix strings and have
// never treated a namespaced key any differently from a cluster-scoped one -- a namespace-root
// watch key is itself one of the path-prefix ancestors extractKeysToNotify walks for any object
// created under that namespace -- so namespace-scoped watches now deliver real events through
// the exact same dispatch mechanism already proven correct for cluster-scoped resources.
func TestStorageImplWatchNamespacedKeyDeliversRealEvents(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)

	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	const nsRootKey = "/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape"
	w, err := s.Watch(ctx, nsRootKey, storage.ListOptions{})
	require.NoError(t, err)
	assertStaysOpen(t, w) // no event yet -- still a genuinely idle-looking watch, distinguishing "no events" from "broken watch"

	obj := &v1beta1.OpenVulnerabilityExchangeContainer{ObjectMeta: v1.ObjectMeta{Name: "some-vex"}}
	out := &v1beta1.OpenVulnerabilityExchangeContainer{}
	require.NoError(t, s.Create(ctx, nsRootKey+"/some-vex", obj, out, 0))

	select {
	case ev, ok := <-w.ResultChan():
		require.True(t, ok, "namespace-scoped watch must deliver a real event, not a closed channel")
		assert.Equal(t, watch.Added, ev.Type)
	case <-time.After(chanWaitTimeout):
		t.Fatal("namespace-scoped watch did not deliver the Added event for an object created in its namespace")
	}

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
