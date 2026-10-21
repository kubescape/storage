package file

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

// A delete whose metadata row delete fails leaves the object as it was, returns the
// error and tells no watcher. Before, the payload file was removed anyway, nil was
// returned and a DELETED event with an empty object went out, leaving a row without its
// payload that LIST kept showing.
func TestStorageImpl_DeleteKeepsTheObjectWhenTheRowDeleteFails(t *testing.T) {
	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	dispatcher := NewWatchDispatcher()
	s := NewStorageImpl(fs, DefaultStorageRoot, pool, dispatcher, scheme.Scheme)
	key := "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto"
	require.NoError(t, s.Create(context.Background(), key, &v1beta1.SBOMSyft{ObjectMeta: v1.ObjectMeta{Name: "toto", Namespace: "kubescape"}}, nil, 0))
	w, err := s.Watch(context.Background(), key, storage.ListOptions{})
	require.NoError(t, err)
	t.Cleanup(w.Stop)

	// Cancel the request as its metadata DELETE is prepared: the connection's
	// interrupt follows the request context, so that statement fails.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Bool
	prev := writeStmtObserver.Load()
	hook := func(ws writeStatement) {
		if prev != nil {
			(*prev)(ws)
		}
		if ws.pool == pool && ws.op == sqlite.OpDelete && ws.table == "metadata" && fired.CompareAndSwap(false, true) {
			cancel()
		}
	}
	writeStmtObserver.Store(&hook)
	t.Cleanup(func() { writeStmtObserver.Store(prev) })

	err = s.Delete(ctx, key, &v1beta1.SBOMSyft{}, nil, nil, nil, storage.DeleteOptions{})

	require.True(t, fired.Load(), "the metadata row delete was attempted")
	require.ErrorContains(t, err, "delete metadata")
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	_, err = ReadMetadata(conn, key)
	pool.Put(conn)
	assert.NoError(t, err, "the metadata row is still there")
	exists, err := afero.Exists(fs, makePayloadPath(DefaultStorageRoot+key))
	require.NoError(t, err)
	assert.True(t, exists, "the payload file is still there")
	select {
	case ev := <-w.ResultChan():
		t.Fatalf("a watcher was told of a delete that did not happen: %v", ev)
	case <-time.After(200 * time.Millisecond):
	}
	got := &v1beta1.SBOMSyft{}
	require.NoError(t, s.Get(context.Background(), key, storage.GetOptions{}, got))
	assert.Equal(t, "toto", got.Name, "the object is still readable")
}
