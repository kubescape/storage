// Build this probe against the pinned pre-fix source tree, never the candidate.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

func main() {
	if len(os.Args) != 4 {
		panic("usage: old-reader ROOT create|get NAME")
	}
	root, action, name := os.Args[1], os.Args[2], os.Args[3]
	pool := file.NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	scheme := runtime.NewScheme()
	install.Install(scheme)
	s := file.NewStorageImpl(afero.NewOsFs(), root, pool, nil, scheme)
	key := "/" + softwarecomposition.GroupName + "/containerprofile/ns/" + name
	obj := &softwarecomposition.ContainerProfile{}
	var err error
	if action == "create" {
		obj.ObjectMeta = metav1.ObjectMeta{Name: name, Namespace: "ns", UID: "old-uid"}
		obj.Spec.ImageTag = "old:1"
		err = s.Create(context.Background(), key, obj, nil, 0)
	} else if action == "get" {
		err = s.Get(context.Background(), key, storage.GetOptions{}, obj)
	} else {
		panic("unknown action")
	}
	if storage.IsNotFound(err) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	if err != nil {
		panic(err)
	}
	if err = json.NewEncoder(os.Stdout).Encode(obj); err != nil {
		panic(err)
	}
}
