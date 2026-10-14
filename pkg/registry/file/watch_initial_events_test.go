package file

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/utils/ptr"
)

const vexRootKey = "/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape"

func newWatchListStorage(t *testing.T) StorageQuerier {
	t.Helper()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	return NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
}

func createVEX(t *testing.T, ctx context.Context, s StorageQuerier, name string, lbls map[string]string) {
	t.Helper()
	obj := &softwarecomposition.OpenVulnerabilityExchangeContainer{
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "kubescape", Labels: lbls},
		Spec:       softwarecomposition.VEX{Metadata: softwarecomposition.Metadata{ID: "vex-" + name}},
	}
	require.NoError(t, s.Create(ctx, vexRootKey+"/"+name, obj, &softwarecomposition.OpenVulnerabilityExchangeContainer{}, 0))
}

func watchListOptions(bookmarks bool) storage.ListOptions {
	return storage.ListOptions{
		Recursive:         true,
		SendInitialEvents: ptr.To(true),
		Predicate: storage.SelectionPredicate{
			Label:               labels.Everything(),
			Field:               fields.Everything(),
			AllowWatchBookmarks: bookmarks,
		},
	}
}

func nextEvent(t *testing.T, w watch.Interface) watch.Event {
	t.Helper()
	select {
	case ev, ok := <-w.ResultChan():
		require.True(t, ok, "watch closed before the expected event")
		return ev
	case <-time.After(chanWaitTimeout):
		t.Fatal("no event within chanWaitTimeout")
	}
	return watch.Event{}
}

func objectName(t *testing.T, obj interface{}) string {
	t.Helper()
	acc, err := meta.Accessor(obj)
	require.NoError(t, err)
	return acc.GetName()
}

func TestWatchListReplaysObjectsThenBookmarkThenLiveEvents(t *testing.T) {
	s := newWatchListStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()
	createVEX(t, ctx, s, "a", nil)
	createVEX(t, ctx, s, "b", nil)

	w, err := s.Watch(ctx, vexRootKey, watchListOptions(true))
	require.NoError(t, err)
	defer w.Stop()

	var names []string
	var maxRV uint64
	for i := 0; i < 2; i++ {
		ev := nextEvent(t, w)
		assert.Equal(t, watch.Added, ev.Type)
		names = append(names, objectName(t, ev.Object))
		acc, err := meta.Accessor(ev.Object)
		require.NoError(t, err)
		rv, err := strconv.ParseUint(acc.GetResourceVersion(), 10, 64)
		require.NoError(t, err)
		maxRV = max(maxRV, rv)
	}
	assert.ElementsMatch(t, []string{"a", "b"}, names)

	bookmark := nextEvent(t, w)
	assert.Equal(t, watch.Bookmark, bookmark.Type)
	assert.IsType(t, &softwarecomposition.OpenVulnerabilityExchangeContainer{}, bookmark.Object)
	annotated, err := storage.HasInitialEventsEndBookmarkAnnotation(bookmark.Object)
	require.NoError(t, err)
	assert.True(t, annotated)
	acc, err := meta.Accessor(bookmark.Object)
	require.NoError(t, err)
	assert.Equal(t, strconv.FormatUint(maxRV, 10), acc.GetResourceVersion())

	createVEX(t, ctx, s, "c", nil)
	ev := nextEvent(t, w)
	assert.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "c", objectName(t, ev.Object))
}

func TestWatchListWithoutBookmarksShipsOnlyObjects(t *testing.T) {
	s := newWatchListStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()
	createVEX(t, ctx, s, "a", nil)

	w, err := s.Watch(ctx, vexRootKey, watchListOptions(false))
	require.NoError(t, err)
	defer w.Stop()

	ev := nextEvent(t, w)
	assert.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "a", objectName(t, ev.Object))

	createVEX(t, ctx, s, "b", nil)
	ev = nextEvent(t, w)
	assert.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "b", objectName(t, ev.Object))
}

func TestWatchListAppliesTheLabelSelector(t *testing.T) {
	s := newWatchListStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()
	createVEX(t, ctx, s, "wanted", map[string]string{"app": "x"})
	createVEX(t, ctx, s, "other", map[string]string{"app": "y"})

	opts := watchListOptions(true)
	opts.Predicate.Label = labels.SelectorFromSet(labels.Set{"app": "x"})
	opts.Predicate.GetAttrs = storage.DefaultNamespaceScopedAttr
	w, err := s.Watch(ctx, vexRootKey, opts)
	require.NoError(t, err)
	defer w.Stop()

	ev := nextEvent(t, w)
	assert.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "wanted", objectName(t, ev.Object))
	assert.Equal(t, watch.Bookmark, nextEvent(t, w).Type)
}

func TestWatchListFullSpecCarriesTheSpec(t *testing.T) {
	s := newWatchListStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()
	createVEX(t, ctx, s, "a", nil)

	metaOnly, err := s.Watch(ctx, vexRootKey, watchListOptions(true))
	require.NoError(t, err)
	defer metaOnly.Stop()
	ev := nextEvent(t, metaOnly)
	require.Equal(t, watch.Added, ev.Type)
	assert.Empty(t, ev.Object.(*softwarecomposition.OpenVulnerabilityExchangeContainer).Spec.ID)

	fullOpts := watchListOptions(true)
	fullOpts.ResourceVersion = softwarecomposition.ResourceVersionFullSpec
	full, err := s.Watch(ctx, vexRootKey, fullOpts)
	require.NoError(t, err)
	defer full.Stop()
	ev = nextEvent(t, full)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "vex-a", ev.Object.(*softwarecomposition.OpenVulnerabilityExchangeContainer).Spec.ID)
}

func TestWatchListBuffersWritesThatLandDuringTheList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)

	vex := func(name, rv string) *softwarecomposition.OpenVulnerabilityExchangeContainer {
		return &softwarecomposition.OpenVulnerabilityExchangeContainer{ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "kubescape", ResourceVersion: rv}}
	}
	// Live traffic while the list is still running: one object the list will also
	// contain at the same version, one it will not, and a delete.
	for _, ev := range []watch.Event{
		{Type: watch.Added, Object: vex("listed", "3")},
		{Type: watch.Added, Object: vex("new", "7")},
		{Type: watch.Deleted, Object: vex("gone", "2")},
	} {
		w.notify(ev, ev)
	}

	bookmark := vex("", "3")
	require.NoError(t, storage.AnnotateInitialEventsEndBookmark(bookmark))
	w.initialCh <- initialEvents{
		events: []watch.Event{{Type: watch.Added, Object: vex("listed", "3")}, {Type: watch.Bookmark, Object: bookmark}},
		listed: map[string]string{"kubescape/listed": "3"},
	}

	var got []string
	for i := 0; i < 4; i++ {
		ev := nextEvent(t, w)
		got = append(got, string(ev.Type)+":"+objectName(t, ev.Object))
	}
	assert.Equal(t, []string{"ADDED:listed", "BOOKMARK:", "ADDED:new", "DELETED:gone"}, got)

	later := watch.Event{Type: watch.Modified, Object: vex("listed", "4")}
	w.notify(later, later)
	ev := nextEvent(t, w)
	assert.Equal(t, "MODIFIED:listed", string(ev.Type)+":"+objectName(t, ev.Object))

	w.Stop()
	assertClosed(t, w)
}

func TestInitialEventsCoversOnlyListedState(t *testing.T) {
	vex := func(name, rv string) *softwarecomposition.OpenVulnerabilityExchangeContainer {
		return &softwarecomposition.OpenVulnerabilityExchangeContainer{ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "kubescape", ResourceVersion: rv}}
	}
	ie := initialEvents{listed: map[string]string{"kubescape/a": "3"}}
	assert.True(t, ie.covers(watch.Event{Type: watch.Added, Object: vex("a", "3")}))
	assert.True(t, ie.covers(watch.Event{Type: watch.Modified, Object: vex("a", "3")}))
	assert.False(t, ie.covers(watch.Event{Type: watch.Modified, Object: vex("a", "4")}), "a newer version is not covered")
	assert.False(t, ie.covers(watch.Event{Type: watch.Added, Object: vex("b", "3")}), "an unlisted object is not covered")
	assert.False(t, ie.covers(watch.Event{Type: watch.Deleted, Object: vex("a", "3")}), "a delete is never covered")
}

func TestImmutableStorageWatchRefusesWatchList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	_, err := immutableStorage{}.Watch(ctx, "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries", watchListOptions(true))
	require.Error(t, err)
	assert.True(t, apierrors.IsMethodNotSupported(err), err.Error())

	w, err := immutableStorage{}.Watch(ctx, "/spdx.softwarecomposition.kubescape.io/vulnerabilitysummaries", storage.ListOptions{})
	require.NoError(t, err)
	assertStaysOpen(t, w)
	cancel()
	assertClosed(t, w)
}

func TestNewObjectsForKeyResolvesRegisteredResources(t *testing.T) {
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	tests := []struct {
		key      string
		wantItem interface{}
		wantList interface{}
	}{
		{key: "/spdx.softwarecomposition.kubescape.io/containerprofiles/kubescape/n", wantItem: &softwarecomposition.ContainerProfile{}, wantList: &softwarecomposition.ContainerProfileList{}},
		{key: "/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds", wantItem: &softwarecomposition.SBOMSyftFiltered{}, wantList: &softwarecomposition.SBOMSyftFilteredList{}},
		{key: "/spdx.softwarecomposition.kubescape.io/knownservers", wantItem: &softwarecomposition.KnownServer{}, wantList: &softwarecomposition.KnownServerList{}},
		{key: "/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/ns", wantItem: &softwarecomposition.VulnerabilityManifestSummary{}, wantList: &softwarecomposition.VulnerabilityManifestSummaryList{}},
	}
	for _, tt := range tests {
		item, list, err := newObjectsForKey(sch, tt.key)
		require.NoError(t, err, tt.key)
		assert.IsType(t, tt.wantItem, item, tt.key)
		assert.IsType(t, tt.wantList, list, tt.key)
	}

	_, _, err := newObjectsForKey(sch, "/spdx.softwarecomposition.kubescape.io/widgets/ns/n")
	assert.Error(t, err)
	_, _, err = newObjectsForKey(nil, vexRootKey)
	assert.Error(t, err)
}
