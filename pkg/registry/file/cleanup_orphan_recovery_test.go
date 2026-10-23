package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// cleanupDBNamespaceFetcher exercises the same metadata-backed namespace
// discovery as production, including when the last row disappears.
type cleanupDBNamespaceFetcher struct {
	staleWorkloadFetchMock
	visited []string
}

func (f *cleanupDBNamespaceFetcher) ListNamespaces(conn *sqlite.Conn) ([]string, error) {
	return listNamespaces(conn)
}

func (f *cleanupDBNamespaceFetcher) FetchResources(ns string) (ResourceMaps, error) {
	f.visited = append(f.visited, ns)
	return f.staleWorkloadFetchMock.FetchResources(ns)
}

func newOrphanRecoveryCleanup(t *testing.T, record cleanupRecord) (*ResourcesCleanupHandler, afero.Fs, *sqlitemigration.Pool) {
	t.Helper()
	fs := afero.NewMemMapFs()
	dir := t.TempDir()
	pool := NewTestPool(dir)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	fixture := openFixtureConn(t, pool, filepath.Join(dir, "test.sq3"), time.Second)
	require.NoError(t, afero.WriteFile(fs, record.path(), []byte("payload"), 0644))
	metadata, err := json.Marshal(metav1.ObjectMeta{Name: record.name, Namespace: record.ns, Annotations: record.annotations})
	require.NoError(t, err)
	require.NoError(t, WriteJSON(fixture, payloadPathToKey(record.path()), metadata))
	fetcher := &cleanupDBNamespaceFetcher{}
	return NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, "kubescape", fetcher, true), fs, pool
}

func TestCleanupRecoversPayloadAfterLastNamespaceRowIsDeleted(t *testing.T) {
	for _, gated := range []bool{false, true} {
		t.Run(fmt.Sprintf("gated=%t", gated), func(t *testing.T) {
			testCleanupRecoversPayloadAfterLastNamespaceRowIsDeleted(t, gated)
		})
	}
}

func testCleanupRecoversPayloadAfterLastNamespaceRowIsDeleted(t *testing.T, gated bool) {
	record := cleanupRecord{
		kind: "workloadconfigurationscansummaries", ns: "orphaned", name: "deployment-gone",
		annotations: map[string]string{"kubescape.io/wlid": "wlid://cluster-test/namespace-orphaned/deployment-gone"},
	}
	h, fs, pool := newOrphanRecoveryCleanup(t, record)
	var gate *WriteGate
	if gated {
		var err error
		gate, err = NewWriteGate(context.Background(), pool)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, gate.Close()) })
		h.SetWriteGate(gate)
	}

	// The first tick cannot remove the payload, but does remove the final row.
	h.appFs = afero.NewReadOnlyFs(fs)
	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.True(t, exists)
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	_, err = ReadMetadata(conn, payloadPathToKey(record.path()))
	require.ErrorIs(t, err, ErrMetadataNotFound)
	pool.Put(conn)

	// A fresh handler simulates restart: recovery cannot depend on in-memory
	// knowledge of the failed removal or an unrelated row in the namespace.
	fetcher := &cleanupDBNamespaceFetcher{}
	h = NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, "kubescape", fetcher, true)
	h.SetWriteGate(gate)
	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	exists, err = afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.False(t, exists, "a later cleanup tick must remove the stranded payload")
}

type signalSidecarStatFs struct {
	afero.Fs
	sidecar string
	seen    chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (fs *signalSidecarStatFs) Stat(name string) (os.FileInfo, error) {
	if name == fs.sidecar {
		fs.once.Do(func() {
			close(fs.seen)
			<-fs.resume
		})
	}
	return fs.Fs.Stat(name)
}

func TestCleanupDoesNotRemoveUncommittedCreatePayload(t *testing.T) {
	for _, gated := range []bool{false, true} {
		t.Run(fmt.Sprintf("gated=%t", gated), func(t *testing.T) {
			testCleanupDoesNotRemoveUncommittedCreatePayload(t, gated)
		})
	}
}

func testCleanupDoesNotRemoveUncommittedCreatePayload(t *testing.T, gated bool) {
	record := cleanupRecord{
		kind: "workloadconfigurationscansummaries", ns: "new-namespace", name: "deployment-new",
		annotations: map[string]string{"kubescape.io/wlid": "wlid://cluster-test/namespace-new-namespace/deployment-new"},
	}
	fs := afero.NewMemMapFs()
	pool := NewPool(filepath.Join(t.TempDir(), "metadata.sq3"), 4, 2*time.Second)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	var gate *WriteGate
	var err error
	if gated {
		gate, err = NewWriteGate(context.Background(), pool)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, gate.Close()) })
	}
	stage := record.path() + ".staged"
	require.NoError(t, afero.WriteFile(fs, stage, []byte("new payload"), 0644))
	metadata := []byte(`{"name":"deployment-new","namespace":"new-namespace"}`)
	writerReady := make(chan struct{})
	writerRelease := make(chan struct{})
	writerDone := make(chan error, 1)
	var releaseOnce sync.Once
	releaseWriter := func() { releaseOnce.Do(func() { close(writerRelease) }) }
	defer releaseWriter()
	if gated {
		go func() {
			writerDone <- gate.run(context.Background(), priorityHigh, holdPathLegacyCommit, record.kind, func(_ context.Context, conn *sqlite.Conn) error {
				if err := WriteJSON(conn, payloadPathToKey(record.path()), metadata); err != nil {
					return err
				}
				if err := fs.Rename(stage, record.path()); err != nil {
					return err
				}
				close(writerReady)
				<-writerRelease
				return nil
			})
		}()
	} else {
		writerConn, err := pool.Take(context.Background())
		require.NoError(t, err)
		end, err := sqlitex.ImmediateTransaction(writerConn)
		require.NoError(t, err)
		t.Cleanup(func() {
			if !writerConn.AutocommitEnabled() {
				rollback := errors.New("test rollback")
				end(&rollback)
			}
			pool.Put(writerConn)
		})
		require.NoError(t, WriteJSON(writerConn, payloadPathToKey(record.path()), metadata))
		require.NoError(t, fs.Rename(stage, record.path()))
		close(writerReady)
		go func() {
			<-writerRelease
			var commitErr error
			end(&commitErr)
			writerDone <- commitErr
		}()
	}
	select {
	case <-writerReady:
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not prepare the payload")
	}

	sidecar := strings.TrimSuffix(record.path(), GobExt) + MetadataExt
	wrapped := &signalSidecarStatFs{Fs: fs, sidecar: sidecar, seen: make(chan struct{}), resume: make(chan struct{})}
	fetcher := &cleanupDBNamespaceFetcher{}
	h := NewResourcesCleanupHandler(wrapped, DefaultStorageRoot, pool, nil, 0, "kubescape", fetcher, true)
	h.SetWriteGate(gate)
	done := make(chan error, 1)
	go func() { done <- h.CleanupTask(context.Background(), h.resourceToKindHandler) }()
	select {
	case <-wrapped.seen:
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not inspect the candidate payload")
	}
	close(wrapped.resume)
	// Before the writer commits, a recovery pass must not remove its payload.
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.True(t, exists, "an uncommitted create is not an orphan")
	releaseWriter()
	select {
	case err := <-writerDone:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("writer did not commit")
	}
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup did not finish after writer committed")
	}
	exists, err = afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.True(t, exists)
}

type failQuarantineRemoveFs struct{ afero.Fs }

func (fs failQuarantineRemoveFs) Remove(path string) error {
	if isCleanupQuarantineFile(path) {
		return os.ErrPermission
	}
	return fs.Fs.Remove(path)
}

type failNamedRemoveFs struct {
	afero.Fs
	blocked string
}

func (fs failNamedRemoveFs) Remove(path string) error {
	if path == fs.blocked {
		return os.ErrPermission
	}
	return fs.Fs.Remove(path)
}

func TestCleanupRetriesQuarantinedOrphanWithoutAnotherWatchEvent(t *testing.T) {
	record := cleanupRecord{
		kind: "workloadconfigurationscansummaries", ns: "orphaned", name: "deployment-gone",
		annotations: map[string]string{"kubescape.io/wlid": "wlid://cluster-test/namespace-orphaned/deployment-gone"},
	}
	h, fs, pool := newOrphanRecoveryCleanup(t, record)
	dispatcher := NewWatchDispatcher()
	w := newWatcher(context.Background(), true)
	t.Cleanup(w.Stop)
	dispatcher.Register("/"+softwarecomposition.GroupName+"/"+record.kind, w)
	h.watchDispatcher = dispatcher

	h.appFs = afero.NewReadOnlyFs(fs)
	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	select {
	case event := <-w.ResultChan():
		require.Equal(t, watch.Deleted, event.Type)
	case <-time.After(5 * time.Second):
		t.Fatal("expected the one logical deletion event")
	}

	h.appFs = failQuarantineRemoveFs{fs}
	require.ErrorContains(t, h.CleanupTask(context.Background(), h.resourceToKindHandler), "remove orphan payload")
	entries, err := afero.ReadDir(fs, filepath.Dir(record.path()))
	require.NoError(t, err)
	require.Len(t, entries, 1)
	quarantine := filepath.Join(filepath.Dir(record.path()), entries[0].Name())
	require.True(t, isCleanupQuarantineFile(quarantine))
	require.False(t, IsPayloadFile(quarantine), "quarantine must not be returned by file-backed LIST")
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.False(t, exists)
	var list softwarecomposition.WorkloadConfigurationScanSummaryList
	s := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, nil).(*StorageImpl)
	require.NoError(t, s.GetByCluster(context.Background(), softwarecomposition.GroupName, record.kind, &list))
	require.Empty(t, list.Items)

	// A fresh handler finds the reserved non-payload file even though SQLite
	// no longer knows the namespace, and removes it without a second event.
	h = NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, dispatcher, 0, "kubescape", &cleanupDBNamespaceFetcher{}, true)
	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	exists, err = afero.Exists(fs, quarantine)
	require.NoError(t, err)
	require.False(t, exists)
	select {
	case event := <-w.ResultChan():
		t.Fatalf("orphan file recovery emitted a second event: %v", event.Type)
	default:
	}
}

func TestCleanupPreservesLegacySidecarPayload(t *testing.T) {
	record := cleanupRecord{kind: "workloadconfigurationscansummaries", ns: "legacy", name: "deployment-old"}
	h, fs, pool := newNamespaceCleanup(t, &namespaceFetcher{}, nil)
	fetcher := &cleanupDBNamespaceFetcher{}
	h.fetcher = fetcher
	require.NoError(t, afero.WriteFile(fs, record.path(), []byte("legacy payload"), 0644))
	sidecar := strings.TrimSuffix(record.path(), GobExt) + MetadataExt
	require.NoError(t, afero.WriteFile(fs, sidecar, []byte(`{"name":"deployment-old","namespace":"legacy"}`), 0644))

	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	_, err = ReadMetadata(conn, payloadPathToKey(record.path()))
	pool.Put(conn)
	require.ErrorIs(t, err, ErrMetadataNotFound, "orphan recovery must leave legacy migration to its normal path")
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.True(t, exists)
	exists, err = afero.Exists(fs, sidecar)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, []string{"kubescape"}, fetcher.visited, "recovery needs no workload query in the first tick")
}

func TestCleanupOrphanSweepUsesConfiguredRootForMetadataLookup(t *testing.T) {
	const root = "/custom/storage/"
	path := filepath.Join(root, softwarecomposition.GroupName, "workloadconfigurationscansummaries", "unlisted", "still-current"+GobExt)
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, path, []byte("payload"), 0644))
	dir := t.TempDir()
	pool := NewTestPool(dir)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	fixture := openFixtureConn(t, pool, filepath.Join(dir, "test.sq3"), time.Second)
	key, err := keyFromPayloadPath(root, path)
	require.NoError(t, err)
	require.NoError(t, WriteJSON(fixture, key, []byte(`{"name":"still-current","namespace":"unlisted"}`)))
	// Force the namespace into the sweep despite its row, so a wrong key
	// derived from the textual root would delete a current payload.
	h := NewResourcesCleanupHandler(fs, root, pool, nil, 0, "kubescape", &namespaceFetcher{}, true)
	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	exists, err := afero.Exists(fs, path)
	require.NoError(t, err)
	require.True(t, exists)
}

func TestCleanupOrphanSweepStopsOnMetadataReadError(t *testing.T) {
	record := cleanupRecord{kind: "workloadconfigurationscansummaries", ns: "unlisted", name: "unknown"}
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, record.path(), []byte("payload"), 0644))
	dir := t.TempDir()
	pool := NewTestPool(dir)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	fixture := openFixtureConn(t, pool, filepath.Join(dir, "test.sq3"), time.Second)
	require.NoError(t, sqlitex.ExecuteTransient(fixture, `DROP TABLE metadata`, nil))
	h := NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, "kubescape", &namespaceFetcher{}, true)
	require.ErrorContains(t, h.CleanupTask(context.Background(), h.resourceToKindHandler), "read metadata")
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.True(t, exists, "uncertain metadata state must preserve the payload")
}

func TestCleanupQuarantineFailureDoesNotBlockListedNamespace(t *testing.T) {
	record := cleanupRecord{
		kind: "workloadconfigurationscansummaries", ns: "listed", name: "deployment-gone",
		annotations: map[string]string{"kubescape.io/wlid": "wlid://cluster-test/namespace-listed/deployment-gone"},
	}
	h, fs, _ := newOrphanRecoveryCleanup(t, record)
	quarantine := filepath.Join(filepath.Dir(record.path()), cleanupQuarantinePrefix+strings.Repeat("0", 32)+cleanupQuarantineSuffix)
	require.NoError(t, afero.WriteFile(fs, quarantine, []byte("old orphan"), 0644))
	h.appFs = failQuarantineRemoveFs{fs}
	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.False(t, exists, "a failed quarantine retry must not block ordinary cleanup")
	exists, err = afero.Exists(fs, quarantine)
	require.NoError(t, err)
	require.True(t, exists, "quarantine removal is retried on a later tick")
}

func TestCleanupOrphanSweepContinuesAfterOneRemovalFailure(t *testing.T) {
	record := cleanupRecord{kind: "workloadconfigurationscansummaries", ns: "unlisted", name: "recover-me"}
	fs := afero.NewMemMapFs()
	require.NoError(t, afero.WriteFile(fs, record.path(), []byte("orphan"), 0644))
	blocked := filepath.Join(filepath.Dir(record.path()), cleanupQuarantinePrefix+strings.Repeat("0", 32)+cleanupQuarantineSuffix)
	require.NoError(t, afero.WriteFile(fs, blocked, []byte("old orphan"), 0644))
	pool := NewTestPool(t.TempDir())
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	h := NewResourcesCleanupHandler(failNamedRemoveFs{Fs: fs, blocked: blocked}, DefaultStorageRoot, pool, nil, 0, "kubescape", &cleanupDBNamespaceFetcher{}, true)
	require.ErrorContains(t, h.CleanupTask(context.Background(), h.resourceToKindHandler), "remove orphan payload")
	exists, err := afero.Exists(fs, record.path())
	require.NoError(t, err)
	require.False(t, exists, "a failed retry must not block another orphan in the same directory")
	exists, err = afero.Exists(fs, blocked)
	require.NoError(t, err)
	require.True(t, exists)
}
