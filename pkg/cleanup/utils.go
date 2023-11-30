package cleanup

import (
	"errors"
	"fmt"
	"k8s.io/client-go/discovery"
	"path/filepath"
	"strings"

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
