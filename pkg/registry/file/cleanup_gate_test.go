package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// listDeleting returns the reserved payload files left in a directory.
func listDeleting(t *testing.T, fs afero.Fs, dir string) []string {
	t.Helper()
	entries, err := afero.ReadDir(fs, dir)
	require.NoError(t, err)
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), cleanupDeletingSuffix) {
			out = append(out, e.Name())
		}
	}
	return out
}

// Once the gate is released, a delete and a create by another writer can put a new
// payload and a new row at the same path before the removal runs: the removal takes the
// payload reserved under the gate, never the newcomer.
func TestCleanupRemovesTheReservedPayloadNotAReplacement(t *testing.T) {
	const kind = "applicationprofiles"
	path := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, "ns", "app"+GobExt)
	key := payloadPathToKey(path)
	meta := func(rv string) []byte {
		return []byte(fmt.Sprintf(`{"name":"app","namespace":"ns","uid":"u%s","resourceVersion":%q}`, rv, rv))
	}
	memFs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(memFs, path, []byte("old payload"), 0644))
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)
	require.NoError(t, WriteJSON(conn, key, meta("1")))
	h := &ResourcesCleanupHandler{appFs: memFs, pool: pool, root: DefaultStorageRoot, deleteFunc: deleteFile}
	h.beforeReservedRemoval = func(k string) {
		if k == key {
			require.NoError(t, afero.WriteFile(memFs, path, []byte("new payload"), 0644))
			require.NoError(t, WriteJSON(conn, key, meta("2")))
		}
	}
	require.NoError(t, h.cleanupNamespace(context.Background(), "ns", map[string][]TypeCleanupHandlerFunc{kind: {deleteDeprecated}}, conn, ResourceMaps{}))

	content, err := afero.ReadFile(memFs, path)
	require.NoError(t, err, "the replacement payload was removed")
	assert.Equal(t, "new payload", string(content))
	row, err := ReadMetadata(conn, key)
	require.NoError(t, err, "the replacement row was removed")
	assert.Equal(t, string(meta("2")), string(row))
	assert.Empty(t, listDeleting(t, memFs, filepath.Dir(path)), "the reserved payload is gone")
}

// A reserved payload a crash left behind is settled by the next walk: back in place when
// its row is still there and nothing sits at its path, the rows were never deleted;
// removed when the row is gone, the deletion went through.
func TestCleanupSettlesAReservedPayloadLeftByACrash(t *testing.T) {
	const kind = "applicationprofiles"
	dir := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, "ns")
	payload := func(name string) string { return filepath.Join(dir, name+GobExt) }
	reserved := func(name string) string { return payload(name) + ".0123456789abcdef" + cleanupDeletingSuffix }
	memFs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)

	// kept: the row exists and the payload path is empty
	require.NoError(t, afero.WriteFile(memFs, reserved("kept"), []byte("payload"), 0644))
	require.NoError(t, WriteJSON(conn, payloadPathToKey(payload("kept")), []byte(`{"name":"kept","namespace":"ns","annotations":{"kubescape.io/image-id":"live"}}`)))
	// gone: no row
	require.NoError(t, afero.WriteFile(memFs, reserved("gone"), []byte("payload"), 0644))

	h := &ResourcesCleanupHandler{appFs: memFs, pool: pool, root: DefaultStorageRoot, deleteFunc: deleteFile}
	keep := func(_, _ string, _ *metav1.ObjectMeta, _ ResourceMaps) bool { return false }
	require.NoError(t, h.cleanupNamespace(context.Background(), "ns", map[string][]TypeCleanupHandlerFunc{kind: {keep}}, conn, ResourceMaps{}))

	restored, err := afero.ReadFile(memFs, payload("kept"))
	require.NoError(t, err, "the payload with a live row was not put back")
	assert.Equal(t, "payload", string(restored))
	exists, _ := afero.Exists(memFs, payload("gone"))
	assert.False(t, exists, "a payload without a row must not come back")
	assert.Empty(t, listDeleting(t, memFs, dir))
}

// Without a gate, a writer on another connection replaces the row and the payload after
// the walk's recheck: the delete, in the same transaction as the recheck, fails on the
// stale snapshot and the replacement stays whole.
func TestCleanupLeavesAReplacementThatLandsAfterTheRecheck(t *testing.T) {
	const kind = "applicationprofiles"
	path := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, "ns", "app"+GobExt)
	key := payloadPathToKey(path)
	meta := func(rv string) []byte {
		return []byte(fmt.Sprintf(`{"name":"app","namespace":"ns","uid":"u%s","resourceVersion":%q}`, rv, rv))
	}
	memFs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(memFs, path, []byte("old payload"), 0644))
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)
	writer, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(writer)
	require.NoError(t, WriteJSON(conn, key, meta("1")))
	h := &ResourcesCleanupHandler{appFs: memFs, pool: pool, root: DefaultStorageRoot, deleteFunc: deleteFile}
	h.afterRecheck = func(k string) {
		if k == key {
			require.NoError(t, afero.WriteFile(memFs, path, []byte("new payload"), 0644))
			require.NoError(t, WriteJSON(writer, key, meta("2")))
		}
	}
	_ = h.cleanupNamespace(context.Background(), "ns", map[string][]TypeCleanupHandlerFunc{kind: {deleteDeprecated}}, conn, ResourceMaps{})

	content, err := afero.ReadFile(memFs, path)
	require.NoError(t, err, "the replacement payload was removed")
	assert.Equal(t, "new payload", string(content))
	row, err := ReadMetadata(conn, key)
	require.NoError(t, err, "the replacement row was removed")
	assert.Equal(t, string(meta("2")), string(row))
	assert.Empty(t, listDeleting(t, memFs, filepath.Dir(path)))
}

// A replacement that lands at the path between the walk's sighting of a reserved file and
// its settlement is the live payload: the reserved copy goes and the replacement stays.
func TestCleanupSettleLeavesAReplacementCreatedBeforeTheRestore(t *testing.T) {
	const kind = "applicationprofiles"
	dir := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, "ns")
	original := filepath.Join(dir, "app"+GobExt)
	reserved := original + ".0123456789abcdef" + cleanupDeletingSuffix
	key := payloadPathToKey(original)
	memFs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(memFs, reserved, []byte("stale payload"), 0644))
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)
	require.NoError(t, WriteJSON(conn, key, []byte(`{"name":"app","namespace":"ns","annotations":{"kubescape.io/image-id":"live"}}`)))
	h := &ResourcesCleanupHandler{appFs: memFs, pool: pool, root: DefaultStorageRoot, deleteFunc: deleteFile}
	h.beforeRestore = func(k string) {
		if k == key {
			require.NoError(t, afero.WriteFile(memFs, original, []byte("new payload"), 0644))
		}
	}
	keep := func(_, _ string, _ *metav1.ObjectMeta, _ ResourceMaps) bool { return false }
	require.NoError(t, h.cleanupNamespace(context.Background(), "ns", map[string][]TypeCleanupHandlerFunc{kind: {keep}}, conn, ResourceMaps{}))

	content, err := afero.ReadFile(memFs, original)
	require.NoError(t, err)
	assert.Equal(t, "new payload", string(content), "the replacement was clobbered by the stale payload")
	assert.Empty(t, listDeleting(t, memFs, dir))
}

func TestRenameUnlessPresent(t *testing.T) {
	for name, fs := range map[string]afero.Fs{"os": afero.NewOsFs(), "mem": afero.NewMemMapFs()} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name == "mem" {
				dir = "/d"
			}
			from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
			require.NoError(t, afero.WriteFile(fs, from, []byte("payload"), 0644))
			require.NoError(t, renameUnlessPresent(fs, from, to))
			got, err := afero.ReadFile(fs, to)
			require.NoError(t, err)
			assert.Equal(t, "payload", string(got))
			exists, _ := afero.Exists(fs, from)
			assert.False(t, exists)

			require.NoError(t, afero.WriteFile(fs, from, []byte("stale"), 0644))
			require.NoError(t, afero.WriteFile(fs, to, []byte("live"), 0644))
			assert.ErrorIs(t, renameUnlessPresent(fs, from, to), os.ErrExist)
			got, err = afero.ReadFile(fs, to)
			require.NoError(t, err)
			assert.Equal(t, "live", string(got), "a present target is never replaced")
		})
	}
}
