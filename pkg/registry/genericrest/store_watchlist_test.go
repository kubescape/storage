package genericrest_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/tools/cache"
)

// storeListerWatcher drives a client-go reflector straight from the Store, without HTTP.
// It counts List calls: a watch-list sync has to complete without any.
type storeListerWatcher struct {
	store *genericrest.Store
	lists atomic.Int32
}

func (lw *storeListerWatcher) ListWithContext(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
	lw.lists.Add(1)
	opts, err := internalListOptions(options)
	if err != nil {
		return nil, err
	}
	return lw.store.List(genericapirequest.WithNamespace(ctx, ""), opts)
}

func (lw *storeListerWatcher) WatchWithContext(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
	opts, err := internalListOptions(options)
	if err != nil {
		return nil, err
	}
	return lw.store.Watch(genericapirequest.WithNamespace(ctx, ""), opts)
}

func (lw *storeListerWatcher) List(options metav1.ListOptions) (runtime.Object, error) {
	return lw.ListWithContext(context.Background(), options)
}

func (lw *storeListerWatcher) Watch(options metav1.ListOptions) (watch.Interface, error) {
	return lw.WatchWithContext(context.Background(), options)
}

func internalListOptions(options metav1.ListOptions) (*metainternalversion.ListOptions, error) {
	opts := &metainternalversion.ListOptions{}
	if err := metainternalversion.Convert_v1_ListOptions_To_internalversion_ListOptions(&options, opts, nil); err != nil {
		return nil, err
	}
	return opts, nil
}

// TestStore_WatchListSyncsReflectorWithoutList runs a client-go reflector in watch-list mode
// against the Store: the initial state has to arrive through the watch stream closed by the
// initial-events-end bookmark, and later writes through the same stream, with no List call.
func TestStore_WatchListSyncsReflectorWithoutList(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, true)
	store := newTestStore(t)
	ctx := testContext()
	for _, name := range []string{"first", "second"} {
		_, err := store.Create(ctx, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: name}}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
		require.NoError(t, err)
	}

	lw := &storeListerWatcher{store: store}
	cacheStore := cache.NewStore(cache.MetaNamespaceKeyFunc)
	reflector := cache.NewReflectorWithOptions(lw, &softwarecomposition.KnownServer{}, cacheStore, cache.ReflectorOptions{Name: "watch-list"})

	rctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = reflector.ListAndWatchWithContext(rctx)
	}()

	require.Eventually(t, func() bool { return len(cacheStore.ListKeys()) == 2 }, 5*time.Second, 10*time.Millisecond, "the reflector never finished its initial sync")
	assert.NotEmpty(t, reflector.LastSyncResourceVersion())
	assert.Zero(t, lw.lists.Load(), "the initial state must come from the watch stream, not from a list fallback")

	_, err := store.Create(ctx, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "third"}}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(cacheStore.ListKeys()) == 3 }, 5*time.Second, 10*time.Millisecond, "the stream did not deliver an object created after the bookmark")
	assert.Zero(t, lw.lists.Load())

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ListAndWatch did not return after cancellation")
	}
}
