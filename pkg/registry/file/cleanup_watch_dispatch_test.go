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

// staleWorkloadFetchMock reports no running resources, so any resource found
// during cleanup is considered orphaned and deleted.
type staleWorkloadFetchMock struct{}

var _ ResourcesFetcher = (*staleWorkloadFetchMock)(nil)

func (staleWorkloadFetchMock) ListNamespaces(_ *sqlite.Conn) ([]string, error) {
	return []string{"default"}, nil
}

func (staleWorkloadFetchMock) FetchResources(_ string) (ResourceMaps, error) {
	return ResourceMaps{
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}, nil
}

// TestCleanupNamespaceDispatchesRegisteredTypeToWatchers is a regression test
// for issue #405: the periodic cleanup loop used to dispatch delete events to
// active watchers using the internal, apiserver-Scheme-unregistered
// file.PartialObjectMetadata helper. That broke k8s.io/apiserver's watch
// encoder ("no kind is registered for the type file.PartialObjectMetadata").
// Deleting a resource kind that still has a REST endpoint (and therefore can
// have a real watcher) must dispatch a properly scheme-registered API type
// instead. See TestDeletedEventEncodesThroughApiserverScheme (apiserver
// package) for the actual HTTP-serialization-boundary assertion.
func TestCleanupNamespaceDispatchesRegisteredTypeToWatchers(t *testing.T) {
	memFs := afero.NewMemMapFs()

	const (
		kind = "workloadconfigurationscans"
		ns   = "default"
		name = "deployment-gone-1234"
		wlid = "wlid://cluster-test/namespace-default/pod-gone"
	)

	payloadPath := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, ns, name+GobExt)
	require.NoError(t, afero.WriteFile(memFs, payloadPath, []byte("payload"), 0644))

	pool := NewTestPool(t.TempDir())
	defer func() { _ = pool.Close() }()
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	metadataJSON := fmt.Appendf(nil, `{"name":%q,"namespace":%q,"annotations":{"kubescape.io/wlid":%q}}`, name, ns, wlid)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(payloadPath), metadataJSON))
	pool.Put(conn)

	watchDispatcher := NewWatchDispatcher()
	watchKey := "/" + softwarecomposition.GroupName + "/" + kind
	w := newWatcher(context.Background(), true)
	defer w.Stop()
	watchDispatcher.Register(watchKey, w)

	handler := &ResourcesCleanupHandler{
		appFs:            memFs,
		pool:             pool,
		root:             DefaultStorageRoot,
		defaultNamespace: "kubescape", // must differ from ns so cleanupNamespace runs for ns in CleanupTask's main loop
		fetcher:          staleWorkloadFetchMock{},
		deleteFunc:       deleteFile,
		watchDispatcher:  watchDispatcher,
	}
	handler.resourceToKindHandler = initResourceToKindHandler(false)

	require.NoError(t, handler.CleanupTask(context.Background(), handler.resourceToKindHandler))

	exists, err := afero.Exists(memFs, payloadPath)
	require.NoError(t, err)
	assert.False(t, exists, "orphaned workload configuration scan should have been deleted")

	select {
	case ev := <-w.ResultChan():
		assert.Equal(t, watch.Deleted, ev.Type)
		scan, ok := ev.Object.(*softwarecomposition.WorkloadConfigurationScan)
		require.True(t, ok, "watch dispatcher must send a scheme-registered type, not file.PartialObjectMetadata; got %T", ev.Object)
		assert.Equal(t, name, scan.Name)
		assert.Equal(t, ns, scan.Namespace)
	case <-time.After(chanWaitTimeout):
		t.Fatal("expected a Deleted watch event")
	}
}

// TestCleanupNamespaceSkipsDispatchForDeprecatedKind ensures a deprecated
// resource kind with no registered API type left (see
// resourceKindToObjectFunc) never reaches the watch dispatcher, instead of
// falling back to the unregistered file.PartialObjectMetadata helper.
func TestCleanupNamespaceSkipsDispatchForDeprecatedKind(t *testing.T) {
	memFs := afero.NewMemMapFs()

	const (
		kind = "applicationprofiles"
		ns   = "default"
		name = "deprecated-profile"
	)

	payloadPath := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, ns, name+GobExt)
	require.NoError(t, afero.WriteFile(memFs, payloadPath, []byte("payload"), 0644))

	pool := NewTestPool(t.TempDir())
	defer func() { _ = pool.Close() }()
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	metadataJSON := fmt.Appendf(nil, `{"name":%q,"namespace":%q}`, name, ns)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(payloadPath), metadataJSON))
	pool.Put(conn)

	watchDispatcher := NewWatchDispatcher()
	watchKey := "/" + softwarecomposition.GroupName + "/" + kind
	w := newWatcher(context.Background(), true)
	defer w.Stop()
	watchDispatcher.Register(watchKey, w)

	handler := &ResourcesCleanupHandler{
		appFs:            memFs,
		pool:             pool,
		root:             DefaultStorageRoot,
		defaultNamespace: "kubescape",
		fetcher:          staleWorkloadFetchMock{},
		deleteFunc:       deleteFile,
		watchDispatcher:  watchDispatcher,
	}
	handler.resourceToKindHandler = initResourceToKindHandler(false)

	require.NoError(t, handler.CleanupTask(context.Background(), handler.resourceToKindHandler))

	exists, err := afero.Exists(memFs, payloadPath)
	require.NoError(t, err)
	assert.False(t, exists, "deprecated profile should still be deleted from disk")

	select {
	case ev := <-w.ResultChan():
		t.Fatalf("expected no watch event for a deprecated kind with no registered type, got %#v", ev)
	case <-time.After(chanWaitTimeout):
		// expected: nothing dispatched
	}
}
