package cleanup

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
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
