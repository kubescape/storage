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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
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
		ObjectMeta: v1.ObjectMeta{Name: name, Namespace: "kubescape", Labels: lbls, UID: uuid.NewUUID(), CreationTimestamp: v1.Now()},
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
		assert.NotEmpty(t, acc.GetUID(), "the replay carries the uid the reconciliation keys on")
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

type testVEX struct {
	name, rv string
	uid      types.UID
	created  time.Time
}

func (tv testVEX) object() *softwarecomposition.OpenVulnerabilityExchangeContainer {
	return &softwarecomposition.OpenVulnerabilityExchangeContainer{ObjectMeta: v1.ObjectMeta{
		Name: tv.name, Namespace: "kubescape", ResourceVersion: tv.rv, UID: tv.uid, CreationTimestamp: v1.NewTime(tv.created),
	}}
}

func (tv testVEX) event(typ watch.EventType) watch.Event {
	return watch.Event{Type: typ, Object: tv.object()}
}

func (tv testVEX) listed() listedObject {
	return listedObject{uid: tv.uid, rv: tv.rv}
}

func describeEvents(t *testing.T, events []watch.Event) []string {
	t.Helper()
	if len(events) == 0 {
		return nil
	}
	out := make([]string, 0, len(events))
	for _, e := range events {
		acc, err := meta.Accessor(e.Object)
		require.NoError(t, err)
		out = append(out, string(e.Type)+":"+acc.GetName()+"@"+acc.GetResourceVersion())
	}
	return out
}

func collectEvents(t *testing.T, w watch.Interface, n int) []watch.Event {
	t.Helper()
	events := make([]watch.Event, 0, n)
	for i := 0; i < n; i++ {
		events = append(events, nextEvent(t, w))
	}
	return events
}

func TestWatchListBuffersWritesThatLandDuringTheList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	now := time.Now()
	listed := testVEX{name: "listed", rv: "3", uid: "listed-uid", created: now}
	fresh := testVEX{name: "new", rv: "7", uid: "new-uid", created: now}
	gone := testVEX{name: "gone", rv: "2", uid: "gone-uid", created: now}

	// Live traffic while the list is still running: one object the list will also hold at
	// the same version, one it will not, and the delete of one it never had.
	for _, ev := range []watch.Event{listed.event(watch.Added), fresh.event(watch.Added), gone.event(watch.Deleted)} {
		w.notify(ev, ev)
	}

	bookmark := &softwarecomposition.OpenVulnerabilityExchangeContainer{}
	require.NoError(t, storage.AnnotateInitialEventsEndBookmark(bookmark))
	w.initialCh <- initialEvents{
		events:   []watch.Event{listed.event(watch.Added)},
		listed:   map[string]listedObject{"kubescape/listed": listed.listed()},
		bookmark: bookmark,
		rv:       3,
	}

	got := collectEvents(t, w, 3)
	assert.Equal(t, []string{"ADDED:listed@3", "ADDED:new@7", "BOOKMARK:@7"}, describeEvents(t, got), "the bookmark comes after the reconciled events and carries the version the client then holds")

	later := testVEX{name: "listed", rv: "4", uid: "listed-uid", created: now}.event(watch.Modified)
	w.notify(later, later)
	assert.Equal(t, []string{"MODIFIED:listed@4"}, describeEvents(t, collectEvents(t, w, 1)))

	w.Stop()
	assertClosed(t, w)
}

// TestWatchListReconcilesWritesBetweenListPagesBeforeTheBookmark models a list whose
// pages straddle writes: page one read A, then A was deleted and B updated, then page two
// read the updated B. The deletion is applied before the bookmark, so the state the client
// holds when it finishes its initial sync is one that existed.
func TestWatchListReconcilesWritesBetweenListPagesBeforeTheBookmark(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	now := time.Now()
	a := testVEX{name: "a", rv: "1", uid: "a-uid", created: now}
	b := testVEX{name: "b", rv: "2", uid: "b-uid", created: now}

	for _, ev := range []watch.Event{a.event(watch.Deleted), b.event(watch.Modified)} {
		w.notify(ev, ev)
	}
	bookmark := &softwarecomposition.OpenVulnerabilityExchangeContainer{}
	require.NoError(t, storage.AnnotateInitialEventsEndBookmark(bookmark))
	w.initialCh <- initialEvents{
		events:   []watch.Event{a.event(watch.Added), b.event(watch.Added)},
		listed:   map[string]listedObject{"kubescape/a": a.listed(), "kubescape/b": b.listed()},
		bookmark: bookmark,
		rv:       2,
	}

	got := collectEvents(t, w, 4)
	assert.Equal(t, []string{"ADDED:a@1", "ADDED:b@2", "DELETED:a@1", "BOOKMARK:@2"}, describeEvents(t, got))
	annotated, err := storage.HasInitialEventsEndBookmarkAnnotation(got[3].Object)
	require.NoError(t, err)
	assert.True(t, annotated)
	w.Stop()
	assertClosed(t, w)
}

func TestInitialEventsReconcileKeepsOnlyWhatTheSnapshotLacks(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	old := func(name, rv string) testVEX {
		return testVEX{name: name, rv: rv, uid: types.UID(name + "-old"), created: t0}
	}
	cur := func(name, rv string) testVEX {
		return testVEX{name: name, rv: rv, uid: types.UID(name + "-new"), created: t0.Add(time.Minute)}
	}
	// A replacement whose creation timestamp predates the snapshot's object: the timestamp
	// is stamped before validation, so a request that paused there can commit after a later
	// one was created, snapshotted and deleted.
	stale := func(name, rv string) testVEX {
		return testVEX{name: name, rv: rv, uid: types.UID(name + "-stale"), created: t0.Add(-time.Minute)}
	}
	snapshot := func(objs ...testVEX) map[string]listedObject {
		m := map[string]listedObject{}
		for _, o := range objs {
			m["kubescape/"+o.name] = o.listed()
		}
		return m
	}
	tests := []struct {
		name     string
		listed   map[string]listedObject
		buffered []watch.Event
		want     []string
	}{
		{name: "two updates before the list", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "2").event(watch.Modified), old("a", "3").event(watch.Modified)}},
		{name: "stale update whose successor the list already holds", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "2").event(watch.Modified)}},
		{name: "update after the list", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "4").event(watch.Modified)}, want: []string{"MODIFIED:a@4"}},
		{name: "delete after the list", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "3").event(watch.Deleted)}, want: []string{"DELETED:a@3"}},
		{name: "delete before the list", listed: snapshot(),
			buffered: []watch.Event{old("a", "3").event(watch.Deleted)}},
		{name: "create after the list", listed: snapshot(),
			buffered: []watch.Event{cur("b", "1").event(watch.Added)}, want: []string{"ADDED:b@1"}},
		{name: "create and delete before the list", listed: snapshot(),
			buffered: []watch.Event{cur("b", "1").event(watch.Added), cur("b", "1").event(watch.Deleted)}},
		{name: "delete and recreate before the list", listed: snapshot(cur("a", "1")),
			buffered: []watch.Event{old("a", "3").event(watch.Deleted), cur("a", "1").event(watch.Added)}},
		{name: "recreate after the list at a lower version", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "3").event(watch.Deleted), cur("a", "1").event(watch.Added)},
			want:     []string{"DELETED:a@3", "ADDED:a@1"}},
		{name: "recreate after the list at the same version", listed: snapshot(old("a", "1")),
			buffered: []watch.Event{old("a", "1").event(watch.Deleted), cur("a", "1").event(watch.Added)},
			want:     []string{"DELETED:a@1", "ADDED:a@1"}},
		{name: "recreate after the list and an update of the replacement", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "3").event(watch.Deleted), cur("a", "1").event(watch.Added), cur("a", "2").event(watch.Modified)},
			want:     []string{"DELETED:a@3", "ADDED:a@1", "MODIFIED:a@2"}},
		{name: "recreate after the list by a request stamped before the snapshot's object", listed: snapshot(cur("a", "3")),
			buffered: []watch.Event{cur("a", "3").event(watch.Deleted), stale("a", "1").event(watch.Added)},
			want:     []string{"DELETED:a@3", "ADDED:a@1"}},
		{name: "delete and recreate before the list by a request stamped before the snapshot's object", listed: snapshot(stale("a", "1")),
			buffered: []watch.Event{cur("a", "3").event(watch.Deleted), stale("a", "1").event(watch.Added)}},
		{name: "recreate after a delete the list saw", listed: snapshot(),
			buffered: []watch.Event{old("b", "2").event(watch.Deleted), cur("b", "1").event(watch.Added)}, want: []string{"ADDED:b@1"}},
		{name: "history after the update the list holds", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{old("a", "3").event(watch.Modified), old("a", "3").event(watch.Deleted), cur("a", "1").event(watch.Added)},
			want:     []string{"DELETED:a@3", "ADDED:a@1"}},
		{name: "other objects keep their own order", listed: snapshot(old("a", "3")),
			buffered: []watch.Event{cur("b", "1").event(watch.Added), old("a", "3").event(watch.Modified), cur("b", "2").event(watch.Modified), old("a", "4").event(watch.Modified)},
			want:     []string{"ADDED:b@1", "MODIFIED:b@2", "MODIFIED:a@4"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ie := initialEvents{listed: tt.listed}
			assert.Equal(t, tt.want, describeEvents(t, ie.reconcile(tt.buffered)))
		})
	}
}

func TestInitialEventsBookmarkCarriesTheVersionTheClientHolds(t *testing.T) {
	bookmark := &softwarecomposition.OpenVulnerabilityExchangeContainer{}
	ie := initialEvents{bookmark: bookmark, rv: 3}
	ev, ok := ie.bookmarkEvent([]watch.Event{
		testVEX{name: "a", rv: "9", uid: "a"}.event(watch.Modified),
		testVEX{name: "b", rv: "12", uid: "b"}.event(watch.Deleted),
	})
	require.True(t, ok)
	acc, err := meta.Accessor(ev.Object)
	require.NoError(t, err)
	assert.Equal(t, "9", acc.GetResourceVersion(), "deletes do not raise the version, upserts do")

	ev, ok = ie.bookmarkEvent(nil)
	require.True(t, ok)
	acc, err = meta.Accessor(ev.Object)
	require.NoError(t, err)
	assert.Equal(t, "3", acc.GetResourceVersion())

	_, ok = initialEvents{}.bookmarkEvent(nil)
	assert.False(t, ok, "no bookmark unless the client allowed them")
}

func TestWatchListRejectsAMinimumVersionAboveTheSnapshot(t *testing.T) {
	s := newWatchListStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	opts := watchListOptions(true)
	opts.ResourceVersion = "100"
	_, err := s.Watch(ctx, vexRootKey, opts)
	require.Error(t, err, "an empty collection cannot be shown to be at least as new as version 100")
	assert.True(t, apierrors.HasStatusCause(err, v1.CauseTypeResourceVersionTooLarge), err.Error())

	createVEX(t, ctx, s, "a", nil)
	for _, rv := range []string{"", "0", "1"} {
		opts.ResourceVersion = rv
		w, err := s.Watch(ctx, vexRootKey, opts)
		require.NoError(t, err, "resourceVersion %q", rv)
		require.Equal(t, watch.Added, nextEvent(t, w).Type)
		bookmark := nextEvent(t, w)
		require.Equal(t, watch.Bookmark, bookmark.Type)
		acc, err := meta.Accessor(bookmark.Object)
		require.NoError(t, err)
		assert.Equal(t, "1", acc.GetResourceVersion())
		w.Stop()
	}
}

func TestWatchListNamedReplayReadsLikeTheList(t *testing.T) {
	s := newWatchListStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()
	createVEX(t, ctx, s, "a", nil)

	named := watchListOptions(true)
	named.Recursive = false
	w, err := s.Watch(ctx, vexRootKey+"/a", named)
	require.NoError(t, err)
	defer w.Stop()
	ev := nextEvent(t, w)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "a", objectName(t, ev.Object))
	assert.Empty(t, ev.Object.(*softwarecomposition.OpenVulnerabilityExchangeContainer).Spec.ID, "a default named replay is metadata only")
	assert.Equal(t, watch.Bookmark, nextEvent(t, w).Type)

	named.ResourceVersion = softwarecomposition.ResourceVersionFullSpec
	full, err := s.Watch(ctx, vexRootKey+"/a", named)
	require.NoError(t, err)
	defer full.Stop()
	ev = nextEvent(t, full)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "vex-a", ev.Object.(*softwarecomposition.OpenVulnerabilityExchangeContainer).Spec.ID)

	named.ResourceVersion = ""
	missing, err := s.Watch(ctx, vexRootKey+"/absent", named)
	require.NoError(t, err)
	defer missing.Stop()
	assert.Equal(t, watch.Bookmark, nextEvent(t, missing).Type, "a named watch on an absent object starts with just the bookmark")
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
		{key: "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/n", wantItem: &softwarecomposition.ContainerProfile{}, wantList: &softwarecomposition.ContainerProfileList{}},
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
