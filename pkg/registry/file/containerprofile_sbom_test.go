package file

import (
	"context"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/k8s-interface/names"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
)

func TestContainerProfileSavePreservesSBOM(t *testing.T) {
	const (
		sbomNamespace     = "kubescape"
		workloadNamespace = "default"
		cluster           = "test-cluster"
		imageTag          = "nginx:1.27"
		imageID           = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		imagePath         = "/usr/lib/libcrypto.so.3"
	)

	for _, backend := range []string{"file", "sqlite"} {
		t.Run(backend, func(t *testing.T) {
			var profiles, sboms storage.Interface
			var processor *ContainerProfileProcessor
			if backend == "sqlite" {
				env := newObjectStoreEnv(t)
				profiles, sboms, processor = env.store, env.legacy, env.processor
			} else {
				store, p, fs, pool := newLifecycleStorage(t)
				sboms = NewStorageImpl(fs, store.root, pool, store.watchDispatcher, store.scheme)
				profiles, processor = store, p
			}
			processor.HostType = armotypes.HostTypeKubernetes
			processor.DefaultNamespace = sbomNamespace
			processor.CollapseSettings = func() dynamicpathdetector.CollapseSettings {
				return dynamicpathdetector.CollapseSettings{OpenDynamicThreshold: 2}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			name, err := names.ImageInfoToSlug(imageTag, imageID)
			require.NoError(t, err)
			sbomPrefix := "/" + softwarecomposition.GroupName + "/sbomsyfts/" + sbomNamespace
			sbomKey := sbomPrefix + "/" + name
			sbom := &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: sbomNamespace}}
			sbom.Spec.Syft.Files = []softwarecomposition.SyftFile{{Location: softwarecomposition.Coordinates{RealPath: imagePath}}}
			require.NoError(t, sboms.Create(ctx, sbomKey, sbom, &softwarecomposition.SBOMSyft{}, 0))

			profile := &softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Name: "nginx", Namespace: workloadNamespace,
					Annotations: map[string]string{helpers.ClusterMetadataKey: cluster},
				},
				Spec: softwarecomposition.ContainerProfileSpec{
					ImageTag: imageTag, ImageID: imageID,
					Opens: []softwarecomposition.OpenCalls{
						{Path: imagePath},
						{Path: "/usr/lib/libssl.so.3"},
						{Path: "/usr/lib/libz.so.1"},
					},
				},
			}
			profileKey := "/" + softwarecomposition.GroupName + "/containerprofiles/" + workloadNamespace + "/" + profile.Name
			require.NoError(t, profiles.Create(ctx, profileKey, profile, &softwarecomposition.ContainerProfile{}, 0))

			listed := &softwarecomposition.SBOMSyftList{}
			require.NoError(t, sboms.GetList(ctx, sbomPrefix, storage.ListOptions{Recursive: true}, listed))
			require.Len(t, listed.Items, 1, "saving a profile must not remove the SBOM metadata")
			assert.Equal(t, name, listed.Items[0].Name)
			storedSBOM := &softwarecomposition.SBOMSyft{}
			require.NoError(t, sboms.Get(ctx, sbomKey, storage.GetOptions{}, storedSBOM))
			assert.Equal(t, sbom.Spec.Syft.Files, storedSBOM.Spec.Syft.Files)

			w, err := sboms.Watch(ctx, sbomPrefix, watchListOptions(false))
			require.NoError(t, err)
			defer w.Stop()
			event := nextEvent(t, w)
			assert.Equal(t, watch.Added, event.Type)
			assert.Equal(t, name, objectName(t, event.Object))

			storedProfile := &softwarecomposition.ContainerProfile{}
			require.NoError(t, profiles.Get(ctx, profileKey, storage.GetOptions{}, storedProfile))
			assert.Contains(t, storedProfile.Spec.Opens, softwarecomposition.OpenCalls{Path: imagePath}, "deflation must use the SBOM to keep its literal path")
		})
	}
}

func TestSBOMReadRejectsClusterKey(t *testing.T) {
	s := newWatchListStorage(t)
	ctx := context.Background()
	prefix := "/" + softwarecomposition.GroupName + "/sbomsyfts/"
	key := prefix + "kubescape/nginx"
	sbom := &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "kubescape"}}
	require.NoError(t, s.Create(ctx, key, sbom, &softwarecomposition.SBOMSyft{}, 0))

	clusterKey := prefix + "test-cluster/kubescape/nginx"
	out := &softwarecomposition.SBOMSyft{}
	err := s.Get(ctx, clusterKey, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, out)
	assert.True(t, storage.IsNotFound(err), "cluster key metadata read returned %v", err)
	err = s.Get(ctx, clusterKey, storage.GetOptions{}, out)
	assert.True(t, storage.IsNotFound(err), "cluster key payload read returned %v", err)

	require.NoError(t, s.Get(ctx, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, out))
	assert.Equal(t, sbom.Name, out.Name)
}
