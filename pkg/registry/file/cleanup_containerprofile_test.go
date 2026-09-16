package file

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/watch"
	"zombiezen.com/go/sqlite"
)

// containerProfileFetchMock reports a single running pod in the default namespace.
type containerProfileFetchMock struct {
	runningWlid string
}

var _ ResourcesFetcher = (*containerProfileFetchMock)(nil)

func (r *containerProfileFetchMock) ListNamespaces(_ *sqlite.Conn) ([]string, error) {
	return []string{"default"}, nil
}

func (r *containerProfileFetchMock) FetchResources(_ string) (ResourceMaps, error) {
	resourceMaps := ResourceMaps{
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}
	resourceMaps.RunningWlidsToContainerNames.Set(r.runningWlid, mapset.NewSet[string]("main"))
	return resourceMaps, nil
}

// container profiles are stored under the singular kind segment, so a handler keyed
// by the plural resource name walks a directory that does not exist and deletes nothing
func TestContainerProfileCleanupUsesStorageKind(t *testing.T) {
	memFs := afero.NewMemMapFs()

	profilePath := func(name string) string {
		return filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, ContainerProfileKind, "default", name+GobExt)
	}
	metadataJSON := func(name, wlid string) []byte {
		return []byte(fmt.Sprintf(`{"name":%q,"namespace":"default","annotations":{"kubescape.io/wlid":%q},"labels":{"kubescape.io/workload-kind":"Pod"}}`, name, wlid))
	}

	const (
		staleName = "pod-deleted-main-1234-5678"
		liveName  = "pod-running-main-8765-4321"
		staleWlid = "wlid://cluster-test/namespace-default/pod-deleted"
		liveWlid  = "wlid://cluster-test/namespace-default/pod-running"
	)

	require.NoError(t, afero.WriteFile(memFs, profilePath(staleName), []byte("payload"), 0644))
	require.NoError(t, afero.WriteFile(memFs, profilePath(liveName), []byte("payload"), 0644))

	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(profilePath(staleName)), metadataJSON(staleName, staleWlid)))
	require.NoError(t, WriteJSON(conn, payloadPathToKey(profilePath(liveName)), metadataJSON(liveName, liveWlid)))
	pool.Put(conn)

	handler := &ResourcesCleanupHandler{
		appFs:            memFs,
		pool:             pool,
		root:             DefaultStorageRoot,
		defaultNamespace: "kubescape",
		fetcher:          &containerProfileFetchMock{runningWlid: wlidWithoutClusterName(liveWlid)},
		deleteFunc:       deleteFile,
	}

	processor := ContainerProfileProcessor{CleanupHandler: handler}
	require.NoError(t, processor.cleanup())

	staleExists, err := afero.Exists(memFs, profilePath(staleName))
	require.NoError(t, err)
	assert.False(t, staleExists, "container profile of a deleted pod should be reclaimed")

	liveExists, err := afero.Exists(memFs, profilePath(liveName))
	require.NoError(t, err)
	assert.True(t, liveExists, "container profile of a running pod should be kept")
}

// TestContainerProfileCleanupDispatchesRegisteredTypeToWatchers is a
// regression test for issue #405 as it applies to ContainerProfileKind
// specifically: ContainerProfileProcessor.cleanup builds its own
// resourceToKindHandler keyed by ContainerProfileKind and runs it through the
// same CleanupHandler.CleanupTask / cleanupNamespace / deleteMetadata path as
// every other cleanup-handled kind, so a delete here must also dispatch a
// scheme-registered *softwarecomposition.ContainerProfile to active watchers
// (not the unregistered file.PartialObjectMetadata helper, and not silently
// nothing).
func TestContainerProfileCleanupDispatchesRegisteredTypeToWatchers(t *testing.T) {
	memFs := afero.NewMemMapFs()

	profilePath := func(name string) string {
		return filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, ContainerProfileKind, "default", name+GobExt)
	}
	metadataJSON := func(name, wlid string) []byte {
		return fmt.Appendf(nil, `{"name":%q,"namespace":"default","annotations":{"kubescape.io/wlid":%q},"labels":{"kubescape.io/workload-kind":"Pod"}}`, name, wlid)
	}

	const (
		staleName = "pod-deleted-main-1234-5678"
		staleWlid = "wlid://cluster-test/namespace-default/pod-deleted"
	)

	require.NoError(t, afero.WriteFile(memFs, profilePath(staleName), []byte("payload"), 0644))

	pool := NewTestPool(t.TempDir())
	defer func() { _ = pool.Close() }()
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(profilePath(staleName)), metadataJSON(staleName, staleWlid)))
	pool.Put(conn)

	watchDispatcher := NewWatchDispatcher()
	watchKey := "/" + softwarecomposition.GroupName + "/" + ContainerProfileKind
	w := newWatcher(context.Background(), true)
	defer w.Stop()
	watchDispatcher.Register(watchKey, w)

	handler := &ResourcesCleanupHandler{
		appFs:            memFs,
		pool:             pool,
		root:             DefaultStorageRoot,
		defaultNamespace: "kubescape",
		fetcher:          &containerProfileFetchMock{runningWlid: "cluster-test/namespace-other/pod-running"},
		deleteFunc:       deleteFile,
		watchDispatcher:  watchDispatcher,
	}

	processor := ContainerProfileProcessor{CleanupHandler: handler}
	require.NoError(t, processor.cleanup())

	staleExists, err := afero.Exists(memFs, profilePath(staleName))
	require.NoError(t, err)
	assert.False(t, staleExists, "orphaned container profile should have been deleted")

	select {
	case ev := <-w.ResultChan():
		assert.Equal(t, watch.Deleted, ev.Type)
		profile, ok := ev.Object.(*softwarecomposition.ContainerProfile)
		require.True(t, ok, "watch dispatcher must send a scheme-registered type, not file.PartialObjectMetadata; got %T", ev.Object)
		assert.Equal(t, staleName, profile.Name)
		assert.Equal(t, "default", profile.Namespace)
	case <-time.After(chanWaitTimeout):
		t.Fatal("expected a Deleted watch event for the reclaimed container profile")
	}
}
