package file

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

// updateProfile commits p through the store's GuaranteedUpdate and returns the stored result.
func updateProfile(t *testing.T, e *objectStoreEnv, key string, p *softwarecomposition.ContainerProfile) *softwarecomposition.ContainerProfile {
	t.Helper()
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, e.store.GuaranteedUpdate(e.ctx, key, out, false, nil,
		func(runtime.Object, storage.ResponseMeta) (runtime.Object, *uint64, error) {
			return p.DeepCopy(), nil, nil
		}, nil))
	return out
}

// TestObjectStoreWatchListReplaysProfiles covers the ContainerProfile SQLite backend: the
// replay, the bookmark, live events on the same stream, and the named metadata-only and
// full-spec reads.
func TestObjectStoreWatchListReplaysProfiles(t *testing.T) {
	e := newObjectStoreEnv(t)
	created := e.create(e.plain("a"))
	require.NotEmpty(t, created.Spec.Architectures, "the fixture profile needs a spec to tell full from metadata-only reads")
	root := testCPPrefix + e.ns

	w, err := e.store.Watch(e.ctx, root, watchListOptions(true))
	require.NoError(t, err)
	defer w.Stop()
	ev := nextEvent(t, w)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "a", objectName(t, ev.Object))
	assert.Empty(t, ev.Object.(*softwarecomposition.ContainerProfile).Spec.Architectures, "a default replay is metadata only")
	bookmark := nextEvent(t, w)
	require.Equal(t, watch.Bookmark, bookmark.Type)
	annotated, err := storage.HasInitialEventsEndBookmarkAnnotation(bookmark.Object)
	require.NoError(t, err)
	assert.True(t, annotated)
	acc, err := meta.Accessor(bookmark.Object)
	require.NoError(t, err)
	assert.Equal(t, created.ResourceVersion, acc.GetResourceVersion())

	e.create(e.plain("b"))
	ev = nextEvent(t, w)
	assert.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "b", objectName(t, ev.Object))

	named := watchListOptions(true)
	named.Recursive = false
	metaOnly, err := e.store.Watch(e.ctx, e.key("a"), named)
	require.NoError(t, err)
	defer metaOnly.Stop()
	ev = nextEvent(t, metaOnly)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, "a", objectName(t, ev.Object))
	assert.Empty(t, ev.Object.(*softwarecomposition.ContainerProfile).Spec.Architectures)
	assert.Equal(t, watch.Bookmark, nextEvent(t, metaOnly).Type)

	named.ResourceVersion = softwarecomposition.ResourceVersionFullSpec
	full, err := e.store.Watch(e.ctx, e.key("a"), named)
	require.NoError(t, err)
	defer full.Stop()
	ev = nextEvent(t, full)
	require.Equal(t, watch.Added, ev.Type)
	assert.Equal(t, created.Spec.Architectures, ev.Object.(*softwarecomposition.ContainerProfile).Spec.Architectures)
}

// TestObjectStoreWatchListReconcilesReorderedNotifications feeds the replay the buffered
// events exactly as the store can publish them, out of commit order, and checks that the
// re-read of the real ObjectStore decides the outcome.
func TestObjectStoreWatchListReconcilesReorderedNotifications(t *testing.T) {
	e := newObjectStoreEnv(t)
	root := testCPPrefix + e.ns
	snapshot := func(objs ...*softwarecomposition.ContainerProfile) initialEvents {
		ie := initialEvents{
			listed:    map[string]listedObject{},
			lister:    e.store,
			newObject: func() runtime.Object { return &softwarecomposition.ContainerProfile{} },
			key:       root,
			recursive: true,
			readRV:    softwarecomposition.ResourceVersionMetadata,
		}
		for _, o := range objs {
			ie.listed[o.Namespace+"/"+o.Name] = listedObject{uid: o.UID, rv: o.ResourceVersion, obj: o}
		}
		return ie
	}
	event := func(typ watch.EventType, o *softwarecomposition.ContainerProfile) watch.Event {
		return watch.Event{Type: typ, Object: o}
	}

	// Commit version 2 then 3 of the same object, snapshot at 3, notifications arrive as
	// [3, 2]: the store holds 3, so nothing ships and the client stays at 3.
	v1 := e.create(e.plain("a"))
	v2 := e.mustGet(e.key("a"))
	v2.Spec.Architectures = append(v2.Spec.Architectures, "arm64")
	v2 = updateProfile(t, e, e.key("a"), v2)
	v3 := e.mustGet(e.key("a"))
	v3.Spec.Architectures = append(v3.Spec.Architectures, "s390x")
	v3 = updateProfile(t, e, e.key("a"), v3)
	require.Equal(t, "3", v3.ResourceVersion)
	got, err := snapshot(v3).reconcile(e.ctx, []watch.Event{event(watch.Modified, v3), event(watch.Modified, v2)})
	require.NoError(t, err)
	assert.Empty(t, got)

	// Snapshot at 2, the update to 3 published before the one to 2: the client gets 3 once.
	got, err = snapshot(v2).reconcile(e.ctx, []watch.Event{event(watch.Modified, v3), event(watch.Modified, v2)})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, watch.Modified, got[0].Type)
	assert.Equal(t, "3", got[0].Object.(*softwarecomposition.ContainerProfile).ResourceVersion)

	// Delete and recreate, published as [ADDED new, DELETED old]: the store holds the new
	// incarnation, so the client gets the delete of the old one and the add of the new one.
	require.NoError(t, e.store.Delete(e.ctx, e.key("a"), nil, nil, nil, nil, storage.DeleteOptions{}))
	recreated := e.create(e.plain("a"))
	require.NotEqual(t, v1.UID, recreated.UID)
	got, err = snapshot(v3).reconcile(e.ctx, []watch.Event{event(watch.Added, recreated), event(watch.Deleted, v3)})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, watch.Deleted, got[0].Type)
	assert.Equal(t, v1.UID, got[0].Object.(*softwarecomposition.ContainerProfile).UID)
	assert.Equal(t, watch.Added, got[1].Type)
	assert.Equal(t, recreated.UID, got[1].Object.(*softwarecomposition.ContainerProfile).UID)

	// The same with only the delete published so far: the re-read still finds the new one.
	got, err = snapshot(v3).reconcile(e.ctx, []watch.Event{event(watch.Deleted, v3)})
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, watch.Deleted, got[0].Type)
	assert.Equal(t, watch.Added, got[1].Type)
}
