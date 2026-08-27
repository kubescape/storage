package file

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
