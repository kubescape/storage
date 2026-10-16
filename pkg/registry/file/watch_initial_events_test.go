package file

import (
	"context"
	"strconv"
	"strings"
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
	"k8s.io/apimachinery/pkg/runtime"
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
	return listedObject{uid: tv.uid, rv: tv.rv, obj: tv.object()}
}

// stubStore is the store the replay re-reads from in the watcher-level tests: a fixed map
// of object key to the committed object, or an error.
type stubStore struct {
	objects map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer
	err     error
	reads   []string
}

func (st *stubStore) Get(_ context.Context, key string, _ storage.GetOptions, objPtr runtime.Object) error {
	st.reads = append(st.reads, key)
	if st.err != nil {
		return st.err
	}
	if obj, ok := st.objects[key]; ok {
		obj.DeepCopyInto(objPtr.(*softwarecomposition.OpenVulnerabilityExchangeContainer))
		return nil
	}
	return runtime.SetZeroValue(objPtr)
}

func (st *stubStore) GetList(context.Context, string, storage.ListOptions, runtime.Object) error {
	return nil
}

func stubInitialEvents(store initialLister, bookmark bool, snapshot ...testVEX) initialEvents {
	ie := initialEvents{
		held:      map[string]listedObject{},
		lister:    store,
		newObject: func() runtime.Object { return &softwarecomposition.OpenVulnerabilityExchangeContainer{} },
		key:       vexRootKey,
		recursive: true,
		readRV:    softwarecomposition.ResourceVersionMetadata,
	}
	for _, o := range snapshot {
		ie.held["kubescape/"+o.name] = o.listed()
		ie.events = append(ie.events, o.event(watch.Added))
		if rv, err := strconv.ParseUint(o.rv, 10, 64); err == nil && rv > ie.rv {
			ie.rv = rv
		}
	}
	if bookmark {
		bm := &softwarecomposition.OpenVulnerabilityExchangeContainer{}
		if err := storage.AnnotateInitialEventsEndBookmark(bm); err != nil {
			panic(err)
		}
		ie.bookmark = bm
	}
	return ie
}

// pausingStore is a stubStore whose reads wait for the test before answering, so live
// events can be delivered while a re-read is in flight. Each read reports its key on
// started and then waits for one release; a cancelled context ends the wait.
type pausingStore struct {
	stubStore
	started chan string
	release chan struct{}
}

func (ps *pausingStore) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	select {
	case ps.started <- key:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-ps.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return ps.stubStore.Get(ctx, key, opts, objPtr)
}

// notifyWithin delivers a live event the way the dispatcher does and fails the test when
// the watcher does not take it in time.
func notifyWithin(t *testing.T, w *watcher, ev watch.Event) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		w.notify(ev, ev)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(chanWaitTimeout):
		t.Fatal("the watcher stopped receiving live events")
	}
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
	store := &stubStore{objects: map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer{
		vexRootKey + "/listed": listed.object(),
		vexRootKey + "/new":    fresh.object(),
	}}
	w.initialCh <- stubInitialEvents(store, true, listed)

	got := collectEvents(t, w, 3)
	assert.Equal(t, []string{"ADDED:listed@3", "ADDED:new@7", "BOOKMARK:@7"}, describeEvents(t, got), "the bookmark comes after the reconciled events and carries the version the client then holds")
	assert.ElementsMatch(t, []string{vexRootKey + "/listed", vexRootKey + "/new", vexRootKey + "/gone"}, store.reads, "every touched object is re-read once")

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
	store := &stubStore{objects: map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer{vexRootKey + "/b": b.object()}}
	w.initialCh <- stubInitialEvents(store, true, a, b)

	got := collectEvents(t, w, 4)
	assert.Equal(t, []string{"ADDED:a@1", "ADDED:b@2", "DELETED:a@1", "BOOKMARK:@2"}, describeEvents(t, got))
	annotated, err := storage.HasInitialEventsEndBookmarkAnnotation(got[3].Object)
	require.NoError(t, err)
	assert.True(t, annotated)
	w.Stop()
	assertClosed(t, w)
}

func TestWatchListEndsWithAnErrorWhenTheReReadFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	a := testVEX{name: "a", rv: "1", uid: "a-uid"}
	ev := a.event(watch.Modified)
	w.notify(ev, ev)
	w.initialCh <- stubInitialEvents(&stubStore{err: assert.AnError}, true, a)

	got := collectEvents(t, w, 2)
	assert.Equal(t, watch.Added, got[0].Type)
	assert.Equal(t, watch.Error, got[1].Type, "a state nobody could confirm is not declared synchronized")
	assertClosed(t, w)
}

// TestWatchListKeepsReceivingWhileItReReads delivers live events while the re-read of a
// buffered object is in flight. The watcher must take them without waiting for the read,
// and what arrived is settled against the read before the bookmark: a newer version of
// the held incarnation ships, an older one is dropped, a name the client does not hold
// ships as an add.
func TestWatchListKeepsReceivingWhileItReReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	a := testVEX{name: "a", rv: "3", uid: "a-uid"}
	during := a.event(watch.Modified)
	w.notify(during, during)
	store := &pausingStore{
		stubStore: stubStore{objects: map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer{vexRootKey + "/a": a.object()}},
		started:   make(chan string), release: make(chan struct{}),
	}
	w.initialCh <- stubInitialEvents(store, true, a)
	assert.Equal(t, []string{"ADDED:a@3"}, describeEvents(t, collectEvents(t, w, 1)))

	assert.Equal(t, vexRootKey+"/a", <-store.started)
	notifyWithin(t, w, testVEX{name: "a", rv: "5", uid: "a-uid"}.event(watch.Modified))
	notifyWithin(t, w, testVEX{name: "a", rv: "2", uid: "a-uid"}.event(watch.Modified))
	notifyWithin(t, w, testVEX{name: "b", rv: "1", uid: "b-uid"}.event(watch.Modified))
	store.release <- struct{}{}

	got := collectEvents(t, w, 3)
	assert.Equal(t, []string{"MODIFIED:a@5", "ADDED:b@1", "BOOKMARK:@5"}, describeEvents(t, got))
	assert.Equal(t, []string{vexRootKey + "/a"}, store.reads, "settled arrivals need no second read")
	w.Stop()
	assertClosed(t, w)
}

// TestWatchListReReadsAnIncarnationItCannotSettle models a delete and recreate committed
// while the first re-read was in flight and published out of order. The recreate's add
// is already reflected by the read; the old incarnation's delete arrives for a name now
// held by another incarnation, which the versions cannot order, so a second round
// re-reads it and finds nothing left to ship.
func TestWatchListReReadsAnIncarnationItCannotSettle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	old := testVEX{name: "a", rv: "3", uid: "a-old"}
	fresh := testVEX{name: "a", rv: "1", uid: "a-new"}
	during := old.event(watch.Modified)
	w.notify(during, during)
	store := &pausingStore{
		stubStore: stubStore{objects: map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer{vexRootKey + "/a": old.object()}},
		started:   make(chan string), release: make(chan struct{}),
	}
	w.initialCh <- stubInitialEvents(store, true, old)
	assert.Equal(t, []string{"ADDED:a@3"}, describeEvents(t, collectEvents(t, w, 1)))

	<-store.started
	store.objects[vexRootKey+"/a"] = fresh.object()
	notifyWithin(t, w, fresh.event(watch.Added))
	notifyWithin(t, w, old.event(watch.Deleted))
	store.release <- struct{}{}
	<-store.started
	store.release <- struct{}{}

	got := collectEvents(t, w, 3)
	assert.Equal(t, []string{"DELETED:a@3", "ADDED:a@1", "BOOKMARK:@3"}, describeEvents(t, got))
	assert.Equal(t, []string{vexRootKey + "/a", vexRootKey + "/a"}, store.reads)
	w.Stop()
	assertClosed(t, w)
}

func TestWatchListGivesUpAfterTooManyRoundsOfReReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	a := testVEX{name: "a", rv: "3", uid: "a-uid"}
	during := a.event(watch.Modified)
	w.notify(during, during)
	store := &pausingStore{
		stubStore: stubStore{objects: map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer{vexRootKey + "/a": a.object()}},
		started:   make(chan string), release: make(chan struct{}),
	}
	w.initialCh <- stubInitialEvents(store, true, a)
	assert.Equal(t, watch.Added, nextEvent(t, w).Type)

	for round := 0; round < maxReconcileRounds; round++ {
		<-store.started
		// A delete of an incarnation the client never held cannot be settled, so every
		// round ends with one more name to re-read.
		notifyWithin(t, w, testVEX{name: "a", rv: "1", uid: types.UID("ghost-" + strconv.Itoa(round))}.event(watch.Deleted))
		store.release <- struct{}{}
	}
	ev := nextEvent(t, w)
	assert.Equal(t, watch.Error, ev.Type, "a state that could not be confirmed is not declared synchronized")
	assertClosed(t, w)
	assert.Len(t, store.reads, maxReconcileRounds)
}

func TestWatchListStopCancelsAndJoinsTheReRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	w := newWatcherWithInitialEvents(ctx, false, true)
	a := testVEX{name: "a", rv: "3", uid: "a-uid"}
	during := a.event(watch.Modified)
	w.notify(during, during)
	store := &pausingStore{stubStore: stubStore{}, started: make(chan string), release: make(chan struct{})}
	w.initialCh <- stubInitialEvents(store, true, a)
	assert.Equal(t, watch.Added, nextEvent(t, w).Type)

	<-store.started
	w.Stop()
	// The result channel closes only once the read has returned, which the cancelled
	// context makes it do; nobody releases it.
	assertClosed(t, w)
}

// pausingLister wraps a real store so the first read of the replay waits for the test
// before it takes any lock, which is the moment a concurrent writer can be scheduled.
type pausingLister struct {
	initialLister
	started chan string
	release chan struct{}
	paused  bool
}

func (pl *pausingLister) Get(ctx context.Context, key string, opts storage.GetOptions, objPtr runtime.Object) error {
	if !pl.paused {
		pl.paused = true
		pl.started <- key
		<-pl.release
	}
	return pl.initialLister.Get(ctx, key, opts, objPtr)
}

// TestWatchListReReadDoesNotDeadlockWithDelete runs the replay against the real store
// with a delete that commits while the re-read of the same object is in flight.
// StorageImpl.Delete publishes while it still holds the object's write lock, and the
// re-read needs that lock, so a watcher that stopped receiving during its reads would
// hold the delete until the lock timeout and finish with an error instead of a bookmark.
func TestWatchListReReadDoesNotDeadlockWithDelete(t *testing.T) {
	old := lockTimeout
	lockTimeout = 250 * time.Millisecond
	defer func() { lockTimeout = old }()
	s := newWatchListStorage(t).(*StorageImpl)
	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()
	createVEX(t, ctx, s, "a", nil)

	w := newWatcherWithInitialEvents(ctx, false, true)
	s.watchDispatcher.Register(vexRootKey, w)
	defer w.Stop()
	var a softwarecomposition.OpenVulnerabilityExchangeContainer
	require.NoError(t, s.Get(ctx, vexRootKey+"/a", storage.GetOptions{}, &a))
	during := watch.Event{Type: watch.Modified, Object: &a}
	w.notify(during, during)
	reads := &pausingLister{initialLister: s, started: make(chan string), release: make(chan struct{})}
	initial, err := buildInitialEvents(ctx, reads, s.scheme, vexRootKey, watchListOptions(true))
	require.NoError(t, err)
	w.initialCh <- initial
	ev := nextEvent(t, w)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "a", objectName(t, ev.Object))

	assert.Equal(t, vexRootKey+"/a", <-reads.started)
	deleted := make(chan error, 1)
	go func() {
		deleted <- s.Delete(ctx, vexRootKey+"/a", &softwarecomposition.OpenVulnerabilityExchangeContainer{}, nil, nil, nil, storage.DeleteOptions{})
	}()
	select {
	case err := <-deleted:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("the delete waited on the watcher while the watcher's re-read waited on the delete's lock")
	}
	reads.release <- struct{}{}

	got := collectEvents(t, w, 2)
	assert.Equal(t, watch.Deleted, got[0].Type)
	assert.Equal(t, "a", objectName(t, got[0].Object))
	assert.Equal(t, watch.Bookmark, got[1].Type)
}

func TestInitialEventsReconcileShipsTheTransitionToTheCommittedState(t *testing.T) {
	old := func(name, rv string) testVEX { return testVEX{name: name, rv: rv, uid: types.UID(name + "-old")} }
	cur := func(name, rv string) testVEX { return testVEX{name: name, rv: rv, uid: types.UID(name + "-new")} }
	committed := func(objs ...testVEX) map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer {
		m := map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer{}
		for _, o := range objs {
			m[vexRootKey+"/"+o.name] = o.object()
		}
		return m
	}
	tests := []struct {
		name      string
		snapshot  []testVEX
		buffered  []watch.Event
		committed map[string]*softwarecomposition.OpenVulnerabilityExchangeContainer
		want      []string
	}{
		{name: "two updates before the list, published in order", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "2").event(watch.Modified), old("a", "3").event(watch.Modified)}, committed: committed(old("a", "3"))},
		{name: "two updates before the list, published out of order", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "3").event(watch.Modified), old("a", "2").event(watch.Modified)}, committed: committed(old("a", "3"))},
		{name: "updates after the list, published out of order", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "5").event(watch.Modified), old("a", "4").event(watch.Modified)}, committed: committed(old("a", "5")),
			want: []string{"MODIFIED:a@5"}},
		{name: "update after the list whose publication is still pending", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "4").event(watch.Modified)}, committed: committed(old("a", "6")),
			want: []string{"MODIFIED:a@6"}},
		{name: "delete after the list", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "3").event(watch.Deleted)}, committed: committed(),
			want: []string{"DELETED:a@3"}},
		{name: "delete before the list", snapshot: nil,
			buffered: []watch.Event{old("a", "3").event(watch.Deleted)}, committed: committed()},
		{name: "create after the list", snapshot: nil,
			buffered: []watch.Event{cur("b", "1").event(watch.Added)}, committed: committed(cur("b", "1")),
			want: []string{"ADDED:b@1"}},
		{name: "create and delete around the list", snapshot: nil,
			buffered: []watch.Event{cur("b", "1").event(watch.Added), cur("b", "1").event(watch.Deleted)}, committed: committed()},
		{name: "delete and recreate before the list", snapshot: []testVEX{cur("a", "1")},
			buffered: []watch.Event{old("a", "3").event(watch.Deleted), cur("a", "1").event(watch.Added)}, committed: committed(cur("a", "1"))},
		{name: "delete and recreate after the list", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "3").event(watch.Deleted), cur("a", "1").event(watch.Added)}, committed: committed(cur("a", "1")),
			want: []string{"DELETED:a@3", "ADDED:a@1"}},
		{name: "delete and recreate after the list, published out of order", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{cur("a", "1").event(watch.Added), old("a", "3").event(watch.Deleted)}, committed: committed(cur("a", "1")),
			want: []string{"DELETED:a@3", "ADDED:a@1"}},
		{name: "recreate after the list whose creation is still unpublished", snapshot: []testVEX{old("a", "3")},
			buffered: []watch.Event{old("a", "3").event(watch.Deleted)}, committed: committed(cur("a", "1")),
			want: []string{"DELETED:a@3", "ADDED:a@1"}},
		{name: "recreate at the same version", snapshot: []testVEX{old("a", "1")},
			buffered: []watch.Event{old("a", "1").event(watch.Deleted), cur("a", "1").event(watch.Added)}, committed: committed(cur("a", "1")),
			want: []string{"DELETED:a@1", "ADDED:a@1"}},
		{name: "several objects, each at its committed state", snapshot: []testVEX{old("a", "3"), old("c", "1")},
			buffered:  []watch.Event{cur("b", "1").event(watch.Added), old("a", "4").event(watch.Modified), old("c", "1").event(watch.Deleted), cur("b", "2").event(watch.Modified)},
			committed: committed(cur("b", "2"), old("a", "4")),
			want:      []string{"ADDED:b@2", "MODIFIED:a@4", "DELETED:c@1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubStore{objects: tt.committed}
			ie := stubInitialEvents(store, false, tt.snapshot...)
			got, err := ie.reconcile(context.Background(), tt.buffered)
			require.NoError(t, err)
			assert.Equal(t, tt.want, describeEvents(t, got))
		})
	}
}

func TestInitialEventsSettleOrdersLateArrivalsAgainstTheReads(t *testing.T) {
	held := func(name, rv string) testVEX { return testVEX{name: name, rv: rv, uid: types.UID(name + "-held")} }
	other := func(name, rv string) testVEX { return testVEX{name: name, rv: rv, uid: types.UID(name + "-other")} }
	tests := []struct {
		name          string
		holds         []testVEX
		late          []watch.Event
		want          []string
		wantUnsettled []string
		wantHeldAfter []string
	}{
		{name: "newer version of the held incarnation ships", holds: []testVEX{held("a", "3")},
			late: []watch.Event{held("a", "5").event(watch.Modified)}, want: []string{"MODIFIED:a@5"}, wantHeldAfter: []string{"a@5"}},
		{name: "older or equal version is dropped", holds: []testVEX{held("a", "3")},
			late: []watch.Event{held("a", "2").event(watch.Modified), held("a", "3").event(watch.Added)}, wantHeldAfter: []string{"a@3"}},
		{name: "a name not held ships as an add whatever the event type", holds: nil,
			late: []watch.Event{other("b", "1").event(watch.Modified)}, want: []string{"ADDED:b@1"}, wantHeldAfter: []string{"b@1"}},
		{name: "delete of the held incarnation ships", holds: []testVEX{held("a", "3")},
			late: []watch.Event{held("a", "3").event(watch.Deleted)}, want: []string{"DELETED:a@3"}},
		{name: "delete of a name not held is final and its reordered events are dropped", holds: nil,
			late: []watch.Event{other("a", "1").event(watch.Deleted), other("a", "1").event(watch.Added), other("a", "2").event(watch.Modified)}},
		{name: "reordered update of a deleted incarnation is dropped", holds: []testVEX{held("a", "3")},
			late: []watch.Event{held("a", "4").event(watch.Deleted), held("a", "4").event(watch.Modified)}, want: []string{"DELETED:a@4"}},
		{name: "another incarnation is re-read", holds: []testVEX{held("a", "3")},
			late: []watch.Event{other("a", "1").event(watch.Added)}, wantUnsettled: []string{"ADDED:a@1"}, wantHeldAfter: []string{"a@3"}},
		{name: "an unparsable version is re-read", holds: []testVEX{held("a", "3")},
			late: []watch.Event{held("a", "x").event(watch.Modified)}, wantUnsettled: []string{"MODIFIED:a@x"}, wantHeldAfter: []string{"a@3"}},
		{name: "a sequence in arrival order needs no re-read", holds: []testVEX{held("a", "3")},
			late: []watch.Event{held("a", "4").event(watch.Modified), held("a", "4").event(watch.Deleted), other("a", "1").event(watch.Added), other("a", "2").event(watch.Modified)},
			want: []string{"MODIFIED:a@4", "DELETED:a@4", "ADDED:a@1", "MODIFIED:a@2"}, wantHeldAfter: []string{"a@2"}},
		{name: "the same sequence with the add published first is re-read", holds: []testVEX{held("a", "3")},
			late: []watch.Event{other("a", "1").event(watch.Added), held("a", "4").event(watch.Modified), held("a", "4").event(watch.Deleted), other("a", "2").event(watch.Modified)},
			want: []string{"MODIFIED:a@4", "DELETED:a@4", "ADDED:a@2"}, wantUnsettled: []string{"ADDED:a@1"}, wantHeldAfter: []string{"a@2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ie := stubInitialEvents(&stubStore{}, false, tt.holds...)
			shipped, unsettled := ie.settle(tt.late)
			assert.Equal(t, tt.want, describeEvents(t, shipped))
			assert.Equal(t, tt.wantUnsettled, describeEvents(t, unsettled))
			var heldAfter []string
			for k, o := range ie.held {
				heldAfter = append(heldAfter, strings.TrimPrefix(k, "kubescape/")+"@"+o.rv)
			}
			assert.ElementsMatch(t, tt.wantHeldAfter, heldAfter)
		})
	}
}

func TestInitialEventsObjectKey(t *testing.T) {
	namespaced := initialEvents{key: "/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape", recursive: true}
	assert.Equal(t, namespaced.key+"/a", namespaced.objectKey("kubescape", "a"))
	allNamespaces := initialEvents{key: "/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers", recursive: true}
	assert.Equal(t, allNamespaces.key+"/kubescape/a", allNamespaces.objectKey("kubescape", "a"))
	clusterScoped := initialEvents{key: "/spdx.softwarecomposition.kubescape.io/knownservers", recursive: true}
	assert.Equal(t, clusterScoped.key+"/a", clusterScoped.objectKey("", "a"))
	named := initialEvents{key: namespaced.key + "/a"}
	assert.Equal(t, named.key, named.objectKey("kubescape", "a"))
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

	none := initialEvents{}
	_, ok = none.bookmarkEvent(nil)
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
