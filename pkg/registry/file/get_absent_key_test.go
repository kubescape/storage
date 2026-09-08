package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/storage"
)

// AC-B1: a Get of a key that has neither payload nor metadata row is a read
// and must not wait on SQLite's write lock. Before the fix get() issued
// DeleteMetadata unconditionally on a missing payload; a DELETE that matches
// no row still opens a write transaction, so the Get blocked for the busy
// timeout behind any in-flight writer -- including the caller's own.
func TestGet_AbsentKeyDoesNotWaitOnWriter(t *testing.T) {
	s := newSelfStallStorage(t, DefaultProcessor{})
	_, release := holdWriteLock(t, s)
	defer release()

	var out softwarecomposition.ContainerProfile
	start := time.Now()
	err := s.Get(context.Background(), "/spdx.softwarecomposition.kubescape.io/containerprofile/ns/absent", storage.GetOptions{IgnoreNotFound: true}, &out)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Empty(t, out.Name)
	assert.Less(t, elapsed, selfStallBound, "Get of an absent key must not wait on the write lock (busy timeout %s)", selfStallBusyTimeout)
}

// AC-B2: the orphan-prune behaviour get() has had since a51d55f4 is kept: a
// metadata row whose payload file is gone is deleted on read.
func TestGet_PrunesOrphanedMetadataRow(t *testing.T) {
	s := newSelfStallStorage(t, DefaultProcessor{})
	ctx := context.Background()

	content, err := os.ReadFile("testdata/p1.json")
	require.NoError(t, err)
	var profile softwarecomposition.ContainerProfile
	require.NoError(t, json.Unmarshal(content, &profile))
	key := "/spdx.softwarecomposition.kubescape.io/containerprofile/" + profile.Namespace + "/" + profile.Name
	require.NoError(t, s.Create(ctx, key, &profile, nil, 0))

	conn, err := s.pool.Take(ctx)
	require.NoError(t, err)
	_, err = ReadMetadata(conn, key)
	require.NoError(t, err, "row exists after Create")
	s.pool.Put(conn)

	require.NoError(t, s.appFs.Remove(makePayloadPath(filepath.Join(s.root, key))))

	var out softwarecomposition.ContainerProfile
	err = s.Get(ctx, key, storage.GetOptions{}, &out)
	assert.True(t, storage.IsNotFound(err), "missing payload reads as not found, got %v", err)

	conn, err = s.pool.Take(ctx)
	require.NoError(t, err)
	defer s.pool.Put(conn)
	_, err = ReadMetadata(conn, key)
	assert.ErrorIs(t, err, ErrMetadataNotFound, "orphaned row is pruned")
}
