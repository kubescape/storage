package file

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A writer that updates or re-creates an object between the walk's decision and the
// delete keeps it: the rows and the payload go only when, under the write gate, the row
// is still the one the decision was made on.
func TestCleanupKeepsAnObjectRewrittenAfterTheDecision(t *testing.T) {
	const kind = "applicationprofiles" // deleteDeprecated: the walk always decides to delete
	path := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, "ns", "app"+GobExt)
	key := payloadPathToKey(path)
	meta := func(rv string) []byte {
		return []byte(fmt.Sprintf(`{"name":"app","namespace":"ns","uid":"u1","resourceVersion":%q}`, rv))
	}
	run := func(t *testing.T, rewrite bool) (payloadLeft, rowLeft bool) {
		memFs := afero.NewMemMapFs()
		require.NoError(t, afero.WriteFile(memFs, path, []byte("payload"), 0644))
		pool := NewTestPool(t.TempDir())
		conn, err := pool.Take(context.Background())
		require.NoError(t, err)
		defer pool.Put(conn)
		require.NoError(t, WriteJSON(conn, key, meta("1")))
		h := &ResourcesCleanupHandler{appFs: memFs, pool: pool, root: DefaultStorageRoot, deleteFunc: deleteFile}
		if rewrite {
			h.beforeGatedDelete = func(k string) {
				if k == key {
					require.NoError(t, WriteJSON(conn, key, meta("2")))
				}
			}
		}
		require.NoError(t, h.cleanupNamespace(context.Background(), "ns", map[string][]TypeCleanupHandlerFunc{kind: {deleteDeprecated}}, conn, ResourceMaps{}))
		payloadLeft, _ = afero.Exists(memFs, path)
		_, err = ReadMetadata(conn, key)
		return payloadLeft, err == nil
	}

	payloadLeft, rowLeft := run(t, true)
	assert.True(t, payloadLeft, "the rewritten object's payload was removed")
	assert.True(t, rowLeft, "the rewritten object's row was removed")

	payloadLeft, rowLeft = run(t, false)
	assert.False(t, payloadLeft, "an unchanged object keeps its payload")
	assert.False(t, rowLeft, "an unchanged object keeps its row")
}
