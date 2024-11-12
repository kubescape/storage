package cleanup

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/olvrng/ujson"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func NewKubernetesClient() (dynamic.Interface, discovery.DiscoveryInterface, error) {
	clusterConfig, err := getConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get cluster config: %w", err)
	}

	dynClient, err := dynamic.NewForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}
	disco, err := discovery.NewDiscoveryClientForConfig(clusterConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create discovery client: %w", err)
	}
	return dynClient, disco, nil
}

func (h *ResourcesCleanupHandler) deleteMetadata(path string) {
	conn, err := h.pool.Take(context.Background())
	if err != nil {
		logger.L().Error("failed to take connection", helpers.Error(err))
		return
	}
	defer h.pool.Put(conn)
	key := payloadPathToKey(path)
	err = file.DeleteMetadata(conn, key, nil)
	if err != nil {
		logger.L().Error("failed to delete metadata", helpers.Error(err))
	}
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
		return nil, nil
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
			// record parent for level 3
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

func migrateToGob[T any](appFs afero.Fs, path string) error {
	// open json file
	jsonFile, err := appFs.Open(path)
	if err != nil {
		return err
	}
	// decode json
	decoder := json.NewDecoder(jsonFile)
	var objPtr T
	err = decoder.Decode(&objPtr)
	if err != nil {
		return err
	}
	// encode to gob
	var b bytes.Buffer
	encoder := gob.NewEncoder(&b)
	err = encoder.Encode(objPtr)
	if err != nil {
		return err
	}
	// write to gob file
	err = afero.WriteFile(appFs, path[:len(path)-len(file.JsonExt)]+file.GobExt, b.Bytes(), 0644)
	if err != nil {
		return err
	}
	// remove json file
	err = appFs.Remove(path)
	if err != nil {
		return err
	}
	return nil
}

func moveToGobBeforeDeletion(appFs afero.Fs, path string) error {
	return appFs.Rename(path, path[:len(path)-len(file.JsonExt)]+file.GobExt)
}

func payloadPathToKey(path string) string {
	return path[len(file.DefaultStorageRoot) : len(path)-len(file.GobExt)]
}

func (h *ResourcesCleanupHandler) readMetadata(payloadFilePath string) (*metav1.ObjectMeta, error) {
	conn, err := h.pool.Take(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to take connection: %w", err)
	}
	defer h.pool.Put(conn)
	key := payloadPathToKey(payloadFilePath)
	metadataJSON, err := file.ReadMetadata(conn, key)
	if err == nil {
		metadata, err := loadMetadata(metadataJSON)
		if err == nil {
			return metadata, nil
		}
	}
	// end of happy path - migration starts here
	// try to find old metadata file
	metadataFilePath := payloadFilePath[:len(payloadFilePath)-len(file.GobExt)] + file.MetadataExt
	metadataJSON, err = afero.ReadFile(h.appFs, metadataFilePath)
	if err != nil {
		// no metadata in SQLite nor on disk, delete payload file
		h.deleteFunc(h.appFs, payloadFilePath)
		return nil, fmt.Errorf("failed to read metadata file: %w", err)
	}
	// write to SQLite
	err = file.WriteJSON(conn, key, metadataJSON)
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
