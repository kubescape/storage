package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	instanceidhandlerv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/k8s-interface/names"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file/callstack"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/kubescape/storage/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

type ContainerProfileProcessor struct {
	cleanupHandler          *ResourcesCleanupHandler
	cleanupInterval         time.Duration
	defaultNamespace        string
	deleteThreshold         time.Duration
	interval                time.Duration
	lastCleanup             time.Time
	maxContainerProfileSize int
	pool                    *sqlitemigration.Pool
	storageImpl             *StorageImpl
}

func NewContainerProfileProcessor(cfg config.Config, conn *sqlitemigration.Pool, cleanupHandler *ResourcesCleanupHandler) *ContainerProfileProcessor {
	return &ContainerProfileProcessor{
		cleanupHandler:          cleanupHandler,
		cleanupInterval:         cfg.CleanupInterval,
		defaultNamespace:        cfg.DefaultNamespace,
		deleteThreshold:         2 * cfg.MaxSniffingTime,
		interval:                30 * time.Second,
		pool:                    conn,
		maxContainerProfileSize: cfg.MaxApplicationProfileSize,
	}
}

var _ Processor = (*ContainerProfileProcessor)(nil)

// AfterCreate is called after a TS ContainerProfile is created to store metadata in SQLite.
func (a *ContainerProfileProcessor) AfterCreate(ctx context.Context, conn *sqlite.Conn, object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.ContainerProfile)
	if !ok {
		return fmt.Errorf("given object is not an ContainerProfile")
	}
	seriesID, ok := profile.Annotations[helpers.ReportSeriesIdMetadataKey]
	if !ok {
		// if the container ID annotation is not set, it's not a TS ContainerProfile and we skip it
		return nil
	}
	// parse name and namespace
	// remove the suffix from the name after the last hyphen
	name, tsSuffix := splitProfileName(profile.Name)
	namespace := profile.Namespace
	// parse annotations
	completion := profile.Annotations[helpers.CompletionMetadataKey]
	previousReportTimestamp := profile.Annotations[helpers.PreviousReportTimestampMetadataKey]
	reportTimestamp := profile.Annotations[helpers.ReportTimestampMetadataKey]
	status := profile.Annotations[helpers.StatusMetadataKey]
	// add sequence info to SQLite
	err := WriteTimeSeriesEntry(conn, "containerprofile", namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, true)
	if err != nil {
		logger.L().Ctx(ctx).Error("ContainerProfileProcessor.AfterCreate - failed to write time series data for container profile",
			loggerhelpers.Error(err),
			loggerhelpers.String("name", profile.Name),
			loggerhelpers.String("namespace", namespace),
			loggerhelpers.String("completion", completion),
			loggerhelpers.String("seriesID", seriesID),
			loggerhelpers.String("tsSuffix", tsSuffix),
			loggerhelpers.Interface("previousReportTimestamp", previousReportTimestamp),
			loggerhelpers.Interface("reportTimestamp", reportTimestamp),
			loggerhelpers.String("status", status))
		return fmt.Errorf("write time series data: %w", err)
	}
	return nil
}

func (a *ContainerProfileProcessor) PreSave(ctx context.Context, conn *sqlite.Conn, object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.ContainerProfile)
	if !ok {
		// do not return an error as we might call this on AP and NN as part of the updateProfile() flow below
		return nil
	}

	// detect TS profiles
	if profile.Annotations[helpers.ReportSeriesIdMetadataKey] != "" {
		// check size and completion for the corresponding container profile
		name, _ := splitProfileName(profile.Name)
		// load profile metadata if profile exists
		key := keysToPath("", "spdx.softwarecomposition.kubescape.io", "containerprofile", profile.Namespace, name)
		existingProfile := softwarecomposition.ContainerProfile{}
		err := a.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &existingProfile)
		if err != nil {
			return nil
		}
		switch existingProfile.Annotations[helpers.StatusMetadataKey] {
		case helpers.TooLarge:
			return ObjectTooLargeError
		case helpers.Completed:
			return ObjectCompletedError
		default:
			// skip processing of TS profiles
			return nil
		}
	}

	// size is the sum of all fields in all containers
	var size int

	var sbomSet mapset.Set[string]
	// get files from corresponding sbom
	sbomName, err := names.ImageInfoToSlug(profile.Spec.ImageTag, profile.Spec.ImageID)
	if err == nil {
		sbom := softwarecomposition.SBOMSyft{}
		key := keysToPath("", "spdx.softwarecomposition.kubescape.io", "sbomsyft", a.defaultNamespace, sbomName)
		if err := a.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &sbom); err == nil {
			// fill sbomSet
			sbomSet = mapset.NewSet[string]()
			for _, f := range sbom.Spec.Syft.Files {
				sbomSet.Add(f.Location.RealPath)
			}
		} else {
			logger.L().Debug("ContainerProfileProcessor.PreSave - failed to get sbom", loggerhelpers.Error(err), loggerhelpers.String("key", key))
		}
	} else {
		logger.L().Debug("ContainerProfileProcessor.PreSave - failed to get sbom name", loggerhelpers.Error(err), loggerhelpers.String("imageTag", profile.Spec.ImageTag), loggerhelpers.String("imageID", profile.Spec.ImageID))
	}
	profile.Spec = deflateContainerProfileSpec(profile.Spec, sbomSet)
	size += len(profile.Spec.Execs)
	size += len(profile.Spec.Opens)
	size += len(profile.Spec.Syscalls)
	size += len(profile.Spec.Capabilities)
	size += len(profile.Spec.Endpoints)
	size += len(profile.Spec.IdentifiedCallStacks)
	size += len(profile.Spec.Ingress)
	size += len(profile.Spec.Egress)

	if size > a.maxContainerProfileSize {
		// set annotation but don't return an error as we want to save the profile anyway
		profile.Annotations[helpers.StatusMetadataKey] = helpers.TooLarge
	}

	// make sure annotations are initialized
	if profile.Annotations == nil {
		profile.Annotations = make(map[string]string)
	}
	profile.Annotations[helpers.ResourceSizeMetadataKey] = strconv.Itoa(size)

	return nil
}

func (a *ContainerProfileProcessor) SetStorage(storageImpl *StorageImpl) {
	a.storageImpl = storageImpl
	if a.interval > 0 {
		go a.runMaintenanceTasks()
	}
}

func (a *ContainerProfileProcessor) runMaintenanceTasks() {
	for {
		// cleanup
		logger.L().Debug("ContainerProfileProcessor.runMaintenanceTasks - starting cleanup task")
		err := a.cleanup()
		if err != nil {
			logger.L().Error("ContainerProfileProcessor.runMaintenanceTasks - failed to complete cleanup task", loggerhelpers.Error(err))
		} else {
			logger.L().Debug("ContainerProfileProcessor.runMaintenanceTasks - cleanup task completed successfully")
		}
		// consolidation
		logger.L().Debug("ContainerProfileProcessor.runMaintenanceTasks - starting consolidation task", loggerhelpers.String("interval", a.interval.String()))
		err = a.consolidateTimeSeries()
		if err != nil {
			logger.L().Error("ContainerProfileProcessor.runMaintenanceTasks - failed to complete consolidation task", loggerhelpers.Error(err))
		} else {
			logger.L().Debug("ContainerProfileProcessor.runMaintenanceTasks - consolidation task completed successfully")
		}
		// sleep
		time.Sleep(a.interval)
	}
}

func (a *ContainerProfileProcessor) cleanup() error {
	if a.cleanupInterval == 0 && !a.lastCleanup.IsZero() {
		// no cleanup interval set, we run cleanup only once
		return nil
	}
	if time.Since(a.lastCleanup) < a.cleanupInterval {
		// cleanup interval not reached yet
		return nil
	}
	a.lastCleanup = time.Now()
	resourceToKindHandler := map[string][]TypeCleanupHandlerFunc{
		"applicationprofiles":  {deleteWrongSchemaVersion, deleteByTemplateHashOrWlid},
		"containerprofiles":    {deleteByTemplateHashOrWlid},
		"networkneighborhoods": {deleteWrongSchemaVersion, deleteByTemplateHashOrWlid},
	}
	return a.cleanupHandler.CleanupTask(context.TODO(), resourceToKindHandler)
}

func (a *ContainerProfileProcessor) consolidateTimeSeries() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // FIXME should we add a timeout here?
	conn, err := a.pool.Take(ctx)
	if err != nil {
		return fmt.Errorf("failed to take connection: %w", err)
	}
	defer a.pool.Put(conn)
	// clean up older time series
	if a.deleteThreshold > 0 {
		err = CleanOlderTimeSeries(conn, a.deleteThreshold)
		if err != nil {
			return fmt.Errorf("failed to clean up time series: %w", err)
		}
	}
	// list all time series keys with data
	keys, err := ListTimeSeriesKeys(conn)
	if err != nil {
		return fmt.Errorf("failed to list time series keys: %w", err)
	}
	// consolidate data for each key
	for _, key := range keys {
		logger.L().Debug("ContainerProfileProcessor.consolidateTimeSeries - consolidating data for key", loggerhelpers.String("key", key))
		// list all containers for the key
		timeSeries, err := ListTimeSeriesContainers(conn, key)
		if err != nil {
			return fmt.Errorf("failed to list time series containers: %w", err)
		}
		// load profile from disk if it exists
		profile := softwarecomposition.ContainerProfile{}
		err = a.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
		prefix, root, kind, namespace, name := pathToKeys(key)
		switch {
		case storage.IsNotFound(err):
			// remove error
			err = nil
			profile.APIVersion = StorageV1Beta1ApiVersion
			profile.Kind = kind
			profile.Namespace = namespace
			profile.Name = name
			profile.Annotations = map[string]string{}
			profile.Labels = map[string]string{}
		case err != nil:
			return fmt.Errorf("failed to get profile: %w", err)
		}
		// start a transaction
		endFn := sqlitex.Transaction(conn)
		processed, err := a.updateProfile(ctx, conn, timeSeries, key, profile, prefix, root, namespace)
		endFn(&err)
		if err != nil {
			return fmt.Errorf("failed to process time series data (transaction rolled back): %w", err)
		}
		// delete processed time series profiles
		for _, tsKey := range processed {
			// no locking needed for TS profiles
			err := a.storageImpl.delete(ctx, conn, tsKey, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
			// FIXME maybe try to delete others before exit?
			if err != nil {
				return fmt.Errorf("failed to delete processed time series profile: %w", err)
			}
		}
	}
	return nil
}

func (a *ContainerProfileProcessor) updateProfile(ctx context.Context, conn *sqlite.Conn, timeSeries map[string][]TimeSeriesContainers, key string, profile softwarecomposition.ContainerProfile, prefix string, root string, namespace string) ([]string, error) {
	var processed []string
	creationTimestamp := metav1.Now()
	var newData bool
	for seriesID := range timeSeries {
		var deleteTimeSeries []string
		// merge time series data for each container
		for k, ts := range timeSeries[seriesID] {
			deleteTimeSeries = append(deleteTimeSeries, ts.TsSuffix)
			if ts.HasData {
				// load TS profile from disk
				tsKey := key + "-" + ts.TsSuffix
				tsProfile := softwarecomposition.ContainerProfile{}
				// no locking needed for TS profiles
				err := a.storageImpl.get(ctx, conn, tsKey, storage.GetOptions{}, &tsProfile)
				switch {
				case storage.IsNotFound(err):
					timeSeries[seriesID][k].HasData = false
					continue
				case err != nil:
					return nil, fmt.Errorf("failed to get ts profile: %w", err)
				}
				newData = true
				mergeContainerProfileTS(&profile, &tsProfile)
				// mark as processed
				timeSeries[seriesID][k].HasData = false
				processed = append(processed, tsKey)
			}
		}
		// combine continuous time series entries
		j := 0
		var newTimeSeries []TimeSeriesContainers
		for i := 0; i < len(timeSeries[seriesID])-1; i++ {
			// time series are in reverse chronological order
			if timeSeries[seriesID][j].PreviousReportTimestamp == timeSeries[seriesID][i+1].ReportTimestamp {
				timeSeries[seriesID][j].PreviousReportTimestamp = timeSeries[seriesID][i+1].PreviousReportTimestamp
			} else {
				newTimeSeries = append(newTimeSeries, timeSeries[seriesID][j])
				j = i + 1
			}
		}
		newTimeSeries = append(newTimeSeries, timeSeries[seriesID][j])
		if t, err := time.Parse(time.RFC3339, timeSeries[seriesID][j].ReportTimestamp); err == nil && creationTimestamp.After(t) {
			creationTimestamp = metav1.NewTime(t)
		}
		// compute status and completion
		// an aggregated series is complete only if it has one element, no previous report timestamp and is completed
		var completed bool
		if len(newTimeSeries) == 1 && isZeroTime(newTimeSeries[0].PreviousReportTimestamp) && newTimeSeries[0].Status == helpers.Completed {
			if profile.Annotations[helpers.StatusMetadataKey] == helpers.Completed && profile.Annotations[helpers.CompletionMetadataKey] == helpers.Full {
				// do not override completed full
			} else {
				profile.Annotations[helpers.StatusMetadataKey] = helpers.Completed
				profile.Annotations[helpers.CompletionMetadataKey] = newTimeSeries[0].Completion
				completed = true
			}
			// do not save this time series as it is already consolidated
			newTimeSeries = newTimeSeries[:0]
		} else if profile.Annotations[helpers.StatusMetadataKey] != helpers.Completed {
			profile.Annotations[helpers.StatusMetadataKey] = helpers.Learning
			profile.Annotations[helpers.CompletionMetadataKey] = newTimeSeries[0].Completion
		}
		// abort processing if the profile is completed
		if completed {
			logger.L().Info("ContainerProfileProcessor.updateProfile - profile is completed, skipping further processing", loggerhelpers.String("key", key), loggerhelpers.String("seriesID", seriesID))
			// remove the time series data
			err := DeleteTimeSeriesContainerEntries(conn, key)
			if err != nil {
				return nil, fmt.Errorf("failed to delete time series data: %w", err)
			}
			// regular cleanup will remove the time series data if it cannot find the metadata in SQLite
			break
		}
		// write the consolidated time series data back to the database
		err := ReplaceTimeSeriesContainerEntries(conn, key, seriesID, deleteTimeSeries, newTimeSeries)
		if err != nil {
			return nil, fmt.Errorf("failed to replace consolidated time series data: %w", err)
		}
	}
	// check if it's worth saving
	if !newData {
		logger.L().Debug("ContainerProfileProcessor.updateProfile - no new data, skip saving profile", loggerhelpers.String("key", key))
		return processed, nil
	}
	// verify we have a valid profile before writing it to disk
	if _, ok := profile.Annotations[helpers.InstanceIDMetadataKey]; !ok {
		logger.L().Debug("ContainerProfileProcessor.updateProfile - skip saving invalid profile", loggerhelpers.String("key", key), loggerhelpers.Interface("profile", profile))
		return processed, nil
	}
	// update creation timestamp
	if profile.CreationTimestamp.IsZero() {
		profile.CreationTimestamp = creationTimestamp
	}
	wlid := profile.Annotations[helpers.WlidMetadataKey]
	// write the consolidated data back to disk
	tryUpdateContainerProfile := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		// this is a full replace
		return &profile, nil, nil
	}
	err := a.storageImpl.GuaranteedUpdateWithConn(ctx, conn, key, &softwarecomposition.ContainerProfile{}, true, nil, tryUpdateContainerProfile, &softwarecomposition.ContainerProfile{}, "")
	if err != nil {
		return nil, fmt.Errorf("failed to update container profile: %w", err)
	}
	// calculate the slug without the container name
	instanceID, err := instanceidhandlerv1.GenerateInstanceIDFromString(profile.Annotations[helpers.InstanceIDMetadataKey])
	if err != nil {
		return nil, fmt.Errorf("failed to create instance ID: %w", err)
	}
	slug, err := instanceID.GetSlug(true)
	if err != nil {
		return nil, fmt.Errorf("failed to get slug: %w", err)
	}
	// update the corresponding application profile, appending the container profile key to Parts
	apKey := keysToPath(prefix, root, "applicationprofiles", namespace, slug)
	var apChecksum string
	tryUpdateApplicationProfile := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		output := input.DeepCopyObject()
		ap, ok := output.(*softwarecomposition.ApplicationProfile)
		if !ok {
			return nil, nil, fmt.Errorf("given object is not an ApplicationProfile")
		}
		ap.Name = slug
		ap.Namespace = namespace
		if ap.CreationTimestamp.IsZero() {
			ap.CreationTimestamp = creationTimestamp
		}
		ap.SchemaVersion = SchemaVersion
		if ap.Parts == nil {
			ap.Parts = map[string]string{}
		}
		ap.Parts[key] = "" // checksum will be updated by getAggregatedData
		status, completion, checksum := a.getAggregatedData(ctx, conn, ap.Parts)
		apChecksum = checksum
		ap.Annotations = map[string]string{
			helpers.CompletionMetadataKey: completion,
			helpers.InstanceIDMetadataKey: instanceID.GetStringNoContainer(),
			helpers.StatusMetadataKey:     status,
			helpers.WlidMetadataKey:       wlid,
		}
		ap.Labels = map[string]string{}
		utils.MergeMaps(ap.Labels, profile.Labels)
		delete(ap.Labels, helpers.ContainerNameMetadataKey)
		return output, nil, nil
	}
	err = a.storageImpl.GuaranteedUpdateWithConn(ctx, conn, apKey, &softwarecomposition.ApplicationProfile{}, true, nil, tryUpdateApplicationProfile, nil, apChecksum)
	if err != nil {
		return nil, fmt.Errorf("failed to update application profile: %w", err)
	}
	// update the corresponding network neighborhood, appending the container profile key to Parts
	nnKey := keysToPath(prefix, root, "networkneighborhoods", namespace, slug)
	var nnChecksum string
	tryUpdateNetworkNeighborhood := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		output := input.DeepCopyObject()
		nn, ok := output.(*softwarecomposition.NetworkNeighborhood)
		if !ok {
			return nil, nil, fmt.Errorf("given object is not an NetworkNeighborhood")
		}
		nn.Name = slug
		nn.Namespace = namespace
		if nn.CreationTimestamp.IsZero() {
			nn.CreationTimestamp = creationTimestamp
		}
		nn.SchemaVersion = SchemaVersion
		if nn.Parts == nil {
			nn.Parts = map[string]string{}
		}
		nn.Parts[key] = "" // checksum will be updated by getAggregatedData
		status, completion, checksum := a.getAggregatedData(ctx, conn, nn.Parts)
		nnChecksum = checksum
		nn.Annotations = map[string]string{
			helpers.CompletionMetadataKey: completion,
			helpers.InstanceIDMetadataKey: instanceID.GetStringNoContainer(),
			helpers.StatusMetadataKey:     status,
			helpers.WlidMetadataKey:       wlid,
		}
		nn.Labels = map[string]string{}
		utils.MergeMaps(nn.Labels, profile.Labels)
		delete(nn.Labels, helpers.ContainerNameMetadataKey)
		return output, nil, nil
	}
	err = a.storageImpl.GuaranteedUpdateWithConn(ctx, conn, nnKey, &softwarecomposition.NetworkNeighborhood{}, true, nil, tryUpdateNetworkNeighborhood, nil, nnChecksum)
	if err != nil {
		return nil, fmt.Errorf("failed to update network neighborhood: %w", err)
	}
	return processed, nil
}

// getAggregatedData computes various data of the aggregated profile.
// A profile status is completed only if all its main containers are completed.
// A profile completion is full only if all its init/main containers are full.
// A profile sync checksum is the checksum of all container checksums.
func (a *ContainerProfileProcessor) getAggregatedData(ctx context.Context, conn *sqlite.Conn, parts map[string]string) (string, string, string) {
	mainContainers := 0
	completed := 0
	full := 0
	status := helpers.Learning
	completion := helpers.Partial
	hasher := sha256.New()
	for key := range parts {
		profile := softwarecomposition.ContainerProfile{}
		// checksum is only present in get metadata
		err := a.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &profile)
		if err != nil {
			logger.L().Debug("ContainerProfileProcessor.getAggregatedData - failed to get profile", loggerhelpers.Error(err), loggerhelpers.String("key", key))
			continue
		}
		// only main containers are considered for aggregated status
		if profile.Annotations[helpers.ContainerTypeMetadataKey] == "containers" {
			mainContainers++
			if profile.Annotations[helpers.StatusMetadataKey] == helpers.Completed {
				completed++
			}
		}
		if profile.Annotations[helpers.CompletionMetadataKey] == helpers.Full {
			full++
		}
		checksum := profile.Annotations[helpers.SyncChecksumMetadataKey]
		parts[key] = checksum
		hasher.Write([]byte(checksum)) // profile.Parts is sorted so the checksum is consistent
	}
	if completed == mainContainers && mainContainers > 0 {
		status = helpers.Completed
	}
	if full == len(parts) {
		completion = helpers.Full
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	logger.L().Debug("ContainerProfileProcessor.getAggregatedData - returning",
		loggerhelpers.Int("mainContainers", mainContainers), loggerhelpers.Int("completed", completed), loggerhelpers.Int("full", full),
		loggerhelpers.String("status", status), loggerhelpers.String("completion", completion), loggerhelpers.String("hash", hash))
	return status, completion, hash
}

func deflateContainerProfileSpec(container softwarecomposition.ContainerProfileSpec, sbomSet mapset.Set[string]) softwarecomposition.ContainerProfileSpec {
	opens, err := dynamicpathdetector.AnalyzeOpens(container.Opens, dynamicpathdetector.NewPathAnalyzer(OpenDynamicThreshold), sbomSet)
	if err != nil {
		logger.L().Debug("ContainerProfileProcessor.deflateContainerProfileSpec - falling back to DeflateStringer for opens", loggerhelpers.Error(err))
		opens = DeflateStringer(container.Opens)
	}
	endpoints := dynamicpathdetector.AnalyzeEndpoints(&container.Endpoints, dynamicpathdetector.NewPathAnalyzer(EndpointDynamicThreshold))
	identifiedCallStacks := callstack.UnifyIdentifiedCallStacks(container.IdentifiedCallStacks)

	return softwarecomposition.ContainerProfileSpec{
		Architectures:        DeflateSortString(container.Architectures),
		Capabilities:         DeflateSortString(container.Capabilities),
		Execs:                DeflateStringer(container.Execs),
		Opens:                opens,
		Syscalls:             DeflateSortString(container.Syscalls),
		SeccompProfile:       container.SeccompProfile,
		Endpoints:            endpoints,
		ImageTag:             container.ImageTag,
		ImageID:              container.ImageID,
		PolicyByRuleId:       DeflateRulePolicies(container.PolicyByRuleId),
		IdentifiedCallStacks: identifiedCallStacks,
		LabelSelector: metav1.LabelSelector{
			MatchLabels:      container.MatchLabels,
			MatchExpressions: DeflateLabelSelectorRequirement(container.MatchExpressions),
		},
		Ingress: deflateNetworkNeighbors(container.Ingress),
		Egress:  deflateNetworkNeighbors(container.Egress),
	}
}

func isZeroTime(s string) bool {
	switch s {
	case "", "0001-01-01 00:00:00 +0000 UTC", "0001-01-01T00:00:00Z":
		return true
	default:
		return false
	}
}

// mergeContainerProfileTS is copied from node-agent but works on the softwarecomposition internal type
func mergeContainerProfileTS(profile, tsProfile *softwarecomposition.ContainerProfile) {
	// merge annotations
	profile.Annotations = utils.MergeMaps(profile.Annotations, tsProfile.Annotations,
		helpers.CompletionMetadataKey, helpers.PreviousReportTimestampMetadataKey,
		helpers.ReportSeriesIdMetadataKey, helpers.ReportTimestampMetadataKey, helpers.StatusMetadataKey)
	// merge labels
	profile.Labels = utils.MergeMaps(profile.Labels, tsProfile.Labels)
	// merge spec
	profile.Spec.Architectures = append(profile.Spec.Architectures, tsProfile.Spec.Architectures...)
	profile.Spec.Capabilities = append(profile.Spec.Capabilities, tsProfile.Spec.Capabilities...)
	profile.Spec.Execs = append(profile.Spec.Execs, tsProfile.Spec.Execs...)
	profile.Spec.Opens = append(profile.Spec.Opens, tsProfile.Spec.Opens...)
	profile.Spec.Syscalls = append(profile.Spec.Syscalls, tsProfile.Spec.Syscalls...)
	profile.Spec.SeccompProfile = tsProfile.Spec.SeccompProfile
	profile.Spec.Endpoints = append(profile.Spec.Endpoints, tsProfile.Spec.Endpoints...)
	profile.Spec.ImageID = tsProfile.Spec.ImageID
	profile.Spec.ImageTag = tsProfile.Spec.ImageTag
	if profile.Spec.PolicyByRuleId == nil {
		profile.Spec.PolicyByRuleId = make(map[string]softwarecomposition.RulePolicy)
	}
	for k, v := range tsProfile.Spec.PolicyByRuleId {
		if existingPolicy, exists := profile.Spec.PolicyByRuleId[k]; exists {
			profile.Spec.PolicyByRuleId[k] = mergePolicies(existingPolicy, v)
		} else {
			profile.Spec.PolicyByRuleId[k] = v
		}
	}
	profile.Spec.IdentifiedCallStacks = append(profile.Spec.IdentifiedCallStacks, tsProfile.Spec.IdentifiedCallStacks...)
	profile.Spec.LabelSelector.MatchLabels = utils.MergeMaps(profile.Spec.LabelSelector.MatchLabels, tsProfile.Spec.LabelSelector.MatchLabels)
	profile.Spec.LabelSelector.MatchExpressions = append(profile.Spec.LabelSelector.MatchExpressions, tsProfile.Spec.LabelSelector.MatchExpressions...)
	profile.Spec.Ingress = append(profile.Spec.Ingress, tsProfile.Spec.Ingress...)
	profile.Spec.Egress = append(profile.Spec.Egress, tsProfile.Spec.Egress...)
}

// mergePolicies is copied from node-agent but works on the softwarecomposition internal type
func mergePolicies(primary, secondary softwarecomposition.RulePolicy) softwarecomposition.RulePolicy {
	mergedPolicy := softwarecomposition.RulePolicy{
		AllowedContainer: primary.AllowedContainer || secondary.AllowedContainer,
	}
	processes := mapset.NewSet[string]()
	for _, process := range primary.AllowedProcesses {
		processes.Add(process)
	}
	for _, process := range secondary.AllowedProcesses {
		processes.Add(process)
	}
	for process := range processes.Iter() {
		mergedPolicy.AllowedProcesses = append(mergedPolicy.AllowedProcesses, process)
	}
	return mergedPolicy
}

func splitProfileName(profileName string) (name string, tsSuffix string) {
	lastHyphenIndex := strings.LastIndex(profileName, "-")
	if lastHyphenIndex == -1 {
		// No hyphen found, so the whole string is the name, and suffix is empty
		return profileName, ""
	}
	name = profileName[:lastHyphenIndex]
	tsSuffix = profileName[lastHyphenIndex+1:]
	return name, tsSuffix
}
