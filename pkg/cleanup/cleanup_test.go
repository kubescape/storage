package cleanup

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"testing"

	_ "embed"

	sets "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
)

//go:embed testdata/imageids.json
var imageIds []byte

//go:embed testdata/instanceids.json
var instanceIds []byte

//go:embed testdata/wlids.json
var wlids []byte

func TestCleanupTask(t *testing.T) {
	memFs := afero.NewMemMapFs()
	// extract test data
	err := unzipSource("./testdata/data.zip", memFs)
	if err != nil {
		t.Fatal(err)
	}

	handler := NewResourcesCleanupHandler(memFs, file.DefaultStorageRoot, time.Hour*0, &ResourcesFetchMock{})
	handler.StartCleanupTask()

	expectedFilesToDelete := []string{
		"/data/spdx.softwarecomposition.kubescape.io/applicationactivities/gadget/gadget-daemonset-gadget-0d7c-fd3c.j",
		"/data/spdx.softwarecomposition.kubescape.io/applicationactivities/gadget/gadget-daemonset-gadget-0d7c-fd3c.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofiles/gadget/gadget-daemonset-gadget-0d7c-fd3c.j",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofiles/gadget/gadget-daemonset-gadget-0d7c-fd3c.m",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/gadget/gadget-daemonset-gadget-0d7c-fd3c.j",
		"/data/spdx.softwarecomposition.kubescape.io/applicationprofilesummaries/gadget/gadget-daemonset-gadget-0d7c-fd3c.m",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/default/deployment-redis.j",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/default/deployment-redis.m",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/gadget/daemonset-gadget.j",
		"/data/spdx.softwarecomposition.kubescape.io/networkneighborses/gadget/daemonset-gadget.m",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-storage-debug-76f234.j",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-storage-debug-76f234.m",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-synchronizer-latest-63825b.j",
		"/data/spdx.softwarecomposition.kubescape.io/openvulnerabilityexchangecontainers/kubescape/quay.io-matthiasb-1-synchronizer-latest-63825b.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.j",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.j",
		"/data/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.m",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.j",
		"/data/spdx.softwarecomposition.kubescape.io/sbomsummaries/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.m",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.j",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/kubescape-replicaset-operator-5b99d66db7-3195-f368.m",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.j",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/kubescape/quay.io-amirm-armo-storage-v0.0.1-98086e.m",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/gmp-system/statefulset-alertmanager-config-reloader.j",
		"/data/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/gmp-system/statefulset-alertmanager-config-reloader.m",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscans/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.j",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscans/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.m",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.j",
		"/data/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape/apps-v1-daemonset-kubescape-host-scanner-c93b-a749.m",
	}

	filesDeleted := handler.GetFilesToDelete()
	slices.Sort(filesDeleted)

	assert.Equal(t, expectedFilesToDelete, filesDeleted)
}

type ResourcesFetchMock struct {
}

var _ ResourcesFetcher = (*ResourcesFetchMock)(nil)

func (r *ResourcesFetchMock) FetchResources() (ResourceMaps, error) {
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           sets.NewSet[string](),
		RunningContainerImageIds:     sets.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, sets.Set[string]]),
	}

	var expectedImageIds []string
	if err := json.Unmarshal(imageIds, &expectedImageIds); err != nil {
		panic(err)
	}
	resourceMaps.RunningContainerImageIds.Append(expectedImageIds...)

	var expectedInstanceIds []string
	if err := json.Unmarshal(instanceIds, &expectedInstanceIds); err != nil {
		panic(err)
	}
	resourceMaps.RunningInstanceIds.Append(expectedInstanceIds...)

	var expectedWlids map[string][]string
	if err := json.Unmarshal(wlids, &expectedWlids); err != nil {
		panic(err)
	}
	for wlid, containerNames := range expectedWlids {
		resourceMaps.RunningWlidsToContainerNames.Set(wlid, sets.NewSet(containerNames...))
	}

	return resourceMaps, nil
}

func unzipSource(source string, appFs afero.Fs) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, f := range reader.File {
		err := unzipFile(f, file.DefaultStorageRoot, appFs)
		if err != nil {
			return err
		}
	}

	return nil
}

func unzipFile(f *zip.File, destination string, appFs afero.Fs) error {
	filePath := filepath.Join(destination, f.Name)
	if !strings.HasPrefix(filePath, filepath.Clean(destination)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path: %s", filePath)
	}

	if f.FileInfo().IsDir() {
		if err := appFs.MkdirAll(filePath, os.ModePerm); err != nil {
			return err
		}
		return nil
	}

	if err := appFs.MkdirAll(filepath.Dir(filePath), os.ModePerm); err != nil {
		return err
	}

	destinationFile, err := appFs.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	zippedFile, err := f.Open()
	if err != nil {
		return err
	}
	defer zippedFile.Close()

	if _, err := io.Copy(destinationFile, zippedFile); err != nil {
		return err
	}
	return nil
}
