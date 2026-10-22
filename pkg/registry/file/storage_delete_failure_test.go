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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// A delete whose row delete fails, at whichever of its statements, leaves the object as it
// was, returns the error and tells no watcher; the retry then deletes it whole and the
// watcher gets the real object. Before, a failed metadata delete removed the payload file
// anyway, returned nil and dispatched a DELETED event with an empty object, and a failed
// time-series delete after a committed metadata delete would have left the payload file
// without its row.
func TestStorageImpl_DeleteKeepsTheObjectWhenARowDeleteFails(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		object runtime.Object
		table  string
	}{
		{name: "metadata row of an SBOM", key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
			object: &v1beta1.SBOMSyft{ObjectMeta: v1.ObjectMeta{Name: "toto", Namespace: "kubescape"}}, table: "metadata"},
		{name: "time series rows of a container profile, after the metadata row", key: "/spdx.softwarecomposition.kubescape.io/containerprofiles/kubescape/toto",
			object: &v1beta1.ContainerProfile{ObjectMeta: v1.ObjectMeta{Name: "toto", Namespace: "kubescape"}}, table: "time_series"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			pool := NewTestPool(t.TempDir())
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			dispatcher := NewWatchDispatcher()
			s := NewStorageImpl(fs, DefaultStorageRoot, pool, dispatcher, scheme.Scheme)
			require.NoError(t, s.Create(context.Background(), tt.key, tt.object, nil, 0))
			w, err := s.Watch(context.Background(), tt.key, storage.ListOptions{})
			require.NoError(t, err)
			t.Cleanup(w.Stop)

			// Cancel the request as the DELETE on tt.table is prepared: the connection's
			// interrupt follows the request context, so that statement fails.
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var fired atomic.Bool
			prev := writeStmtObserver.Load()
			hook := func(ws writeStatement) {
				if prev != nil {
					(*prev)(ws)
				}
				if ws.pool == pool && ws.op == sqlite.OpDelete && ws.table == tt.table && fired.CompareAndSwap(false, true) {
					cancel()
				}
			}
			writeStmtObserver.Store(&hook)
			t.Cleanup(func() { writeStmtObserver.Store(prev) })

			out := tt.object.DeepCopyObject()
			err = s.Delete(ctx, tt.key, out, nil, nil, nil, storage.DeleteOptions{})

			require.True(t, fired.Load(), "the row delete was attempted")
			require.Error(t, err)
			assert.True(t, metadataRowExists(t, pool, tt.key), "the metadata row is still there")
			exists, err := afero.Exists(fs, makePayloadPath(DefaultStorageRoot+tt.key))
			require.NoError(t, err)
			assert.True(t, exists, "the payload file is still there")
			select {
			case ev := <-w.ResultChan():
				t.Fatalf("a watcher was told of a delete that did not happen: %v", ev)
			case <-time.After(200 * time.Millisecond):
			}
			got := tt.object.DeepCopyObject()
			require.NoError(t, s.Get(context.Background(), tt.key, storage.GetOptions{}, got))
			assert.Equal(t, "toto", got.(v1.Object).GetName(), "the object is still readable")

			// The retry deletes it whole and the watcher gets the object.
			writeStmtObserver.Store(prev)
			out = tt.object.DeepCopyObject()
			require.NoError(t, s.Delete(context.Background(), tt.key, out, nil, nil, nil, storage.DeleteOptions{}))
			assert.Equal(t, "toto", out.(v1.Object).GetName(), "the delete fills in the deleted object")
			assert.False(t, metadataRowExists(t, pool, tt.key))
			exists, err = afero.Exists(fs, makePayloadPath(DefaultStorageRoot+tt.key))
			require.NoError(t, err)
			assert.False(t, exists)
			select {
			case ev := <-w.ResultChan():
				assert.Equal(t, "DELETED", string(ev.Type))
				assert.Equal(t, "toto", ev.Object.(v1.Object).GetName(), "the event carries the deleted object")
			case <-time.After(2 * time.Second):
				t.Fatal("the watcher was not told of the delete")
			}
		})
	}
}

func metadataRowExists(t *testing.T, pool *sqlitemigration.Pool, key string) bool {
	t.Helper()
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)
	_, err = ReadMetadata(conn, key)
	return err == nil
}
