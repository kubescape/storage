package cleanup

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wlidPkg "github.com/armosec/utils-k8s-go/wlid"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/olvrng/ujson"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type TypeCleanupHandlerFunc func(kind, path string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool

var resourceKindToHandler = map[string]TypeCleanupHandlerFunc{
	// configurationscansummaries is virtual
	// vulnerabilitysummaries is virtual
	"applicationactivities":               deleteByTemplateHashOrWlid,
	"applicationprofiles":                 deleteByTemplateHashOrWlid,
	"applicationprofilesummaries":         deleteDeprecated,
	"networkneighborses":                  deleteDeprecated,
	"networkneighborhoods":                deleteByTemplateHashOrWlid,
	"openvulnerabilityexchangecontainers": deleteByImageId,
	"sbomspdxv2p3filtereds":               deleteDeprecated,
	"sbomspdxv2p3filtered":                deleteDeprecated,
	"sbomspdxv2p3s":                       deleteDeprecated,
	"sbomspdxv2p3":                        deleteDeprecated,
	"sbomsyftfiltered":                    deleteByInstanceId,
	"sbomsyft":                            deleteByImageId,
	"sbomsummaries":                       deleteDeprecated,
	"seccompprofiles":                     deleteByTemplateHashOrWlid,
	"vulnerabilitymanifests":              deleteByImageIdOrInstanceId,
	"vulnerabilitymanifestsummaries":      deleteByWlidAndContainer,
	"workloadconfigurationscans":          deleteByWlid,
	"workloadconfigurationscansummaries":  deleteByWlid,
}

type TypeDeleteFunc func(appFs afero.Fs, path string)

type ResourcesCleanupHandler struct {
	appFs      afero.Fs
	root       string        // root directory to start the cleanup task
	interval   time.Duration // runs the cleanup task every Interval
	resources  ResourceMaps
	fetcher    ResourcesFetcher
	deleteFunc TypeDeleteFunc
}

func NewResourcesCleanupHandler(appFs afero.Fs, root string, interval time.Duration, fetcher ResourcesFetcher) *ResourcesCleanupHandler {
	return &ResourcesCleanupHandler{
		appFs:      appFs,
		interval:   interval,
		root:       root,
		fetcher:    fetcher,
		deleteFunc: deleteFile,
	}
}

func (h *ResourcesCleanupHandler) StartCleanupTask() {
	for {
		logger.L().Info("started cleanup task", helpers.String("interval", h.interval.String()))
		var err error
		h.resources, err = h.fetcher.FetchResources()
		if err != nil {
			logger.L().Error("cleanup task error. sleeping...", helpers.Error(err))
			time.Sleep(h.interval)
			continue
		}

		var size int64
		for resourceKind, handler := range resourceKindToHandler {
			v1beta1ApiVersionPath := filepath.Join(h.root, softwarecomposition.GroupName, resourceKind)
			exists, _ := afero.DirExists(h.appFs, v1beta1ApiVersionPath)
			if !exists {
				continue
			}
			err := afero.Walk(h.appFs, v1beta1ApiVersionPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}

				// sum all files in path
				if !info.IsDir() {
					size += info.Size()
				}

				// FIXME: migrate to gob files - to remove after some time
				if strings.HasSuffix(path, file.JsonExt) {
					switch resourceKind {
					case "applicationactivities":
						err = migrateToGob[softwarecomposition.ApplicationActivity](h.appFs, path)
					case "applicationprofiles":
						err = migrateToGob[softwarecomposition.ApplicationProfile](h.appFs, path)
					case "networkneighborses":
						err = migrateToGob[softwarecomposition.NetworkNeighbors](h.appFs, path)
					case "networkneighborhoods":
						err = migrateToGob[softwarecomposition.NetworkNeighborhood](h.appFs, path)
					case "openvulnerabilityexchangecontainers":
						err = migrateToGob[softwarecomposition.OpenVulnerabilityExchangeContainer](h.appFs, path)
					case "sbomsyftfiltered":
						err = migrateToGob[softwarecomposition.SBOMSyftFiltered](h.appFs, path)
					case "sbomsyft":
						err = migrateToGob[softwarecomposition.SBOMSyft](h.appFs, path)
					case "vulnerabilitymanifests":
						err = migrateToGob[softwarecomposition.VulnerabilityManifest](h.appFs, path)
					case "vulnerabilitymanifestsummaries":
						err = migrateToGob[softwarecomposition.VulnerabilityManifestSummary](h.appFs, path)
					case "workloadconfigurationscans":
						err = migrateToGob[softwarecomposition.WorkloadConfigurationScan](h.appFs, path)
					case "workloadconfigurationscansummaries":
						err = migrateToGob[softwarecomposition.WorkloadConfigurationScanSummary](h.appFs, path)
					default:
						err = moveToGobBeforeDeletion(h.appFs, path)
					}
					if err != nil {
						logger.L().Error("migration to gob error", helpers.Error(err))
						return nil
					}
				}

				// skip directories and files that are not metadata files
				if info.IsDir() || !file.IsMetadataFile(path) {
					return nil
				}

				metadata, err := loadMetadataFromPath(h.appFs, path)
				if err != nil {
					logger.L().Error("load metadata error", helpers.Error(err))
					return nil
				}
				if metadata == nil {
					// no metadata found
					return nil
				}

				toDelete := handler(resourceKind, path, metadata, h.resources)
				if toDelete {
					logger.L().Debug("deleting", helpers.String("kind", resourceKind), helpers.String("namespace", metadata.Namespace), helpers.String("name", metadata.Name))
					h.deleteFunc(h.appFs, path)

					payloadFilePath := path[:len(path)-len(file.MetadataExt)] + file.GobExt
					h.deleteFunc(h.appFs, payloadFilePath)
				}
				return nil
			})
			if err != nil {
				logger.L().Error("cleanup task error", helpers.Error(err))
			}
		}

		logger.L().Info("storage size before cleanup", helpers.String("path", h.root), helpers.String("size", fmt.Sprintf("%d bytes", size)))

		if h.interval == 0 {
			break
		}

		logger.L().Info("finished cleanup task. sleeping...")
		time.Sleep(h.interval)
	}
}

func deleteFile(appFs afero.Fs, path string) {
	if err := appFs.Remove(path); err != nil {
		logger.L().Error("failed deleting file", helpers.Error(err))
	}
}

func loadMetadataFromPath(appFs afero.Fs, rootPath string) (*metav1.ObjectMeta, error) {
	input, err := afero.ReadFile(appFs, rootPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", rootPath, err)
	}

	data := metav1.ObjectMeta{
		Annotations: map[string]string{},
		Labels:      map[string]string{},
	}

	if len(input) == 0 {
		// empty file
		return nil, nil
	}

	// ujson parsing
	var parent string
	err = ujson.Walk(input, func(level int, key, value []byte) bool {
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
		return nil, fmt.Errorf("failed to parse file %s: %w", rootPath, err)
	}
	return &data, nil
}

func unquote(value []byte) string {
	buf, err := ujson.Unquote(value)
	if err != nil {
		return string(value)
	}
	return string(buf)
}

// delete deprecated resources
func deleteDeprecated(_, _ string, _ *metav1.ObjectMeta, _ ResourceMaps) bool {
	return true
}

func deleteByInstanceId(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	instanceId, ok := metadata.Annotations[helpersv1.InstanceIDMetadataKey]
	return !ok || !resourceMaps.RunningInstanceIds.Contains(instanceId)
}

func deleteByImageId(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	imageId, ok := metadata.Annotations[helpersv1.ImageIDMetadataKey]
	return !ok || !resourceMaps.RunningContainerImageIds.Contains(imageId)
}

func deleteByWlid(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	wlid, ok := metadata.Annotations[helpersv1.WlidMetadataKey]
	kind := strings.ToLower(wlidPkg.GetKindFromWlid(wlid))
	if !Workloads.Contains(kind) {
		if kind != "" {
			logger.L().Debug("skipping unknown kind", helpers.String("kind", kind))
		}
		return false
	}
	return !ok || !resourceMaps.RunningWlidsToContainerNames.Has(wlidWithoutClusterName(wlid))
}

func deleteByImageIdOrInstanceId(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	imageId, imageIdFound := metadata.Annotations[helpersv1.ImageIDMetadataKey]
	instanceId, instanceIdFound := metadata.Annotations[helpersv1.InstanceIDMetadataKey]
	return (!instanceIdFound && !imageIdFound) ||
		(imageIdFound && !resourceMaps.RunningContainerImageIds.Contains(imageId)) ||
		(instanceIdFound && !resourceMaps.RunningInstanceIds.Contains(instanceId))
}

func deleteByWlidAndContainer(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	wlContainerName, wlContainerNameFound := metadata.Annotations[helpersv1.ContainerNameMetadataKey]
	wlid, wlidFound := metadata.Annotations[helpersv1.WlidMetadataKey]
	if !wlidFound || !wlContainerNameFound {
		return true
	}
	containerNames, wlidExists := resourceMaps.RunningWlidsToContainerNames.Load(wlidWithoutClusterName(wlid))
	return !wlidExists || !containerNames.Contains(wlContainerName)
}

func deleteByTemplateHashOrWlid(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	wlReplica, wlReplicaFound := metadata.Labels[helpersv1.TemplateHashKey] // replica
	if wlReplicaFound && wlReplica != "" {
		return !resourceMaps.RunningTemplateHash.Contains(wlReplica)
	}
	// fallback to wlid
	return deleteByWlid("", "", metadata, resourceMaps)
}
