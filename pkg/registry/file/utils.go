package file

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	helpers2 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/olvrng/ujson"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"zombiezen.com/go/sqlite"
)

// PartialObjectMetadata is a generic representation of any object with ObjectMeta. It allows clients
// to get access to a particular ObjectMeta schema without knowing the details of the version.
type PartialObjectMetadata struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

var _ runtime.Object = (*PartialObjectMetadata)(nil)

func (p PartialObjectMetadata) DeepCopyObject() runtime.Object {
	return &PartialObjectMetadata{
		TypeMeta:   p.TypeMeta,
		ObjectMeta: *p.ObjectMeta.DeepCopy(),
	}
}

func NewKubernetesClient() (*kubernetes.Clientset, error) {
	clusterConfig, err := getConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster config: %w", err)
	}
	// force GRPC
	clusterConfig.AcceptContentTypes = "application/vnd.kubernetes.protobuf"
	clusterConfig.ContentType = "application/vnd.kubernetes.protobuf"

	return kubernetes.NewForConfig(clusterConfig)
}

// deleteMetadata deletes the row (and the time_series rows of a
// containerprofile) behind a reclaimed payload file (W9a of
// write-gate-sharing §3.2): today's autocommit statements on the walk's
// connection with no gate; one gated transaction with the row's JSON decoded
// after release with one.
func (h *ResourcesCleanupHandler) deleteMetadata(ctx context.Context, conn *sqlite.Conn, path string) (runtime.Object, error) {
	key := payloadPathToKey(path)
	metaOut := &PartialObjectMetadata{}
	_, _, kind, _, _, _ := K8sPathToKeys(key)
	if h.gate == nil {
		err := DeleteMetadata(conn, key, metaOut)
		if err != nil {
			return nil, fmt.Errorf("failed to delete metadata: %w", err)
		}
		if IsContainerProfileKind(kind) {
			if err := DeleteTimeSeriesContainerEntries(conn, key); err != nil {
				return nil, fmt.Errorf("failed to delete time series entries: %w", err)
			}
		}
		return metaOut, nil
	}
	var raw []byte
	err := h.write(ctx, conn, holdPathCleanup, resourceFromKey(key), func(_ context.Context, conn *sqlite.Conn) error {
		var derr error
		raw, derr = deleteMetadataRaw(conn, key)
		if derr != nil {
			return derr
		}
		if IsContainerProfileKind(kind) {
			if terr := DeleteTimeSeriesContainerEntries(conn, key); terr != nil {
				return fmt.Errorf("failed to delete time series entries: %w", terr)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to delete metadata: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, metaOut); err != nil {
			return nil, fmt.Errorf("failed to delete metadata: %w", err)
		}
	}
	return metaOut, nil
}

func getConfig() (*rest.Config, error) {
	// try in-cluster config first
	clusterConfig, err := rest.InClusterConfig()
	if err == nil {
		return clusterConfig, nil
	}
	// fallback to kubeconfig
	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	clusterConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err == nil {
		return clusterConfig, nil
	}
	// nothing works
	return nil, errors.New("unable to find config")
}

func loadMetadata(metadataJSON []byte) (*metav1.ObjectMeta, error) {
	data := metav1.ObjectMeta{
		Annotations: map[string]string{},
		Labels:      map[string]string{},
	}

	if len(metadataJSON) == 0 {
		// empty string
		return nil, errors.New("metadata is empty")
	}

	// ujson parsing
	var parent string
	err := ujson.Walk(metadataJSON, func(level int, key, value []byte) bool {
		switch level {
		case 1:
			// read name
			if bytes.EqualFold(key, []byte(`"name"`)) {
				data.Name = unquote(value)
			}
			// read namespace
			if bytes.EqualFold(key, []byte(`"namespace"`)) {
				data.Namespace = unquote(value)
			}
			// read schema version
			if bytes.EqualFold(key, []byte(`"schemaVersion"`)) {
				data.Annotations["schemaVersion"] = unquote(value)
			}
			// record parent for level 2
			parent = unquote(key)
		case 2:
			// read annotations
			if parent == "annotations" {
				data.Annotations[unquote(key)] = unquote(value)
			}
			// read labels
			if parent == "labels" {
				data.Labels[unquote(key)] = unquote(value)
			}
		}
		return true
	})
	if err != nil {
		return nil, errors.New("failed to parse metadata")
	}
	return &data, nil
}

func payloadPathToKey(path string) string {
	return path[len(DefaultStorageRoot) : len(path)-len(GobExt)]
}

func (h *ResourcesCleanupHandler) readMetadata(ctx context.Context, conn *sqlite.Conn, payloadFilePath string) (*metav1.ObjectMeta, error) {
	key := payloadPathToKey(payloadFilePath)
	metadataJSON, err := ReadMetadata(conn, key)
	if err == nil {
		metadata, err := loadMetadata(metadataJSON)
		if err == nil {
			return metadata, nil
		}
	}
	// end of happy path - migration starts here
	// try to find old metadata file
	metadataFilePath := payloadFilePath[:len(payloadFilePath)-len(GobExt)] + MetadataExt
	metadataJSON, err = afero.ReadFile(h.appFs, metadataFilePath)
	if err != nil {
		// no metadata in SQLite nor on disk, delete payload file
		h.deleteFunc(h.appFs, payloadFilePath)
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}
	// write to SQLite (W9b: through the gate when there is one)
	err = h.write(ctx, conn, holdPathCleanupMigrate, resourceFromKey(key), func(_ context.Context, conn *sqlite.Conn) error {
		return WriteJSON(conn, key, metadataJSON)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to migrate metadata to SQLite: %w", err)
	}
	// delete old metadata file
	h.deleteFunc(h.appFs, metadataFilePath)
	// load metadata
	return loadMetadata(metadataJSON)
}

func wlidWithoutClusterName(wlid string) string {
	parts := strings.Split(wlid, "://")
	if len(parts) != 2 {
		return wlid
	}

	// Find the index of the first "/"
	idx := strings.Index(parts[1], "/")
	if idx != -1 {
		// Return the substring from the character after "/"
		return parts[1][idx+1:]
	}
	return parts[1]
}

func unquote(value []byte) string {
	buf, err := ujson.Unquote(value)
	if err != nil {
		return string(value)
	}
	return string(buf)
}

func isHostOrNode(metadata *metav1.ObjectMeta) bool {
	if metadata == nil {
		return false
	}
	if artifactType, ok := metadata.Labels[helpers2.ArtifactTypeMetadataKey]; ok {
		if artifactType == helpers2.HostArtifactType || artifactType == helpers2.NodeArtifactType {
			return true
		}
	}
	return false
}
