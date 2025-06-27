package cleanup

import (
	"context"
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
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const (
	MinSizeToReport = 30 * 1024 * 1024 // 30MB
)

type TypeCleanupHandlerFunc func(kind, path string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool

type TypeDeleteFunc func(appFs afero.Fs, path string)

type ResourcesCleanupHandler struct {
	appFs                 afero.Fs
	root                  string // root directory to start the cleanup task
	pool                  *sqlitemigration.Pool
	interval              time.Duration // runs the cleanup task every Interval
	resources             ResourceMaps
	fetcher               ResourcesFetcher
	deleteFunc            TypeDeleteFunc
	resourceToKindHandler map[string][]TypeCleanupHandlerFunc
	watchDispatcher       *file.WatchDispatcher
}

func initResourceToKindHandler(relevancyEnabled bool) map[string][]TypeCleanupHandlerFunc {
	resourceKindToHandler := map[string][]TypeCleanupHandlerFunc{
		// configurationscansummaries is virtual
		// vulnerabilitysummaries is virtual
		"applicationactivities":               {deleteDeprecated},
		"applicationprofiles":                 {deleteByTemplateHashOrWlid},
		"applicationprofilesummaries":         {deleteDeprecated},
		"containerprofiles":                   {deleteByTemplateHashOrWlid},
		"networkneighborses":                  {deleteDeprecated},
		"networkneighborhoods":                {deleteByTemplateHashOrWlid},
		"openvulnerabilityexchangecontainers": {deleteByImageId},
		"sbomspdxv2p3filtereds":               {deleteDeprecated},
		"sbomspdxv2p3filtered":                {deleteDeprecated},
		"sbomspdxv2p3s":                       {deleteDeprecated},
		"sbomspdxv2p3":                        {deleteDeprecated},
		"sbomsyftfiltered":                    {deleteByInstanceId},
		"sbomsyft":                            {deleteByImageId},
		"sbomsummaries":                       {deleteDeprecated},
		"seccompprofiles":                     {deleteByTemplateHashOrWlid},
		"vulnerabilitymanifests":              {deleteByImageIdOrInstanceId},
		"vulnerabilitymanifestsummaries":      {deleteByWlidAndContainer},
		"workloadconfigurationscans":          {deleteByWlid},
		"workloadconfigurationscansummaries":  {deleteByWlid},
	}

	// only if relevancy is enabled, we need to delete application profiles with missing instanceId or wlid annotations
	if relevancyEnabled {
		logger.L().Debug("relevancy is enabled, adding additional cleanup handlers")
		resourceKindToHandler["applicationprofiles"] = append(resourceKindToHandler["applicationprofiles"], deleteMissingInstanceIdAnnotation, deleteMissingWlidAnnotation)
	}
	return resourceKindToHandler
}

func NewResourcesCleanupHandler(appFs afero.Fs, root string, pool *sqlitemigration.Pool, watchDispatcher *file.WatchDispatcher, interval time.Duration, fetcher ResourcesFetcher, relevancyEnabled bool) *ResourcesCleanupHandler {

	return &ResourcesCleanupHandler{
		appFs:                 appFs,
		interval:              interval,
		root:                  root,
		pool:                  pool,
		fetcher:               fetcher,
		deleteFunc:            deleteFile,
		resourceToKindHandler: initResourceToKindHandler(relevancyEnabled),
		watchDispatcher:       watchDispatcher,
	}
}

func (h *ResourcesCleanupHandler) StartCleanupTask(ctx context.Context) {
	for {
		logger.L().Info("started cleanup task", helpers.String("interval", h.interval.String()))
		var err error
		h.resources, err = h.fetcher.FetchResources()
		if err != nil {
			logger.L().Error("cleanup task error. sleeping...", helpers.Error(err))
			time.Sleep(h.interval)
			continue
		}

		conn, err := h.pool.Take(context.Background())
		if err != nil {
			logger.L().Error("failed to take connection", helpers.Error(err))
			time.Sleep(h.interval)
			continue
		}
		for resourceKind, handlers := range h.resourceToKindHandler {
			v1beta1ApiVersionPath := filepath.Join(h.root, softwarecomposition.GroupName, resourceKind)
			exists, _ := afero.DirExists(h.appFs, v1beta1ApiVersionPath)
			if !exists {
				continue
			}
			err := afero.Walk(h.appFs, v1beta1ApiVersionPath, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // we might encounter already deleted files from readMetadata when migrating to SQLite
				}

				// skip directories
				if info.IsDir() {
					return nil
				}

				if size := info.Size(); size > MinSizeToReport {
					logger.L().Ctx(ctx).Warning("large file detected, you may want to truncate it", helpers.String("path", path), helpers.String("size", fmt.Sprintf("%d bytes", size)))
				}

				// skip files that are not payload files
				if !file.IsPayloadFile(path) {
					return nil
				}

				metadata, err := h.readMetadata(conn, path)
				if err != nil {
					logger.L().Error("load metadata error", helpers.Error(err))
					return nil
				}

				// either run single handler, or perform OR operation on multiple handlers
				var toDelete bool
				if len(handlers) == 1 {
					toDelete = handlers[0](resourceKind, path, metadata, h.resources)
				} else {
					toDelete = or(handlers, resourceKind, path, metadata, h.resources)
				}

				if toDelete {
					logger.L().Debug("deleting", helpers.String("kind", resourceKind), helpers.String("namespace", metadata.Namespace), helpers.String("name", metadata.Name))
					h.deleteFunc(h.appFs, path)

					metaOut := h.deleteMetadata(conn, path)
					if h.watchDispatcher != nil {
						key := path[len(h.root) : len(path)-len(file.GobExt)]
						h.watchDispatcher.Deleted(key, metaOut)
					}
				}
				return nil
			})
			if err != nil {
				logger.L().Error("cleanup task error", helpers.Error(err))
			}
		}
		h.pool.Put(conn)

		if h.interval == 0 {
			break
		}

		logger.L().Info("finished cleanup task. sleeping...")
		time.Sleep(h.interval)
	}
}

func or(funcs []TypeCleanupHandlerFunc, kind, path string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	for _, f := range funcs {
		if f(kind, path, metadata, resourceMaps) {
			return true
		}
	}
	return false
}

func deleteFile(appFs afero.Fs, path string) {
	if err := appFs.Remove(path); err != nil {
		logger.L().Error("failed deleting file", helpers.Error(err))
	}
}

// delete deprecated resources
func deleteDeprecated(_, _ string, _ *metav1.ObjectMeta, _ ResourceMaps) bool {
	return true
}

func deleteByInstanceId(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	// skip host and node types
	if isHostOrNode(metadata) {
		return false
	}
	instanceId, ok := metadata.Annotations[helpersv1.InstanceIDMetadataKey]
	return !ok || !resourceMaps.RunningInstanceIds.Contains(instanceId)
}

func deleteByImageId(_, _ string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool {
	// skip host and node types
	if isHostOrNode(metadata) {
		return false
	}
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
	// skip host and node types
	if isHostOrNode(metadata) {
		return false
	}
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

// deleteMissingInstanceIdAnnotation deletes resources that have missing instanceId annotation
func deleteMissingInstanceIdAnnotation(_, _ string, metadata *metav1.ObjectMeta, _ ResourceMaps) bool {
	_, ok := metadata.Annotations[helpersv1.InstanceIDMetadataKey]
	return !ok
}

// deleteMissingInstanceIdAnnotation deletes resources that have missing wlid annotation
func deleteMissingWlidAnnotation(_, _ string, metadata *metav1.ObjectMeta, _ ResourceMaps) bool {
	_, ok := metadata.Annotations[helpersv1.WlidMetadataKey]
	return !ok
}
