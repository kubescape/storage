package file

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	wlidPkg "github.com/armosec/utils-k8s-go/wlid"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

const (
	MinSizeToReport         = 30 * 1024 * 1024 // 30MB
	cleanupQuarantinePrefix = ".kubescape-orphan-"
	cleanupQuarantineSuffix = ".pending"
	// cleanupDeletingSuffix marks a payload the cleanup reserved under the write gate
	// for removal: <payload>.g.<nonce>.deleting, not a payload for LIST or the walks.
	cleanupDeletingSuffix = ".deleting"
)

type TypeCleanupHandlerFunc func(kind, path string, metadata *metav1.ObjectMeta, resourceMaps ResourceMaps) bool

type TypeDeleteFunc func(appFs afero.Fs, path string)

type ResourcesCleanupHandler struct {
	appFs                 afero.Fs
	root                  string // root directory to start the cleanup task
	pool                  *sqlitemigration.Pool
	interval              time.Duration // runs the cleanup task every Interval
	defaultNamespace      string
	fetcher               ResourcesFetcher
	deleteFunc            TypeDeleteFunc
	resourceToKindHandler map[string][]TypeCleanupHandlerFunc
	watchDispatcher       *WatchDispatcher
	// relevancyEnabled adds the missing-annotation handlers to the
	// ContainerProfile arm (ContainerProfileHandlers).
	relevancyEnabled bool
	// gate is the process's shared write gate (write-gate-sharing §3.2, W9):
	// when set, the tick's row deletes and sidecar migrations run on the
	// gate's connection; nil = today's code on the walk's connection.
	gate *writeGate
	// cpStore, when set, replaces the file walk for the ContainerProfile
	// kind: rows are enumerated from the metadata table and reclaimed through
	// the ObjectStore's Delete (one gated transaction over metadata, payloads
	// and time_series; Deleted dispatched after). Under the flag CP objects
	// have no payload file, and a legacy file-then-row delete would leave the
	// payloads row behind.
	cpStore *ObjectStore
	// beforeGatedDelete runs after the walk decided to delete an object and before
	// the gated recheck; beforeReservedRemoval runs after the gate released and before
	// the reserved payload is removed. Test seams, nil in production.
	beforeGatedDelete     func(key string)
	beforeReservedRemoval func(key string)
}

// deleteUnlessChanged removes a payload and its rows the way the orphan path does:
// through the write gate when there is one, after reading the row again and leaving the
// object alone when the row is no longer the one the decision was made on, so a writer
// that updated or re-created it between the walk's read and this point keeps it. The
// rows go inside the gate, where the payload is also renamed to a reserved name, and the
// reserved file is removed after the gate: a create that lands on the payload's path
// once the gate is released is never the file that gets removed. A gate that fails after
// the rename puts the payload back; a crash in between leaves a reserved file the next
// walk settles. Without a gate the statements run on the walk's connection, as every
// legacy write does. deleted says whether anything went.
func (h *ResourcesCleanupHandler) deleteUnlessChanged(ctx context.Context, conn *sqlite.Conn, kind, path string, decidedJSON []byte, newObjectFunc func() runtime.Object) (runtime.Object, bool, error) {
	key := payloadPathToKey(path)
	metaOut := newObjectFunc()
	deleted := false
	reserved := ""
	if h.beforeGatedDelete != nil {
		h.beforeGatedDelete(key)
	}
	err := h.write(ctx, conn, holdPathCleanup, resourceFromKey(key), func(_ context.Context, lockedConn *sqlite.Conn) error {
		currentJSON, err := ReadMetadata(lockedConn, key)
		if errors.Is(err, ErrMetadataNotFound) {
			return nil // gone since the decision
		}
		if err != nil {
			return err
		}
		if !bytes.Equal(currentJSON, decidedJSON) {
			logger.L().Debug("cleanup - object changed since the decision, keeping it", helpers.String("key", key))
			return nil
		}
		raw, err := deleteMetadataRaw(lockedConn, key)
		if err != nil {
			return err
		}
		if IsContainerProfileKind(kind) {
			if err := DeleteTimeSeriesContainerEntries(lockedConn, key); err != nil {
				return fmt.Errorf("failed to delete time series entries: %w", err)
			}
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, metaOut); err != nil {
				return err
			}
		}
		name, err := reservedDeletingPath(path)
		if err != nil {
			return err
		}
		switch err := h.appFs.Rename(path, name); {
		case err == nil:
			reserved = name
		case errors.Is(err, os.ErrNotExist):
			// nothing left to remove
		default:
			// The rows go anyway. A payload left at its path is an orphan the next sweep
			// recovers under the gate, and nothing is removed after the gate for it.
			logger.L().Ctx(ctx).Warning("cleanup - could not reserve the payload, leaving it to the orphan sweep", helpers.Error(err), helpers.String("path", path))
		}
		deleted = true
		return nil
	})
	if err != nil {
		if reserved != "" {
			if restoreErr := h.appFs.Rename(reserved, path); restoreErr != nil {
				logger.L().Ctx(ctx).Error("cleanup - failed to put back a reserved payload", helpers.Error(restoreErr), helpers.String("path", reserved))
			}
		}
		return nil, false, err
	}
	if h.beforeReservedRemoval != nil {
		h.beforeReservedRemoval(key)
	}
	if reserved != "" {
		h.deleteFunc(h.appFs, reserved)
	}
	return metaOut, deleted, nil
}

// SetWriteGate hands the cleanup handler the shared write gate (nil = no
// gate). Called once at wiring time, before the first tick.
func (h *ResourcesCleanupHandler) SetWriteGate(gate *WriteGate) {
	h.gate = gate
}

// SetContainerProfileStore switches the ContainerProfile arm to row
// enumeration through store. Called once at wiring time, before the first
// processor cleanup.
func (h *ResourcesCleanupHandler) SetContainerProfileStore(store *ObjectStore) {
	h.cpStore = store
}

// ContainerProfileHandlers is the ContainerProfile arm's handler list, run
// from ContainerProfileProcessor.cleanup() only: the workload-liveness
// handler, plus the missing-annotation handlers when relevancy is on. The
// generic walk (RunCleanupTask) never carries the kind.
func (h *ResourcesCleanupHandler) ContainerProfileHandlers() []TypeCleanupHandlerFunc {
	handlers := []TypeCleanupHandlerFunc{deleteByTemplateHashOrWlid}
	if h.relevancyEnabled {
		handlers = append(handlers, deleteMissingInstanceIdAnnotation, deleteMissingWlidAnnotation)
	}
	return handlers
}

func (h *ResourcesCleanupHandler) write(ctx context.Context, conn *sqlite.Conn, path, kind string, fn func(ctx context.Context, conn *sqlite.Conn) error) error {
	return gatedWrite(h.gate, ctx, conn, priorityLow, path, kind, false, fn)
}

func initResourceToKindHandler() map[string][]TypeCleanupHandlerFunc {
	return map[string][]TypeCleanupHandlerFunc{
		// configurationscansummaries are virtual
		// containerprofiles are handled by containerprofile_processor
		// (ContainerProfileHandlers), never by this walk
		// vulnerabilitysummaries are virtual
		// DEPRECATED resources
		// applicationprofiles and networkneighborhoods were replaced by
		// containerprofiles; without an entry here their payload files and
		// sqlite rows would be kept forever on every upgraded cluster.
		"applicationactivities":       {deleteDeprecated},
		"applicationprofiles":         {deleteDeprecated},
		"applicationprofilesummaries": {deleteDeprecated},
		"networkneighborhoods":        {deleteDeprecated},
		"networkneighborses":          {deleteDeprecated},
		"sbomspdxv2p3filtereds":       {deleteDeprecated},
		"sbomspdxv2p3filtered":        {deleteDeprecated},
		"sbomspdxv2p3s":               {deleteDeprecated},
		"sbomspdxv2p3":                {deleteDeprecated},
		"sbomsummaries":               {deleteDeprecated},
		// CLUSTER level
		"sbomsyfts":              {deleteByImageId},
		"vulnerabilitymanifests": {deleteByImageIdOrInstanceId},
		// NAMESPACE level
		"openvulnerabilityexchangecontainers": {deleteByInstanceId},
		"sbomsyftfiltereds":                   {deleteByInstanceId},
		"seccompprofiles":                     {deleteByTemplateHashOrWlid},
		"vulnerabilitymanifestsummaries":      {deleteByWlidAndContainer},
		"workloadconfigurationscans":          {deleteByWlid},
		"workloadconfigurationscansummaries":  {deleteByWlid},
	}
}

// resourceKindToObjectFunc maps a cleanup-handled resource kind to a constructor
// for its scheme-registered API type. A delete performed by cleanupNamespace may
// be dispatched to an active watcher, and the apiserver's watch encoder can only
// serialize a type known to the runtime.Scheme (see issue #405, where the
// internal, unregistered file.PartialObjectMetadata helper was dispatched
// directly and broke watch encoding). Deprecated kinds handled by
// deleteDeprecated no longer have a type in pkg/apis/softwarecomposition or a
// REST endpoint that could register a watcher, so they are intentionally
// omitted here and never reach the watch dispatcher.
//
// ContainerProfileKind is needed for the legacy processor cleanup path.
// SQLite-backed ContainerProfiles are deleted through their ObjectStore.
var resourceKindToObjectFunc = map[string]func() runtime.Object{
	ContainerProfileKind:                  func() runtime.Object { return &softwarecomposition.ContainerProfile{} },
	"sbomsyfts":                           func() runtime.Object { return &softwarecomposition.SBOMSyft{} },
	"vulnerabilitymanifests":              func() runtime.Object { return &softwarecomposition.VulnerabilityManifest{} },
	"openvulnerabilityexchangecontainers": func() runtime.Object { return &softwarecomposition.OpenVulnerabilityExchangeContainer{} },
	"sbomsyftfiltereds":                   func() runtime.Object { return &softwarecomposition.SBOMSyftFiltered{} },
	"seccompprofiles":                     func() runtime.Object { return &softwarecomposition.SeccompProfile{} },
	"vulnerabilitymanifestsummaries":      func() runtime.Object { return &softwarecomposition.VulnerabilityManifestSummary{} },
	"workloadconfigurationscans":          func() runtime.Object { return &softwarecomposition.WorkloadConfigurationScan{} },
	"workloadconfigurationscansummaries":  func() runtime.Object { return &softwarecomposition.WorkloadConfigurationScanSummary{} },
}

func NewResourcesCleanupHandler(appFs afero.Fs, root string, pool *sqlitemigration.Pool, watchDispatcher *WatchDispatcher, interval time.Duration, defaultNamespace string, fetcher ResourcesFetcher, relevancyEnabled bool) *ResourcesCleanupHandler {
	if relevancyEnabled {
		logger.L().Debug("relevancy is enabled, adding additional container profile cleanup handlers")
	}
	return &ResourcesCleanupHandler{
		appFs:                 appFs,
		root:                  root,
		pool:                  pool,
		interval:              interval,
		defaultNamespace:      defaultNamespace,
		fetcher:               fetcher,
		deleteFunc:            deleteFile,
		resourceToKindHandler: initResourceToKindHandler(),
		watchDispatcher:       watchDispatcher,
		relevancyEnabled:      relevancyEnabled,
	}
}

func (h *ResourcesCleanupHandler) RunCleanupTask(ctx context.Context) {
	for {
		logger.L().Info("starting cleanup task", helpers.String("interval", h.interval.String()))
		err := h.CleanupTask(ctx, h.resourceToKindHandler)
		if err != nil {
			logger.L().Error("cleanup task error", helpers.Error(err))
			time.Sleep(h.interval)
			continue
		}

		if h.interval == 0 {
			break
		}

		logger.L().Info("finished cleanup task. sleeping...")
		time.Sleep(h.interval)
	}
}

func (h *ResourcesCleanupHandler) CleanupTask(ctx context.Context, resourceToKindHandler map[string][]TypeCleanupHandlerFunc) error {
	// Node discovery failure must not turn live hosts into orphaned resources.
	var hosts *hostResources
	nodes, nodeErr := h.fetcher.FetchNodes(ctx)
	if nodeErr != nil {
		logger.L().Warning("skipping host cleanup: node discovery failed", helpers.Error(nodeErr))
	} else {
		hosts = newHostResources(nodes)
		if hosts.incomplete {
			logger.L().Warning("skipping host cleanup: node inventory has missing machine IDs")
		}
	}
	// take SQLite connection from the pool
	conn, err := h.pool.Take(context.Background())
	defer h.pool.Put(conn)
	if err != nil {
		return fmt.Errorf("failed to take connection: %w", err)
	}
	// list namespaces
	namespaces, err := h.fetcher.ListNamespaces(conn)
	if err != nil {
		return fmt.Errorf("failed to list namespaces: %w", err)
	}
	// cluster level sets to aggregate data from all namespaces
	clusterRunningContainerImageIds := mapset.NewSet[string]()
	clusterRunningInstanceIds := mapset.NewSet[string]()
	// cleanup each namespace, except defaultNamespace that is handled at the end
	for _, ns := range namespaces {
		if ns == h.defaultNamespace {
			continue
		}
		resources, err := h.fetcher.FetchResources(ns)
		if err != nil {
			return fmt.Errorf("failed to fetch resources: %w", err)
		}
		resources.Hosts = hosts
		clusterRunningContainerImageIds.Append(resources.RunningContainerImageIds.ToSlice()...)
		clusterRunningInstanceIds.Append(resources.RunningInstanceIds.ToSlice()...)
		err = h.cleanupNamespace(ctx, ns, resourceToKindHandler, conn, resources)
		if err != nil {
			return err
		}
	}
	// cleanup cluster level resources inside defaultNamespace, together with the
	// workloads that run there: their images and instances count as running for the
	// cluster level records, and their own records are judged like any namespace's
	resources, err := h.fetcher.FetchResources(h.defaultNamespace)
	if err != nil {
		return fmt.Errorf("failed to fetch resources: %w", err)
	}
	resources.Hosts = hosts
	clusterRunningContainerImageIds.Append(resources.RunningContainerImageIds.ToSlice()...)
	clusterRunningInstanceIds.Append(resources.RunningInstanceIds.ToSlice()...)
	resources.RunningContainerImageIds = clusterRunningContainerImageIds
	resources.RunningInstanceIds = clusterRunningInstanceIds
	if err := h.cleanupNamespace(ctx, h.defaultNamespace, resourceToKindHandler, conn, resources); err != nil {
		return err
	}
	// Metadata rows are the usual source of namespaces. A failed payload
	// removal can leave a file behind after the last row in its namespace was
	// deleted, so revisit those on-disk namespaces after normal cleanup. An
	// orphan removal failure must not starve ordinary cleanup on later ticks.
	return h.cleanupOrphansInUnlistedNamespaces(ctx, conn, resourceToKindHandler, namespaces)
}

// cleanupOrphansInUnlistedNamespaces recovers payloads that normal cleanup
// cannot reach through the metadata-backed namespace list. A namespace with
// no metadata rows needs no workload inventory: only files without a matching
// row or legacy metadata sidecar are removed here. A rename under SQLite's
// write lock separates an orphan from its old key before Remove runs, so a
// concurrent Create cannot lose its newly committed payload.
func (h *ResourcesCleanupHandler) cleanupOrphansInUnlistedNamespaces(ctx context.Context, conn *sqlite.Conn, handlers map[string][]TypeCleanupHandlerFunc, namespaces []string) error {
	listed := make(map[string]struct{}, len(namespaces))
	var sweepErr error
	recordError := func(err error) {
		if sweepErr == nil {
			sweepErr = err
		}
	}
	for _, ns := range namespaces {
		listed[ns] = struct{}{}
	}
	for kind := range handlers {
		// The SQLite ContainerProfile backend owns this kind. Legacy files
		// remain on disk for the export tool and must not be walked here.
		if h.cpStore != nil && IsContainerProfileKind(kind) {
			continue
		}
		kindDir := filepath.Join(h.root, softwarecomposition.GroupName, kind)
		entries, err := afero.ReadDir(h.appFs, kindDir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			recordError(fmt.Errorf("read payload namespaces in %s: %w", kindDir, err))
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if _, ok := listed[entry.Name()]; ok {
				continue
			}
			nsDir := filepath.Join(kindDir, entry.Name())
			children, err := afero.ReadDir(h.appFs, nsDir)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				recordError(fmt.Errorf("read orphan payload directory %s: %w", nsDir, err))
				continue
			}
			if len(children) == 0 {
				continue
			}
			err = afero.Walk(h.appFs, nsDir, func(path string, info os.FileInfo, walkErr error) error {
				if errors.Is(walkErr, os.ErrNotExist) {
					return nil // another cleanup or writer removed it
				}
				if walkErr != nil {
					recordError(fmt.Errorf("walk orphan payload %s: %w", path, walkErr))
					return nil
				}
				fileErr := func() error {
					if !info.Mode().IsRegular() {
						return nil
					}
					if isCleanupQuarantineFile(path) {
						return h.removeCleanupQuarantine(path)
					}
					if original, ok := isCleanupDeletingFile(path); ok {
						return h.settleDeletingFile(conn, path, original)
					}
					if !IsPayloadFile(path) {
						return nil
					}
					key, err := keyFromPayloadPath(h.root, path)
					if err != nil {
						return err
					}
					if _, err := ReadMetadata(conn, key); err == nil {
						return nil // a concurrent writer made the namespace current
					} else if !errors.Is(err, ErrMetadataNotFound) {
						return fmt.Errorf("read metadata for %s: %w", path, err)
					}
					sidecar := path[:len(path)-len(GobExt)] + MetadataExt
					if _, err := h.appFs.Stat(sidecar); err == nil {
						// A legacy sidecar may still be the only metadata for this
						// payload. Leave the pair for its ordinary migration path.
						return nil
					} else if !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("stat metadata sidecar for %s: %w", path, err)
					}
					quarantine, err := h.quarantineOrphanPayload(ctx, conn, kind, path)
					if err != nil {
						return err
					}
					if quarantine != "" {
						return h.removeCleanupQuarantine(quarantine)
					}
					return nil
				}()
				if fileErr != nil {
					recordError(fmt.Errorf("recover orphan payload %s: %w", path, fileErr))
				}
				return nil
			})
			if err != nil {
				recordError(fmt.Errorf("walk orphan payloads in %s: %w", nsDir, err))
			}
		}
	}
	return sweepErr
}

func (h *ResourcesCleanupHandler) withCleanupWriteLock(ctx context.Context, conn *sqlite.Conn, kind string, fn func(context.Context, *sqlite.Conn) error) error {
	if h.gate != nil {
		return h.gate.run(ctx, priorityLow, holdPathCleanup, kind, fn)
	}
	end, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return fmt.Errorf("begin orphan recovery transaction: %w", err)
	}
	err = fn(ctx, conn)
	end(&err)
	return err
}

func isCleanupQuarantineFile(path string) bool {
	name := filepath.Base(path)
	if !strings.HasPrefix(name, cleanupQuarantinePrefix) || !strings.HasSuffix(name, cleanupQuarantineSuffix) {
		return false
	}
	nonce := strings.TrimSuffix(strings.TrimPrefix(name, cleanupQuarantinePrefix), cleanupQuarantineSuffix)
	if len(nonce) != 32 {
		return false
	}
	_, err := hex.DecodeString(nonce)
	return err == nil
}

// reservedDeletingPath names the file a payload is renamed to under the write gate before
// it is removed, so a create landing on the payload's path after the gate is released
// is never the file that gets removed.
func reservedDeletingPath(path string) (string, error) {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate deleting name: %w", err)
	}
	return path + "." + hex.EncodeToString(nonce[:]) + cleanupDeletingSuffix, nil
}

// isCleanupDeletingFile says whether path is a payload reserved for removal, and gives
// the payload path it was reserved from.
func isCleanupDeletingFile(path string) (string, bool) {
	if !strings.HasSuffix(path, cleanupDeletingSuffix) {
		return "", false
	}
	i := strings.LastIndex(path, GobExt+".")
	if i < 0 {
		return "", false
	}
	original := path[:i+len(GobExt)]
	nonce := path[i+len(GobExt)+1 : len(path)-len(cleanupDeletingSuffix)]
	if len(nonce) != 16 {
		return "", false
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		return "", false
	}
	return original, true
}

// settleDeletingFile finishes what a reserved payload was left in by a crash: with its
// row still there and nothing at its path, the rows were never deleted, so the payload
// goes back where the row expects it; otherwise the deletion went through and the
// reserved file is removed.
func (h *ResourcesCleanupHandler) settleDeletingFile(conn *sqlite.Conn, path, original string) error {
	if _, err := ReadMetadata(conn, payloadPathToKey(original)); err == nil {
		if exists, _ := afero.Exists(h.appFs, original); !exists {
			if err := h.appFs.Rename(path, original); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("restore reserved payload %s: %w", path, err)
			}
			return nil
		}
	} else if !errors.Is(err, ErrMetadataNotFound) {
		return fmt.Errorf("read metadata for %s: %w", original, err)
	}
	if err := h.appFs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove reserved payload %s: %w", path, err)
	}
	return nil
}

func (h *ResourcesCleanupHandler) removeCleanupQuarantine(path string) error {
	if err := h.appFs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove orphan payload %s: %w", path, err)
	}
	return nil
}

// quarantineOrphanPayload takes the same SQLite write lock as Create, then
// rechecks the row before moving the file. Only one same-directory rename is
// done while the gate is held; the potentially slow Remove follows after it.
// Quarantine names do not end in .g, so file-backed LIST cannot serve them.
func (h *ResourcesCleanupHandler) quarantineOrphanPayload(ctx context.Context, conn *sqlite.Conn, kind, path string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate orphan quarantine name: %w", err)
	}
	quarantine := filepath.Join(filepath.Dir(path), cleanupQuarantinePrefix+hex.EncodeToString(nonce[:])+cleanupQuarantineSuffix)
	if exists, err := afero.Exists(h.appFs, quarantine); err != nil {
		return "", fmt.Errorf("check orphan quarantine path: %w", err)
	} else if exists {
		return "", fmt.Errorf("orphan quarantine path already exists: %s", quarantine)
	}
	key, err := keyFromPayloadPath(h.root, path)
	if err != nil {
		return "", err
	}
	moved := false
	move := func(_ context.Context, lockedConn *sqlite.Conn) error {
		if _, err := ReadMetadata(lockedConn, key); err == nil {
			return nil // an in-flight Create committed while we waited
		} else if !errors.Is(err, ErrMetadataNotFound) {
			return fmt.Errorf("read metadata for %s: %w", path, err)
		}
		if err := h.appFs.Rename(path, quarantine); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return fmt.Errorf("quarantine orphan payload %s: %w", path, err)
		}
		moved = true
		return nil
	}
	err = h.withCleanupWriteLock(ctx, conn, kind, move)
	if err != nil {
		return "", fmt.Errorf("recover orphan payload %s: %w", path, err)
	}
	if moved {
		return quarantine, nil
	}
	return "", nil
}

func (h *ResourcesCleanupHandler) cleanupNamespace(ctx context.Context, ns string, resourceToKindHandler map[string][]TypeCleanupHandlerFunc, conn *sqlite.Conn, resources ResourceMaps) error {
	for resourceKind, handlers := range resourceToKindHandler {
		if h.cpStore != nil && IsContainerProfileKind(resourceKind) {
			if err := h.cleanupContainerProfileRows(ctx, ns, resourceKind, handlers, conn, resources); err != nil {
				return err
			}
			continue
		}
		v1beta1ApiVersionPath := filepath.Join(h.root, softwarecomposition.GroupName, resourceKind, ns)
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
			if isCleanupQuarantineFile(path) {
				if err := h.removeCleanupQuarantine(path); err != nil {
					logger.L().Ctx(ctx).Error("failed removing orphan quarantine", helpers.Error(err), helpers.String("path", path))
				}
				return nil
			}
			if original, ok := isCleanupDeletingFile(path); ok {
				if err := h.settleDeletingFile(conn, path, original); err != nil {
					logger.L().Ctx(ctx).Error("failed settling a reserved payload", helpers.Error(err), helpers.String("path", path))
				}
				return nil
			}

			if size := info.Size(); size > MinSizeToReport {
				logger.L().Ctx(ctx).Warning("large file detected, you may want to truncate it", helpers.String("path", path), helpers.String("size", fmt.Sprintf("%d bytes", size)))
			}

			// skip files that are not payload files
			if !IsPayloadFile(path) {
				return nil
			}

			metadata, decidedJSON, err := h.readMetadata(ctx, conn, path)
			if err != nil {
				logger.L().Error("load metadata error", helpers.Error(err))
				return nil
			}

			// Skip user-managed resources (e.g., user-defined profiles).
			if isUserManaged(metadata) {
				return nil
			}

			toDelete := shouldCleanupResource(handlers, resourceKind, path, metadata, resources)

			if toDelete {
				logger.L().Debug("deleting", helpers.String("kind", resourceKind), helpers.String("namespace", metadata.Namespace), helpers.String("name", metadata.Name))
				newObjectFunc, hasRegisteredType := resourceKindToObjectFunc[resourceKind]
				if !hasRegisteredType {
					newObjectFunc = func() runtime.Object { return &PartialObjectMetadata{} }
				}
				metaOut, deleted, err := h.deleteUnlessChanged(ctx, conn, resourceKind, path, decidedJSON, newObjectFunc)
				if err != nil {
					return fmt.Errorf("failed to delete metadata: %w", err)
				}
				// Only dispatch to watchers when metaOut is a scheme-registered
				// type (see resourceKindToObjectFunc) -- deprecated kinds have
				// no registered type and no REST endpoint to watch anyway.
				if deleted && h.watchDispatcher != nil && hasRegisteredType {
					key := path[len(h.root) : len(path)-len(GobExt)]
					h.watchDispatcher.Deleted(key, metaOut)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to walk %s: %w", v1beta1ApiVersionPath, err)
		}
	}
	return nil
}

// cleanupContainerProfileRows is the ContainerProfile arm under the flag:
// the namespace's rows are enumerated from the metadata table on the tick's
// connection and the reclaimed ones deleted through the ObjectStore, which
// removes metadata, payloads and time_series rows in one gated transaction
// and dispatches Deleted after it. No file is read, written or removed.
func (h *ResourcesCleanupHandler) cleanupContainerProfileRows(ctx context.Context, ns, resourceKind string, handlers []TypeCleanupHandlerFunc, conn *sqlite.Conn, resources ResourceMaps) error {
	type row struct {
		name         string
		metadataJSON []byte
	}
	var rows []row
	err := sqlitex.Execute(conn,
		`SELECT name, metadata FROM metadata WHERE kind = :kind AND namespace = :namespace`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": resourceKind, ":namespace": ns},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				rows = append(rows, row{name: stmt.ColumnText(0), metadataJSON: []byte(stmt.ColumnText(1))})
				return nil
			},
		})
	if err != nil {
		return fmt.Errorf("failed to list %s rows in %s: %w", resourceKind, ns, err)
	}
	for _, r := range rows {
		key := K8sKeysToPath("", softwarecomposition.GroupName, resourceKind, "", ns, r.name)
		metadata, err := loadMetadata(r.metadataJSON)
		if err != nil {
			logger.L().Error("load metadata error", helpers.Error(err), helpers.String("key", key))
			continue
		}
		if isUserManaged(metadata) {
			continue
		}
		if !shouldCleanupResource(handlers, resourceKind, key, metadata, resources) {
			continue
		}
		logger.L().Debug("deleting", helpers.String("kind", resourceKind), helpers.String("namespace", metadata.Namespace), helpers.String("name", metadata.Name))
		err = h.cpStore.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
		if err != nil && !storage.IsNotFound(err) {
			return fmt.Errorf("failed to delete %s: %w", key, err)
		}
	}
	return nil
}

// isUserManaged reports whether the given resource metadata carries the
// "user-managed" marker. The marker lives on Annotations by codebase
// convention (see pkg/apis/softwarecomposition/networkpolicy/v2/
// networkpolicy.go for the canonical read-site) — NOT on Labels.
// Reading from Labels would silently miss every user-managed resource
// and defeat the cleanup skip entirely.
func isUserManaged(metadata *metav1.ObjectMeta) bool {
	if metadata == nil {
		return false
	}
	return metadata.Annotations[helpersv1.ManagedByMetadataKey] == helpersv1.ManagedByUserValue
}

func shouldCleanupResource(handlers []TypeCleanupHandlerFunc, kind, path string, metadata *metav1.ObjectMeta, resources ResourceMaps) bool {
	if handled, remove := hostResourceCleanupDecision(kind, metadata, resources.Hosts); handled {
		return remove
	}
	return or(handlers, kind, path, metadata, resources)
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
	if !isCleanableWlidKind(kind) {
		if kind != "" {
			logger.L().Debug("skipping unknown kind", helpers.String("kind", kind))
		}
		return false
	}
	return !ok || !resourceMaps.RunningWlidsToContainerNames.Has(wlidWithoutClusterName(wlid))
}

func isCleanableWlidKind(kind string) bool {
	return kind == "pod" || Workloads.Contains(kind)
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
		// Check the workload kind - for Jobs and CronJobs, pods are often not running
		// so the template hash may not be in RunningTemplateHash even though the workload exists
		kind, kindFound := metadata.Labels[helpersv1.RelatedKindMetadataKey]
		if kindFound {
			kind = strings.ToLower(kind)
			if kind != "job" && kind != "cronjob" {
				return !resourceMaps.RunningTemplateHash.Contains(wlReplica)
			}
		}
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

// deleteWrongSchemaVersion deletes resources that have missing or unexpected schema version
func deleteWrongSchemaVersion(_, _ string, metadata *metav1.ObjectMeta, _ ResourceMaps) bool {
	// schema version is transferred via annotations by loadMetadata
	v, ok := metadata.Annotations["schemaVersion"]
	if ok && v == fmt.Sprintf("%d", SchemaVersion) {
		return false
	}
	logger.L().Debug("deleting resource with wrong schema version", helpers.String("name", metadata.Name), helpers.String("namespace", metadata.Namespace), helpers.String("schemaVersion", v))
	return true
}
