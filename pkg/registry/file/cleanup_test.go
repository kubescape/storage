package file

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "embed"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
)

//go:embed testdata/expectedFilesToDelete.json
var expectedFilesToDeleteBytes []byte

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

	var filesDeleted []string
	deleteFunc := func(appFs afero.Fs, path string) {
		if err := appFs.Remove(path); err == nil {
			filesDeleted = append(filesDeleted, path)
		}
	}

	// copy sqlite file to the temp directory
	tempDir := t.TempDir()
	bytes, err := os.ReadFile("./testdata/test.sq3")
	require.NoError(t, err)
	err = os.WriteFile(tempDir+"/test.sq3", bytes, 0644)
	require.NoError(t, err)

	handler := &ResourcesCleanupHandler{
		appFs:                 memFs,
		pool:                  NewTestPool(tempDir),
		root:                  DefaultStorageRoot,
		fetcher:               &ResourcesFetchMock{},
		deleteFunc:            deleteFunc,
		resourceToKindHandler: initResourceToKindHandler(false),
	}
	handler.CleanupTask(context.TODO(), handler.resourceToKindHandler)

	containerProfileProcessor := ContainerProfileProcessor{
		cleanupHandler: handler,
	}
	err = containerProfileProcessor.cleanup()
	require.NoError(t, err)

	var expectedFilesToDelete []string
	require.NoError(t, json.Unmarshal(expectedFilesToDeleteBytes, &expectedFilesToDelete))

	slices.Sort(filesDeleted)

	assert.Equal(t, expectedFilesToDelete, filesDeleted)
}

type ResourcesFetchMock struct {
}

var _ ResourcesFetcher = (*ResourcesFetchMock)(nil)

func (r *ResourcesFetchMock) ListNamespaces(_ *sqlite.Conn) ([]string, error) {
	return []string{
		"default", "gadget", "gmp-system", "kubescape", "kube-node-lease",
		"kube-public", "kube-system", "local-path-storage", "systest-ns-foso",
	}, nil
}

func (r *ResourcesFetchMock) FetchResources(_ string) (ResourceMaps, error) {
	// TODO make use of the ns parameter instead of returning the full list all the time
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
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
		resourceMaps.RunningWlidsToContainerNames.Set(wlid, mapset.NewSet(containerNames...))
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
		err := unzipFile(f, DefaultStorageRoot, appFs)
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
