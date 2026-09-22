package file

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"zombiezen.com/go/sqlite"
)

type hostCleanupFetcher struct {
	staleWorkloadFetchMock
	nodes []corev1.Node
	err   error
	calls int
}

func (f *hostCleanupFetcher) FetchNodes(context.Context) ([]corev1.Node, error) {
	f.calls++
	return f.nodes, f.err
}

func (f *hostCleanupFetcher) ListNamespaces(*sqlite.Conn) ([]string, error) {
	return []string{"default", "kubescape"}, nil
}

func hostCleanupMetadata(kind, namespace, name string) metav1.ObjectMeta {
	metadata := metav1.ObjectMeta{Name: name, Namespace: namespace}
	if kind == "sbomsyft" {
		sum := sha256.Sum256([]byte("worker-1"))
		identifier := fmt.Sprintf("worker-1-%x", sum[:16])
		metadata.Labels = map[string]string{"kubescape.io/host": identifier, "kubescape.io/node-name": identifier}
	} else {
		metadata.Labels = map[string]string{
			"kubescape.io/workload-kind":          "Node",
			"kubescape.io/workload-namespace":     "host",
			"kubescape.io/instance-template-hash": "host",
		}
		metadata.Annotations = map[string]string{"kubescape.io/wlid": "wlid://cluster-test/namespace-host/host-worker-1"}
	}
	return metadata
}

func hostCleanupScenarios() []struct {
	name  string
	nodes []corev1.Node
	err   error
	keep  bool
} {
	return []struct {
		name  string
		nodes []corev1.Node
		err   error
		keep  bool
	}{
		{name: "existing NotReady node", nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker-1"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{MachineID: "machine-1"}, Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}}}}, keep: true},
		{name: "missing machine ID", nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker-2"}}}, keep: true},
		{name: "deleted node"},
		{name: "different existing node", nodes: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "worker-2"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{MachineID: "machine-2"}}}}},
		{name: "failed discovery", err: errors.New("nodes list forbidden"), keep: true},
	}
}

func TestCleanupHostFileResources(t *testing.T) {
	for _, tc := range hostCleanupScenarios() {
		t.Run(tc.name, func(t *testing.T) {
			fs := afero.NewMemMapFs()
			pool := NewTestPool(t.TempDir())
			t.Cleanup(func() { require.NoError(t, pool.Close()) })
			conn, err := pool.Take(t.Context())
			require.NoError(t, err)
			paths := map[string]bool{}
			for _, ns := range []string{"default", "kubescape"} {
				for _, kind := range []string{"sbomsyft", ContainerProfileKind} {
					for _, host := range []bool{true, false} {
						name := "ordinary-stale"
						if host {
							name = "host-worker"
						}
						metadata := hostCleanupMetadata(kind, ns, name)
						if !host {
							metadata.Labels = nil
							metadata.Annotations = nil
						}
						path := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, ns, name+GobExt)
						require.NoError(t, afero.WriteFile(fs, path, []byte("payload"), 0644))
						data, err := json.Marshal(metadata)
						require.NoError(t, err)
						require.NoError(t, WriteJSON(conn, payloadPathToKey(path), data))
						paths[path] = host && tc.keep
					}
				}
			}
			pool.Put(conn)
			fetcher := &hostCleanupFetcher{nodes: tc.nodes, err: tc.err}
			h := NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, "kubescape", fetcher, true)
			// Exercise the two production cleanup arms separately: generic
			// resources, then the ContainerProfile processor's handler list.
			require.NoError(t, h.CleanupTask(t.Context(), h.resourceToKindHandler))
			require.NoError(t, h.CleanupTask(t.Context(), map[string][]TypeCleanupHandlerFunc{ContainerProfileKind: h.ContainerProfileHandlers()}))
			require.Equal(t, 2, fetcher.calls, "discover nodes once per cleanup cycle")
			conn, err = pool.Take(t.Context())
			require.NoError(t, err)
			defer pool.Put(conn)
			for path, keep := range paths {
				exists, err := afero.Exists(fs, path)
				require.NoError(t, err)
				assert.Equal(t, keep, exists, path)
				_, err = ReadMetadata(conn, payloadPathToKey(path))
				if keep {
					assert.NoError(t, err, path)
				} else {
					assert.Error(t, err, path)
				}
			}
		})
	}
}

func TestCleanupHostSQLiteContainerProfiles(t *testing.T) {
	for _, tc := range hostCleanupScenarios() {
		for _, ns := range []string{"default", "kubescape"} {
			t.Run(tc.name+"/"+ns, func(t *testing.T) {
				e := newObjectStoreEnv(t)
				e.ns = ns
				host := e.plain("host-worker-host-host")
				host.ObjectMeta = hostCleanupMetadata(ContainerProfileKind, ns, host.Name)
				stale := e.plain("pod-stale-main-1234-5678")
				stale.Namespace = ns
				stale.Labels = nil
				stale.Annotations = nil
				e.create(host)
				e.create(stale)
				fetcher := &hostCleanupFetcher{nodes: tc.nodes, err: tc.err}
				h := NewResourcesCleanupHandler(e.legacyFs, DefaultStorageRoot, e.pool, e.wd, 0, "kubescape", fetcher, true)
				h.SetWriteGate(e.gate)
				h.SetContainerProfileStore(e.store)
				require.NoError(t, h.CleanupTask(e.ctx, map[string][]TypeCleanupHandlerFunc{ContainerProfileKind: h.ContainerProfileHandlers()}))
				require.Equal(t, 1, fetcher.calls)
				row := e.inspect(e.key(host.Name))
				require.Equal(t, tc.keep, row.metaExists)
				require.Equal(t, tc.keep, row.payloadExists)
				staleRow := e.inspect(e.key(stale.Name))
				require.False(t, staleRow.metaExists)
				require.False(t, staleRow.payloadExists)
			})
		}
	}
}
