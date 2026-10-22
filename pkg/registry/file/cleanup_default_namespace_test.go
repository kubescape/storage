package file

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// namespaceFetcher answers FetchResources per namespace and records the calls.
type namespaceFetcher struct {
	staleWorkloadFetchMock
	namespaces []string
	running    map[string]ResourceMaps
	failing    string
	calls      []string
}

func (f *namespaceFetcher) ListNamespaces(*sqlite.Conn) ([]string, error) {
	return f.namespaces, nil
}

func (f *namespaceFetcher) FetchResources(ns string) (ResourceMaps, error) {
	f.calls = append(f.calls, ns)
	if ns == f.failing {
		return ResourceMaps{}, errors.New("pods is forbidden")
	}
	if r, ok := f.running[ns]; ok {
		return r, nil
	}
	return f.staleWorkloadFetchMock.FetchResources(ns)
}

// runningIn is what runs in ns: one image, one instance, and a deployment with one
// container and its template hash.
func runningIn(ns string) ResourceMaps {
	r := ResourceMaps{
		RunningContainerImageIds:     mapset.NewSet("image-" + ns),
		RunningInstanceIds:           mapset.NewSet("instance-" + ns),
		RunningTemplateHash:          mapset.NewSet("hash-" + ns),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}
	r.RunningWlidsToContainerNames.Set("namespace-"+ns+"/deployment-app", mapset.NewSet("app"))
	return r
}

type cleanupRecord struct {
	kind, ns, name      string
	labels, annotations map[string]string
	stale               bool
}

// namespaceRecords are the records the scanners store for what runs in ns, and, when
// withStale is set, the summary of a deployment that no longer runs there.
func namespaceRecords(ns string, withStale bool) []cleanupRecord {
	wlid := "wlid://cluster-test/namespace-" + ns + "/deployment-app"
	records := []cleanupRecord{
		{kind: "sbomsyft", ns: ns, name: "image-" + ns, annotations: map[string]string{helpersv1.ImageIDMetadataKey: "image-" + ns}},
		{kind: "vulnerabilitymanifests", ns: ns, name: "instance-" + ns, annotations: map[string]string{helpersv1.InstanceIDMetadataKey: "instance-" + ns}},
		{kind: "workloadconfigurationscansummaries", ns: ns, name: "deployment-app", annotations: map[string]string{helpersv1.WlidMetadataKey: wlid}},
		{kind: "vulnerabilitymanifestsummaries", ns: ns, name: "deployment-app-app", annotations: map[string]string{helpersv1.WlidMetadataKey: wlid, helpersv1.ContainerNameMetadataKey: "app"}},
		{kind: "seccompprofiles", ns: ns, name: "deployment-app", labels: map[string]string{helpersv1.TemplateHashKey: "hash-" + ns, helpersv1.RelatedKindMetadataKey: "Deployment"}},
	}
	if withStale {
		records = append(records, cleanupRecord{kind: "workloadconfigurationscansummaries", ns: ns, name: "deployment-gone", annotations: map[string]string{helpersv1.WlidMetadataKey: "wlid://cluster-test/namespace-" + ns + "/deployment-gone"}, stale: true})
	}
	return records
}

func (r cleanupRecord) path() string {
	return filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, r.kind, r.ns, r.name+GobExt)
}

func writeRecords(t *testing.T, fs afero.Fs, pool *sqlitemigration.Pool, records []cleanupRecord) {
	t.Helper()
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)
	for _, r := range records {
		require.NoError(t, afero.WriteFile(fs, r.path(), []byte("payload"), 0644))
		data, err := json.Marshal(metav1.ObjectMeta{Name: r.name, Namespace: r.ns, Labels: r.labels, Annotations: r.annotations})
		require.NoError(t, err)
		require.NoError(t, WriteJSON(conn, payloadPathToKey(r.path()), data))
	}
}

func remainingRecords(t *testing.T, fs afero.Fs, pool *sqlitemigration.Pool, records []cleanupRecord) map[string]bool {
	t.Helper()
	conn, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(conn)
	out := map[string]bool{}
	for _, r := range records {
		exists, err := afero.Exists(fs, r.path())
		require.NoError(t, err)
		_, metaErr := ReadMetadata(conn, payloadPathToKey(r.path()))
		require.Equal(t, exists, metaErr == nil, "payload file and metadata row go together for %s", r.path())
		out[r.ns+"/"+r.kind+"/"+r.name] = exists
	}
	return out
}

func newNamespaceCleanup(t *testing.T, fetcher *namespaceFetcher, records []cleanupRecord) (*ResourcesCleanupHandler, afero.Fs, *sqlitemigration.Pool) {
	t.Helper()
	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	writeRecords(t, fs, pool, records)
	return NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, "kubescape", fetcher, true), fs, pool
}

// The default namespace holds the cluster level records, but workloads run there too
// (kubescape's own, when the namespace is scanned). Their records, and the cluster level
// records of what runs only there, must survive the cleanup like any other namespace's.
func TestCleanupTaskKeepsWhatRunsInTheDefaultNamespace(t *testing.T) {
	records := append(namespaceRecords("kubescape", true), namespaceRecords("other", true)...)
	fetcher := &namespaceFetcher{namespaces: []string{"kubescape", "other"}, running: map[string]ResourceMaps{"kubescape": runningIn("kubescape"), "other": runningIn("other")}}
	h, fs, pool := newNamespaceCleanup(t, fetcher, records)

	require.NoError(t, h.CleanupTask(context.Background(), h.resourceToKindHandler))

	assert.Equal(t, []string{"other", "kubescape"}, fetcher.calls, "the default namespace's workloads are fetched too, last")
	for key, exists := range remainingRecords(t, fs, pool, records) {
		for _, r := range records {
			if r.ns+"/"+r.kind+"/"+r.name == key {
				assert.Equal(t, !r.stale, exists, key)
			}
		}
	}
}

// Failing to fetch the default namespace's workloads aborts the tick before any of its
// records is judged, as it does for every other namespace.
func TestCleanupTaskStopsWhenTheDefaultNamespaceCannotBeFetched(t *testing.T) {
	records := append(namespaceRecords("kubescape", true), namespaceRecords("other", true)...)
	fetcher := &namespaceFetcher{namespaces: []string{"kubescape", "other"}, running: map[string]ResourceMaps{"other": runningIn("other")}, failing: "kubescape"}
	h, fs, pool := newNamespaceCleanup(t, fetcher, records)

	err := h.CleanupTask(context.Background(), h.resourceToKindHandler)

	require.ErrorContains(t, err, "failed to fetch resources")
	remaining := remainingRecords(t, fs, pool, records)
	for _, r := range records {
		want := r.ns == "kubescape" || !r.stale
		assert.Equal(t, want, remaining[r.ns+"/"+r.kind+"/"+r.name], r.path())
	}
}

// A delete that fails in the default namespace is reported like one in any other
// namespace, instead of being assigned and dropped.
func TestCleanupTaskReportsAFailureInTheDefaultNamespace(t *testing.T) {
	records := append(namespaceRecords("kubescape", true), namespaceRecords("other", false)...)
	fetcher := &namespaceFetcher{namespaces: []string{"kubescape", "other"}, running: map[string]ResourceMaps{"kubescape": runningIn("kubescape"), "other": runningIn("other")}}
	h, _, pool := newNamespaceCleanup(t, fetcher, records)

	interrupted := make(chan struct{})
	close(interrupted)
	var conn *sqlite.Conn
	prev := writeStmtObserver.Load()
	hook := func(ws writeStatement) {
		if prev != nil {
			(*prev)(ws)
		}
		if ws.pool == pool && ws.op == sqlite.OpDelete && ws.table == "metadata" && conn == nil {
			conn = ws.conn
			conn.SetInterrupt(interrupted)
		}
	}
	writeStmtObserver.Store(&hook)
	t.Cleanup(func() {
		writeStmtObserver.Store(prev)
		if conn != nil {
			conn.SetInterrupt(nil)
		}
	})

	err := h.CleanupTask(context.Background(), h.resourceToKindHandler)

	require.NotNil(t, conn, "the stale summary's row delete was attempted")
	require.ErrorContains(t, err, "failed to delete metadata")
}
