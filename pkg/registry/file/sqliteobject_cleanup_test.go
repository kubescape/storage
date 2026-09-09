package file

// K-4 / §5.6 row 10 of .omc/plans/full-acid-storage-architecture.md: the
// ContainerProfile cleanup handlers (deleteByTemplateHashOrWlid and, with
// relevancy on, the two missing-annotation handlers) run ONLY from
// ContainerProfileProcessor.cleanup(); main.go's generic relevancy walk never
// contains ContainerProfileKind and never visits a CP directory. Under the
// flag the CP arm enumerates rows and deletes through the ObjectStore (one
// gated transaction over metadata + payloads + time_series, Deleted
// dispatched after) and never walks files.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

// pathSpyFs records every path the cleanup walk touches through the fs.
type pathSpyFs struct {
	afero.Fs
	mu    sync.Mutex
	paths []string
}

func (s *pathSpyFs) note(p string) {
	s.mu.Lock()
	s.paths = append(s.paths, p)
	s.mu.Unlock()
}

func (s *pathSpyFs) Open(name string) (afero.File, error) {
	s.note(name)
	return s.Fs.Open(name)
}

func (s *pathSpyFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	s.note(name)
	return s.Fs.OpenFile(name, flag, perm)
}

func (s *pathSpyFs) Stat(name string) (os.FileInfo, error) {
	s.note(name)
	return s.Fs.Stat(name)
}

func (s *pathSpyFs) Remove(name string) error {
	s.note(name)
	return s.Fs.Remove(name)
}

// cpPathsVisited returns the recorded paths under the containerprofile kind.
func (s *pathSpyFs) cpPathsVisited() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, p := range s.paths {
		if strings.Contains(p, "/"+ContainerProfileKind+"/") {
			out = append(out, p)
		}
	}
	return out
}

// nsFetchMock reports one namespace with one running pod wlid.
type nsFetchMock struct {
	ns          string
	runningWlid string
}

func (m *nsFetchMock) ListNamespaces(_ *sqlite.Conn) ([]string, error) { return []string{m.ns}, nil }

func (m *nsFetchMock) FetchResources(_ string) (ResourceMaps, error) {
	r := ResourceMaps{
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}
	r.RunningWlidsToContainerNames.Set(wlidWithoutClusterName(m.runningWlid), mapset.NewSet[string]("main"))
	return r, nil
}

const (
	cleanupLiveWlid  = "wlid://cluster-test/namespace-default/pod-running"
	cleanupStaleWlid = "wlid://cluster-test/namespace-default/pod-deleted"
)

func cpFilePath(ns, name string) string {
	return filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, ContainerProfileKind, ns, name+GobExt)
}

func cpMetadataJSON(ns, name string, annotations map[string]string) []byte {
	parts := make([]string, 0, len(annotations))
	for k, v := range annotations {
		parts = append(parts, fmt.Sprintf("%q:%q", k, v))
	}
	return []byte(fmt.Sprintf(`{"name":%q,"namespace":%q,"annotations":{%s},"labels":{"kubescape.io/workload-kind":"Pod"}}`,
		name, ns, strings.Join(parts, ",")))
}

// The generic relevancy walk (main.go's cleanup goroutine) never contains the
// ContainerProfile kind and never visits a CP directory, even with relevancy
// on: a CP file whose row lacks both relevancy annotations survives it.
func TestCleanup_GenericWalkNeverVisitsContainerProfiles(t *testing.T) {
	memFs := afero.NewMemMapFs()
	const name = "pod-running-main-1111-2222"
	require.NoError(t, afero.WriteFile(memFs, cpFilePath("default", name), []byte("payload"), 0644))
	// A deprecated kind's file in the same namespace proves the walk itself ran.
	deprecated := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, "applicationprofiles", "default", "ap"+GobExt)
	require.NoError(t, afero.WriteFile(memFs, deprecated, []byte("payload"), 0644))

	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(cpFilePath("default", name)), cpMetadataJSON("default", name, map[string]string{})))
	require.NoError(t, WriteJSON(conn, payloadPathToKey(deprecated), []byte(`{"name":"ap","namespace":"default"}`)))
	pool.Put(conn)

	spy := &pathSpyFs{Fs: memFs}
	h := NewResourcesCleanupHandler(spy, DefaultStorageRoot, pool, nil, 0, "kubescape", &nsFetchMock{ns: "default", runningWlid: cleanupLiveWlid}, true)
	_, hasCP := h.resourceToKindHandler[ContainerProfileKind]
	require.False(t, hasCP, "initResourceToKindHandler must never map ContainerProfileKind")
	_, hasCP = initResourceToKindHandler()[ContainerProfileKind]
	require.False(t, hasCP)

	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))

	exists, err := afero.Exists(memFs, deprecated)
	require.NoError(t, err)
	require.False(t, exists, "the walk ran: the deprecated kind's file is reclaimed")
	exists, err = afero.Exists(memFs, cpFilePath("default", name))
	require.NoError(t, err)
	require.True(t, exists, "the generic walk must not reclaim a ContainerProfile")
	require.Empty(t, spy.cpPathsVisited(), "the generic walk must not visit the containerprofile directory")
	conn, err = pool.Take(context.Background())
	require.NoError(t, err)
	_, err = ReadMetadata(conn, payloadPathToKey(cpFilePath("default", name)))
	pool.Put(conn)
	require.NoError(t, err, "the ContainerProfile row must survive the generic walk")
}

// With relevancy on, the missing-annotation handlers run from the processor's
// cleanup: a profile of a running workload with no instance-id annotation is
// reclaimed there (deleteByTemplateHashOrWlid alone keeps it).
func TestCleanup_RelevancyHandlersRunFromTheProcessorCleanup(t *testing.T) {
	memFs := afero.NewMemMapFs()
	const (
		complete   = "pod-running-main-1111-2222"
		noInstance = "pod-running-main-3333-4444"
	)
	for _, n := range []string{complete, noInstance} {
		require.NoError(t, afero.WriteFile(memFs, cpFilePath("default", n), []byte("payload"), 0644))
	}
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(cpFilePath("default", complete)), cpMetadataJSON("default", complete, map[string]string{
		helpersv1.WlidMetadataKey: cleanupLiveWlid, helpersv1.InstanceIDMetadataKey: "iid",
	})))
	require.NoError(t, WriteJSON(conn, payloadPathToKey(cpFilePath("default", noInstance)), cpMetadataJSON("default", noInstance, map[string]string{
		helpersv1.WlidMetadataKey: cleanupLiveWlid,
	})))
	pool.Put(conn)

	for _, relevancy := range []bool{false, true} {
		t.Run(fmt.Sprintf("relevancy=%v", relevancy), func(t *testing.T) {
			h := NewResourcesCleanupHandler(memFs, DefaultStorageRoot, pool, nil, 0, "kubescape", &nsFetchMock{ns: "default", runningWlid: cleanupLiveWlid}, relevancy)
			processor := ContainerProfileProcessor{CleanupHandler: h}
			require.NoError(t, processor.cleanup())

			exists, err := afero.Exists(memFs, cpFilePath("default", complete))
			require.NoError(t, err)
			require.True(t, exists, "a running workload's complete profile is kept")
			exists, err = afero.Exists(memFs, cpFilePath("default", noInstance))
			require.NoError(t, err)
			require.Equal(t, !relevancy, exists, "the missing-instance-id profile is reclaimed exactly when relevancy is on")
		})
	}
}

// Under the flag the CP arm enumerates rows and deletes through the
// ObjectStore: the reclaimed profile leaves no row in any of the three
// tables, the kept one is intact, and no CP file path is visited.
func TestCleanup_ContainerProfileArmUsesRowsUnderTheFlag(t *testing.T) {
	e := newObjectStoreEnv(t)
	// Without the template-hash label the liveness decision is the wlid's.
	stale := e.plain("pod-deleted-main-1234-5678")
	stale.Annotations[helpersv1.WlidMetadataKey] = cleanupStaleWlid
	delete(stale.Labels, helpersv1.TemplateHashKey)
	live := e.plain("pod-running-main-8765-4321")
	live.Annotations[helpersv1.WlidMetadataKey] = cleanupLiveWlid
	delete(live.Labels, helpersv1.TemplateHashKey)
	e.create(stale)
	e.create(live)
	staleKey, liveKey := e.key(stale.Name), e.key(live.Name)

	// A stray legacy file for the stale key: a file walk would find it and
	// delete file-then-row on the legacy path, leaving the payloads row.
	spy := &pathSpyFs{Fs: e.legacyFs}
	require.NoError(t, afero.WriteFile(e.legacyFs, cpFilePath(e.ns, stale.Name), []byte("legacy"), 0644))

	h := NewResourcesCleanupHandler(spy, DefaultStorageRoot, e.pool, e.wd, 0, "kubescape", &nsFetchMock{ns: e.ns, runningWlid: cleanupLiveWlid}, false)
	h.SetWriteGate(e.gate)
	h.SetContainerProfileStore(e.store)
	w, err := e.store.Watch(e.ctx, testCPPrefix+e.ns, storage.ListOptions{Predicate: storage.Everything, Recursive: true})
	require.NoError(t, err)
	defer w.Stop()

	processor := ContainerProfileProcessor{CleanupHandler: h}
	require.NoError(t, processor.cleanup())

	row := e.inspect(staleKey)
	require.False(t, row.metaExists, "the stale profile's metadata row is reclaimed")
	require.False(t, row.payloadExists, "the stale profile's payloads row is reclaimed with it (INV-2)")
	require.Equal(t, 0, row.tsRows)
	e.withConn(func(conn *sqlite.Conn) { assertINV2(t, conn, liveKey) })
	require.True(t, e.inspect(liveKey).metaExists, "the running workload's profile is kept")
	require.Empty(t, spy.cpPathsVisited(), "the CP arm must not walk files under the flag")
	exists, err := afero.Exists(e.legacyFs, cpFilePath(e.ns, stale.Name))
	require.NoError(t, err)
	require.True(t, exists, "legacy files are left for the export tool, never reclaimed by the cleanup")

	select {
	case ev := <-w.ResultChan():
		require.Equal(t, "DELETED", string(ev.Type))
	default:
		t.Fatal("expected a Deleted event for the reclaimed profile")
	}
}
