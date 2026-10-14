package file

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

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
