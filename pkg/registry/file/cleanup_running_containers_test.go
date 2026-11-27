package file

// Cluster level records in the default namespace (SBOMs keyed by image ID,
// vulnerability manifests keyed by instance ID) describe containers that can
// run in any namespace. These tests store records only in the default
// namespace, as SBOM generation alone does, and run the containers in a
// namespace where storage holds no records (kubescape/storage#490).

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"zombiezen.com/go/sqlite"
)

const (
	clusterRecordsNS = "kubescape"
	runningImageID   = "sha256:running"
	stoppedImageID   = "sha256:stopped"
)

// writeClusterRecord stores one record of kind in the default namespace, as a
// payload file and its metadata row, and returns the payload path.
func writeClusterRecord(t *testing.T, fs afero.Fs, conn *sqlite.Conn, kind, name string, annotations map[string]string) string {
	t.Helper()
	path := filepath.Join(DefaultStorageRoot, softwarecomposition.GroupName, kind, clusterRecordsNS, name+GobExt)
	require.NoError(t, afero.WriteFile(fs, path, []byte("payload"), 0644))
	metadata, err := json.Marshal(map[string]any{"name": name, "namespace": clusterRecordsNS, "annotations": annotations})
	require.NoError(t, err)
	require.NoError(t, WriteJSON(conn, payloadPathToKey(path), metadata))
	return path
}

// assertPayload checks whether the cleanup kept the payload at path.
func assertPayload(t *testing.T, fs afero.Fs, path string, kept bool) {
	t.Helper()
	exists, err := afero.Exists(fs, path)
	require.NoError(t, err)
	assert.Equal(t, kept, exists, path)
}

// The records of the running container's image and instance are kept, and the
// stopped ones are deleted, although storage lists no namespace but the
// default one. Before the fix all four were deleted.
func TestCleanupTask_KeepsClusterRecordsOfContainersInNamespacesWithoutRecords(t *testing.T) {
	pod := runningPod("apps", "web", runningImageID)
	ids, err := instanceidhandler.GenerateInstanceIDFromRuntimeObj(pod, nil)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(t.Context())
	require.NoError(t, err)
	runningSBOM := writeClusterRecord(t, fs, conn, "sbomsyfts", "running", map[string]string{helpersv1.ImageIDMetadataKey: runningImageID})
	stoppedSBOM := writeClusterRecord(t, fs, conn, "sbomsyfts", "stopped", map[string]string{helpersv1.ImageIDMetadataKey: stoppedImageID})
	runningManifest := writeClusterRecord(t, fs, conn, "vulnerabilitymanifests", "running", map[string]string{helpersv1.InstanceIDMetadataKey: ids[0].GetStringFormatted()})
	stoppedManifest := writeClusterRecord(t, fs, conn, "vulnerabilitymanifests", "stopped", map[string]string{helpersv1.InstanceIDMetadataKey: "apiVersion-v1/namespace-apps/kind-Pod/name-gone/containerName-main"})
	namespaces, err := listNamespaces(conn)
	require.NoError(t, err)
	require.Equal(t, []string{clusterRecordsNS}, namespaces, "the pod's namespace holds no records")
	pool.Put(conn)

	fetcher := NewKubernetesAPI(config.Config{}, fake.NewSimpleClientset(pod))
	h := NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, clusterRecordsNS, fetcher, false)
	require.NoError(t, h.CleanupTask(t.Context(), h.resourceToKindHandler))

	assertPayload(t, fs, runningSBOM, true)
	assertPayload(t, fs, stoppedSBOM, false)
	assertPayload(t, fs, runningManifest, true)
	assertPayload(t, fs, stoppedManifest, false)
}

// A failed list of the pods of all namespaces ends the tick before the default
// namespace is cleaned: no running set means nothing there is judged stopped.
func TestCleanupTask_FailedClusterPodListDeletesNoClusterRecord(t *testing.T) {
	fs := afero.NewMemMapFs()
	pool := NewTestPool(t.TempDir())
	conn, err := pool.Take(t.Context())
	require.NoError(t, err)
	runningSBOM := writeClusterRecord(t, fs, conn, "sbomsyfts", "running", map[string]string{helpersv1.ImageIDMetadataKey: runningImageID})
	stoppedSBOM := writeClusterRecord(t, fs, conn, "sbomsyfts", "stopped", map[string]string{helpersv1.ImageIDMetadataKey: stoppedImageID})
	pool.Put(conn)

	client := fake.NewSimpleClientset(runningPod("apps", "web", runningImageID))
	listErr := errors.New("pod listing unavailable")
	client.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
		if action.GetNamespace() == metav1.NamespaceAll {
			return true, nil, listErr
		}
		return false, nil, nil
	})
	h := NewResourcesCleanupHandler(fs, DefaultStorageRoot, pool, nil, 0, clusterRecordsNS, NewKubernetesAPI(config.Config{}, client), false)
	require.ErrorIs(t, h.CleanupTask(t.Context(), h.resourceToKindHandler), listErr)

	assertPayload(t, fs, runningSBOM, true)
	assertPayload(t, fs, stoppedSBOM, true)
}
