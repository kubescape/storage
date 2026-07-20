package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
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
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

// ConsolidatedSlugData contains the slug (name) and namespace of a consolidated profile
type ConsolidatedSlugData struct {
	Name      string
	Namespace string
}

type ContainerProfileProcessor struct {
	CleanupHandler          *ResourcesCleanupHandler
	CleanupInterval         time.Duration
	DefaultNamespace        string
	DeleteThreshold         time.Duration
	HostType                armotypes.HostType
	Interval                time.Duration
	LastCleanup             time.Time
	MaxContainerProfileSize int
	ContainerProfileStorage ContainerProfileStorage
	ConsolidatedSlugChannel chan ConsolidatedSlugData
	// CollapseSettings is the lookup hook the deflate path consults for
	// per-prefix thresholds. Defaults to dynamicpathdetector.DefaultCollapseSettings;
	// production wiring may swap to a provider that reads the cluster-scoped
	// CollapseConfiguration "default" CR.
	CollapseSettings dynamicpathdetector.CollapseSettingsProvider
	// Workers bounds how many keys ConsolidateTimeSeries processes concurrently,
	// each on its own pool connection. Kept a fraction of the pool size so the
	// background consolidation never starves REST traffic of connections.
	Workers int
	// consolidateKey is the per-key consolidation entrypoint dispatched by
	// ConsolidateTimeSeries. nil means use consolidateKeyTimeSeries; tests
	// override it to count invocations or inject per-key failures.
	consolidateKey func(ctx context.Context, key string, expired bool) error
}

func NewContainerProfileProcessor(cfg config.Config, cleanupHandler *ResourcesCleanupHandler) *ContainerProfileProcessor {
	hostType := cfg.HostType
	if hostType == "" {
		hostType = armotypes.HostTypeKubernetes
	}
	return &ContainerProfileProcessor{
		CleanupHandler:          cleanupHandler,
		CleanupInterval:         cfg.CleanupInterval,
		DefaultNamespace:        cfg.DefaultNamespace,
		DeleteThreshold:         2 * cfg.MaxSniffingTime,
		HostType:                hostType,
		Interval:                30 * time.Second,
		MaxContainerProfileSize: cfg.MaxApplicationProfileSize,
		CollapseSettings:        dynamicpathdetector.DefaultCollapseSettings,
		Workers:                 max(1, DefaultPoolSize/4),
	}
}

var _ Processor = (*ContainerProfileProcessor)(nil)

// AfterCreate is called after a TS ContainerProfile is created to store metadata.
func (a *ContainerProfileProcessor) AfterCreate(ctx context.Context, object runtime.Object) error {
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
	name, tsSuffix := SplitProfileName(profile.Name)
	namespace := profile.Namespace
	// parse annotations
	completion := profile.Annotations[helpers.CompletionMetadataKey]
	previousReportTimestamp := profile.Annotations[helpers.PreviousReportTimestampMetadataKey]
	reportTimestamp := profile.Annotations[helpers.ReportTimestampMetadataKey]
	status := profile.Annotations[helpers.StatusMetadataKey]
	// add sequence info via storage interface
	err := a.ContainerProfileStorage.(*ContainerProfileStorageImpl).WriteTimeSeriesEntry(ctx, "containerprofile", namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, true)
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

func (a *ContainerProfileProcessor) PreSave(ctx context.Context, object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.ContainerProfile)
	if !ok || profile.Name == "" {
		// do not return an error as we might call this on AP and NN as part of the updateProfile() flow below
		return nil
	}

	// detect TS profiles
	if profile.Annotations[helpers.ReportSeriesIdMetadataKey] != "" {
		// check size and completion for the corresponding container profile
		name, _ := SplitProfileName(profile.Name)
		// load profile metadata if profile exists
		id := armotypes.ProfileIdentifier{
			ProfileScope: armotypes.ProfileScope{
				HostType:               a.HostType,
				Cluster:                profile.Annotations[helpers.ClusterMetadataKey],
				Namespace:              profile.Namespace,
				CloudAccountIdentifier: profile.Annotations[helpers.CloudAccountIdentifierMetadataKey],
				Region:                 profile.Annotations[helpers.RegionMetadataKey],
				HostID:                 profile.Annotations[helpers.HostIDMetadataKey],
			},
			Name: name,
		}
		key := BuildContainerProfileKey(id, "containerprofile")
		existingProfile, err := a.ContainerProfileStorage.GetContainerProfileMetadata(ctx, key)
		if err != nil {
			return nil
		}
		existingStatus := existingProfile.Annotations[helpers.StatusMetadataKey]
		if existingStatus == helpers.TooLarge {
			// reject TS profile if the existing profile is too large
			return ObjectTooLargeError
		} else if existingStatus == helpers.Completed {
			// reject TS profile if the existing profile is already completed and full
			// if the existing profile is completed and partial, we let complete TS profile amend it until it is full
			if existingProfile.Annotations[helpers.CompletionMetadataKey] == helpers.Full || profile.Annotations[helpers.CompletionMetadataKey] == helpers.Partial {
				return ObjectCompletedError
			}
		}
		return nil

	}

	// size is the sum of all fields in all containers
	var size int

	var sbomSet mapset.Set[string]
	// get files from corresponding sbom
	sbomName, err := names.ImageInfoToSlug(profile.Spec.ImageTag, profile.Spec.ImageID)
	if err == nil {
		id := armotypes.ProfileIdentifier{
			ProfileScope: armotypes.ProfileScope{
				HostType:               a.HostType,
				Cluster:                profile.Annotations[helpers.ClusterMetadataKey],
				Namespace:              a.DefaultNamespace, // sbom is stored in default namespace
				CloudAccountIdentifier: profile.Annotations[helpers.CloudAccountIdentifierMetadataKey],
				Region:                 profile.Annotations[helpers.RegionMetadataKey],
				HostID:                 profile.Annotations[helpers.HostIDMetadataKey],
			},
			Name: sbomName,
		}
		key := BuildContainerProfileKey(id, "sbomsyft")
		sbom, err := a.ContainerProfileStorage.GetSbom(ctx, key)
		if err == nil {
			// fill sbomSet
			sbomSet = mapset.NewSet[string]()
			for _, f := range sbom.Spec.Syft.Files {
				sbomSet.Add(f.Location.RealPath)
			}
		} else if !storage.IsNotFound(err) {
			logger.L().Debug("ContainerProfileProcessor.PreSave - failed to get sbom", loggerhelpers.Error(err), loggerhelpers.String("key", key))
		}
	} else {
		logger.L().Debug("ContainerProfileProcessor.PreSave - failed to get sbom name", loggerhelpers.Error(err), loggerhelpers.String("imageTag", profile.Spec.ImageTag), loggerhelpers.String("imageID", profile.Spec.ImageID))
	}
	settings := dynamicpathdetector.DefaultCollapseSettings()
	if a.CollapseSettings != nil {
		settings = a.CollapseSettings()
	}
	profile.Spec = DeflateContainerProfileSpec(profile.Spec, sbomSet, settings)
	size += len(profile.Spec.Execs)
	size += len(profile.Spec.Opens)
	size += len(profile.Spec.Syscalls)
	size += len(profile.Spec.Capabilities)
	size += len(profile.Spec.Endpoints)
	size += len(profile.Spec.IdentifiedCallStacks)
	size += len(profile.Spec.Ingress)
	size += len(profile.Spec.Egress)

	if size > a.MaxContainerProfileSize {
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

func (a *ContainerProfileProcessor) SetStorage(containerProfileStorage ContainerProfileStorage) {
	a.ContainerProfileStorage = containerProfileStorage
	if a.Interval > 0 {
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
		logger.L().Debug("ContainerProfileProcessor.runMaintenanceTasks - starting consolidation task", loggerhelpers.String("interval", a.Interval.String()))
		err = a.ConsolidateTimeSeries(context.Background())
		if err != nil {
			logger.L().Error("ContainerProfileProcessor.runMaintenanceTasks - failed to complete consolidation task", loggerhelpers.Error(err))
		} else {
			logger.L().Debug("ContainerProfileProcessor.runMaintenanceTasks - consolidation task completed successfully")
		}
		// sleep
		time.Sleep(a.Interval)
	}
}

func (a *ContainerProfileProcessor) cleanup() error {
	if a.CleanupInterval == 0 && !a.LastCleanup.IsZero() {
		// no cleanup interval set, we run cleanup only once
		return nil
	}
	if time.Since(a.LastCleanup) < a.CleanupInterval {
		// cleanup interval not reached yet
		return nil
	}
	a.LastCleanup = time.Now()
	resourceToKindHandler := map[string][]TypeCleanupHandlerFunc{
		"applicationprofiles": {deleteWrongSchemaVersion, deleteByTemplateHashOrWlid},
		"containerprofiles":   {deleteByTemplateHashOrWlid},
		// The merged (effective) CP carries the same templateHash/wlid metadata
		// as its observed sibling, so the same predicate retires orphans. This
		// covers workloads that get age-cleaned without going through the REST
		// Delete path (which already cascades to the merged sibling).
		ContainerProfileMergedKind: {deleteByTemplateHashOrWlid},
		"networkneighborhoods":     {deleteWrongSchemaVersion, deleteByTemplateHashOrWlid},
	}
	return a.CleanupHandler.CleanupTask(context.TODO(), resourceToKindHandler)
}

// ConsolidateTimeSeries processes all time series data, handling expired and active series separately.
//
// The function runs in two phases:
// 1. Process expired time series (past deleteThreshold) - marked as Completed/Partial
// 2. Process active time series with data - follow normal completion flow
//
// Expired time series are always marked as Completed/Partial unless they were already Completed/Full,
// ensuring incomplete profiles don't remain in a Learning state indefinitely.
func (a *ContainerProfileProcessor) ConsolidateTimeSeries(ctx context.Context) error {
	// Phase 0: list keys under a short-lived connection, then release it so the
	// per-key workers below each acquire their own connection from the pool.
	listCtx, cleanup, err := a.ContainerProfileStorage.WithConnection(ctx)
	if err != nil {
		return fmt.Errorf("failed to take connection for listing: %w", err)
	}
	// Phase 1: expired time series (past deleteThreshold), marked Completed/Partial.
	expired, err := a.ContainerProfileStorage.ListTimeSeriesExpired(listCtx, a.DeleteThreshold)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to list expired time series: %w", err)
	}
	// Phase 2: active time series with data, following the normal completion flow.
	withData, err := a.ContainerProfileStorage.ListTimeSeriesWithData(listCtx)
	if err != nil {
		cleanup()
		return fmt.Errorf("failed to list active time series: %w", err)
	}
	cleanup()

	// De-duplicate into one work set keyed by storage key. The time_series table
	// holds many rows per (kind,namespace,name) key and neither list applies
	// DISTINCT/GROUP BY, so a key can appear multiple times within one list and
	// in both lists. Expired precedence: a key present in both lists (or multiple
	// times within either list) is processed EXACTLY ONCE, as expired — preserving
	// today's expired-first semantics and avoiding two workers racing one key.
	type workItem struct {
		key     string
		expired bool
	}
	seen := make(map[string]int, len(expired)+len(withData))
	work := make([]workItem, 0, len(expired)+len(withData))
	add := func(key string, exp bool) {
		if i, ok := seen[key]; ok {
			if exp {
				work[i].expired = true // upgrade to expired (expired precedence)
			}
			return
		}
		seen[key] = len(work)
		work = append(work, workItem{key: key, expired: exp})
	}
	for _, k := range expired {
		add(k, true)
	}
	for _, k := range withData {
		add(k, false)
	}

	workers := a.Workers
	if workers < 1 {
		workers = 1
	}

	// Use a plain errgroup.Group (NOT errgroup.WithContext) and pass the PARENT
	// ctx to every worker. WithConnection -> pool.Take binds each connection's
	// interrupt to the passed ctx; a cancel-on-first-error group context would
	// interrupt and roll back every other worker's in-flight transaction. A
	// zero-value Group still returns the first error from Wait but never cancels,
	// so each key's transaction commits or fails independently.
	consolidate := a.consolidateKeyTimeSeries
	if a.consolidateKey != nil {
		consolidate = a.consolidateKey
	}
	var g errgroup.Group
	g.SetLimit(workers)
	for _, it := range work {
		g.Go(func() error {
			return consolidate(ctx, it.key, it.expired)
		})
	}
	return g.Wait()
}

// consolidateKeyTimeSeries consolidates time series data for a single key.
//
// The expired parameter indicates whether this time series has exceeded the deleteThreshold.
// When expired=true, the resulting profile will be marked as Completed/Partial (unless already Completed/Full).
func (a *ContainerProfileProcessor) consolidateKeyTimeSeries(ctx context.Context, key string, expired bool) error {
	logger.L().Debug("ContainerProfileProcessor.consolidateKeyTimeSeries - consolidating data for key", loggerhelpers.String("key", key), loggerhelpers.Interface("expired", expired))

	// Each unit of work owns its own pool connection so keys can be consolidated
	// concurrently, each transaction running on its own *sqlite.Conn.
	ctx, cleanup, err := a.ContainerProfileStorage.WithConnection(ctx)
	if err != nil {
		return fmt.Errorf("failed to take connection for key %s: %w", key, err)
	}
	defer cleanup()

	timeSeries, err := a.ContainerProfileStorage.ListTimeSeriesContainers(ctx, key)
	if err != nil {
		return fmt.Errorf("failed to list time series containers: %w", err)
	}

	profile, id, prefix, root, err := a.loadOrInitializeProfile(ctx, key)
	if err != nil {
		return err
	}

	processed, err := a.processTimeSeriesInTransaction(ctx, timeSeries, key, profile, prefix, root, id, expired)
	if err != nil {
		return err
	}

	// Send consolidated slug to channel before deleting processed time series
	// This allows downstream processing even if the ingester dies after consolidation
	// Only send for k8s host type
	if a.HostType == armotypes.HostTypeKubernetes {
		if err := a.sendConsolidatedSlugToChannel(ctx, profile, id); err != nil {
			return err
		}
	}

	if err := a.deleteProcessedTimeSeries(ctx, processed); err != nil {
		return err
	}

	logger.L().Debug("ContainerProfileProcessor.consolidateKeyTimeSeries - finished consolidating data for key", loggerhelpers.String("key", key))
	return nil
}

// sendConsolidatedSlugToChannel calculates the slug from the profile and sends it to the channel
// The slug is calculated for both ApplicationProfile and NetworkNeighborhood
// Format: "namespace/name" to allow the ingester to extract both namespace and name
func (a *ContainerProfileProcessor) sendConsolidatedSlugToChannel(ctx context.Context, profile softwarecomposition.ContainerProfile, id armotypes.ProfileIdentifier) error {
	if a.ConsolidatedSlugChannel == nil {
		return nil
	}

	// Check if profile has instance ID annotation (required for slug calculation)
	instanceIDStr, ok := profile.Annotations[helpers.InstanceIDMetadataKey]
	if !ok {
		return fmt.Errorf("ContainerProfileProcessor.sendConsolidatedSlugToChannel - instance ID annotation not found")
	}

	instanceID, err := instanceidhandlerv1.GenerateInstanceIDFromString(instanceIDStr)
	if err != nil {
		return fmt.Errorf("ContainerProfileProcessor.sendConsolidatedSlugToChannel - failed to generate instance ID: %w", err)
	}

	slug, err := instanceID.GetSlug(true)
	if err != nil {
		return fmt.Errorf("ContainerProfileProcessor.sendConsolidatedSlugToChannel - failed to get slug: %w", err)
	}

	// Send slug data to channel (blocking - will wait if channel is full)
	slugData := ConsolidatedSlugData{
		Name:      slug,
		Namespace: id.Namespace,
	}
	select {
	case a.ConsolidatedSlugChannel <- slugData:
	case <-ctx.Done():
		return ctx.Err()
	}

	return nil
}

// loadOrInitializeProfile loads an existing profile or creates a new one
func (a *ContainerProfileProcessor) loadOrInitializeProfile(ctx context.Context, key string) (
	profile softwarecomposition.ContainerProfile, id armotypes.ProfileIdentifier, prefix, root string, err error) {

	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	profile, err = a.ContainerProfileStorage.GetContainerProfile(cpCtx, key)

	id, prefix, root, kind, parseErr := ParseContainerProfileKey(key, a.HostType)
	if parseErr != nil {
		return profile, id, "", "", fmt.Errorf("failed to parse profile key: %w", parseErr)
	}

	switch {
	case storage.IsNotFound(err):
		err = nil
		profile = softwarecomposition.ContainerProfile{
			TypeMeta: metav1.TypeMeta{
				APIVersion: StorageV1Beta1ApiVersion,
				Kind:       kind,
			},
			ObjectMeta: metav1.ObjectMeta{
				Namespace:   id.Namespace,
				Name:        id.Name,
				Annotations: map[string]string{},
				Labels:      map[string]string{},
			},
		}
	case err != nil:
		err = fmt.Errorf("failed to get profile: %w", err)
	}

	return profile, id, prefix, root, err
}

// processTimeSeriesInTransaction processes time series data within a database transaction
func (a *ContainerProfileProcessor) processTimeSeriesInTransaction(ctx context.Context,
	timeSeries map[string][]softwarecomposition.TimeSeriesContainers, key string,
	profile softwarecomposition.ContainerProfile, prefix, root string, id armotypes.ProfileIdentifier, expired bool) ([]string, error) {

	endFn, err := a.ContainerProfileStorage.BeginTransaction(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin nested transaction: %w", err)
	}
	processed, err := a.updateProfile(ctx, timeSeries, key, profile, prefix, root, id, expired)
	endFn(&err)

	if err != nil {
		return nil, fmt.Errorf("failed to process time series data for key %s (transaction rolled back): %w", key, err)
	}

	return processed, nil
}

// deleteProcessedTimeSeries removes processed time series profiles from storage.
// Treats "key not found" as success (idempotent delete): the profile may already have been
// deleted by a concurrent consolidation run for the same customer/cluster.
func (a *ContainerProfileProcessor) deleteProcessedTimeSeries(ctx context.Context, processed []string) error {
	for _, tsKey := range processed {
		err := a.ContainerProfileStorage.DeleteContainerProfile(ctx, tsKey)
		if err != nil {
			if isKeyNotFoundErr(err) {
				logger.L().Debug("deleteProcessedTimeSeries - TS profile already deleted, skipping",
					loggerhelpers.String("tsKey", tsKey), loggerhelpers.Error(err))
				continue
			}
			return fmt.Errorf("failed to delete processed time series profile: %w", err)
		}
	}
	return nil
}

func (a *ContainerProfileProcessor) updateProfile(ctx context.Context, timeSeries map[string][]softwarecomposition.TimeSeriesContainers, key string, profile softwarecomposition.ContainerProfile, prefix, root string, id armotypes.ProfileIdentifier, expired bool) ([]string, error) {
	var processed []string
	creationTimestamp := metav1.Now()
	var newData bool

	// Process each time series
	for seriesID := range timeSeries {
		processResult, err := a.processTimeSeries(ctx, timeSeries, seriesID, key, &profile, &creationTimestamp, expired)
		if err != nil {
			return nil, err
		}
		processed = append(processed, processResult.processed...)
		if processResult.hasNewData {
			newData = true
		}
		if processResult.skipFurtherProcessing {
			break
		}
	}

	if _, ok := profile.Annotations[helpers.InstanceIDMetadataKey]; !ok {
		// Without an InstanceID annotation we cannot derive the workload slug,
		// so neither the observed save nor the merged refresh have a target.
		logger.L().Debug("ContainerProfileProcessor.updateProfile - skip saving invalid profile", loggerhelpers.String("key", key), loggerhelpers.Interface("profile", profile))
		return processed, nil
	}

	// Persist the canonical observed CP only when time-series consolidation
	// produced new data this tick. The observed CP is the time-series-only
	// view (kubescape/storage#315 review). It is never mutated by the ug- merge.
	if newData {
		if profile.CreationTimestamp.IsZero() {
			profile.CreationTimestamp = creationTimestamp
		}
		if err := a.ContainerProfileStorage.SaveContainerProfile(ctx, key, &profile); err != nil {
			return nil, err
		}
	} else {
		logger.L().Debug("ContainerProfileProcessor.updateProfile - no new data, observed CP unchanged", loggerhelpers.String("key", key))
	}

	// Refresh the merged (effective) CP every tick, even when !newData. This
	// is what propagates user-managed (ug-) edits and deletes to idle or
	// Completed workloads — the previous design short-circuited here and
	// stranded the merged artifact. refreshMergedProfile rebuilds from scratch
	// from (observed, ug-AP, ug-NN), so retractions land naturally.
	effective, err := a.refreshMergedProfile(ctx, &profile, id, key)
	if err != nil {
		// Refresh failures are surfaced so the transaction rolls back; a half-
		// applied merged write paired with a successful observed save would be
		// worse than retrying the whole tick.
		return nil, err
	}

	// Aggregated AP/NN derive from the effective CP so all downstream outputs
	// stay aligned with what node-agent actually reads (step 6 of the review).
	// Still gated on newData to preserve the existing 30s aggregation cadence —
	// ug-only changes propagate via the merged refresh above; the AP/NN
	// aggregator already serves a different (downstream-policy) audience.
	if newData {
		if err := a.updateAggregatedProfiles(ctx, key, effective, prefix, root, id, creationTimestamp); err != nil {
			return nil, err
		}
	}

	return processed, nil
}

// refreshMergedProfile rebuilds the merged (effective) ContainerProfile from
// the observed CP plus the live user-managed (ug-) AP/NN overlay, and
// reconciles the persisted merged artifact with the result:
//
//   - If at least one ug- input exists: write the freshly merged CP to the
//     parallel containerprofile-merged key.
//   - If no ug- input exists: delete the parallel key so consumers fall back
//     to the observed CP. This is the retraction path that the previous
//     in-place merge could not implement.
//
// Returns the "effective" CP — the merged one when ug- contributed, otherwise
// the observed CP itself. Callers pass this to downstream derivations
// (aggregated AP/NN) so all consumers see the same view node-agent will read.
func (a *ContainerProfileProcessor) refreshMergedProfile(ctx context.Context, observed *softwarecomposition.ContainerProfile, id armotypes.ProfileIdentifier, observedKey string) (*softwarecomposition.ContainerProfile, error) {
	merged, hasOverlay, err := a.buildMergedProfile(ctx, observed, id)
	if err != nil {
		return observed, err
	}

	if !hasOverlay {
		// No ug- input. Delete any prior merged artifact so consumers fall back
		// to observed. DeleteMergedContainerProfile is idempotent (it does a
		// lock-free existence probe and treats not-found as success), so the
		// common no-merged-yet path is quiet — no error log and no futile
		// delete — without a separate existence probe here. A genuine delete
		// failure is surfaced as a hard error so the tick rolls back and retries
		// rather than leaving consumers reading a merged view the ug- overlay no
		// longer backs.
		if delErr := a.ContainerProfileStorage.DeleteMergedContainerProfile(ctx, observedKey); delErr != nil {
			return observed, fmt.Errorf("failed to delete stale merged container profile: %w", delErr)
		}
		return observed, nil
	}

	if saveErr := a.ContainerProfileStorage.SaveMergedContainerProfile(ctx, observedKey, merged); saveErr != nil {
		return observed, fmt.Errorf("failed to save merged container profile: %w", saveErr)
	}
	return merged, nil
}

// timeSeriesProcessResult holds the results of processing a time series
type timeSeriesProcessResult struct {
	processed             []string
	hasNewData            bool
	skipFurtherProcessing bool
}

// processTimeSeries processes a single time series and returns the result
func (a *ContainerProfileProcessor) processTimeSeries(ctx context.Context,
	timeSeries map[string][]softwarecomposition.TimeSeriesContainers, seriesID, key string,
	profile *softwarecomposition.ContainerProfile, creationTimestamp *metav1.Time, expired bool) (timeSeriesProcessResult, error) {

	result := timeSeriesProcessResult{}

	// Merge time series data
	deleteTimeSeries, processed, hasNewData := a.mergeTimeSeriesData(ctx, timeSeries[seriesID], key, profile)
	result.processed = processed
	result.hasNewData = hasNewData

	// Consolidate continuous time series entries
	newTimeSeries := a.consolidateContinuousTimeSeries(timeSeries[seriesID], creationTimestamp)

	// Update profile status based on time series state
	newTimeSeries, skipFurtherProcessing, err := a.updateProfileStatus(ctx, key, seriesID, profile, newTimeSeries, expired)
	if err != nil {
		return result, err
	}
	result.skipFurtherProcessing = skipFurtherProcessing

	// Write consolidated data back to database
	if err := a.ContainerProfileStorage.ReplaceTimeSeriesContainerEntries(ctx, key, seriesID, deleteTimeSeries, newTimeSeries); err != nil {
		return result, fmt.Errorf("failed to replace consolidated time series data: %w", err)
	}

	return result, nil
}

// mergeTimeSeriesData merges time series data into the profile
func (a *ContainerProfileProcessor) mergeTimeSeriesData(ctx context.Context,
	timeSeriesContainers []softwarecomposition.TimeSeriesContainers, key string, profile *softwarecomposition.ContainerProfile) (deleteList []string, processed []string, hasNewData bool) {

	for k, ts := range timeSeriesContainers {
		deleteList = append(deleteList, ts.TsSuffix)
		if !ts.HasData {
			continue
		}

		// Load TS profile from disk
		tsKey := key + "-" + ts.TsSuffix
		tsProfile, err := a.ContainerProfileStorage.GetTsContainerProfile(ctx, tsKey)

		switch {
		case storage.IsNotFound(err):
			timeSeriesContainers[k].HasData = false
			continue
		case err != nil:
			// Log error but continue processing other entries
			logger.L().Debug("ContainerProfileProcessor.mergeTimeSeriesData - failed to get ts profile",
				loggerhelpers.Error(err), loggerhelpers.String("tsKey", tsKey))
			continue
		}

		hasNewData = true
		mergeContainerProfileTS(profile, &tsProfile)
		timeSeriesContainers[k].HasData = false
		processed = append(processed, tsKey)
	}

	return deleteList, processed, hasNewData
}

// consolidateContinuousTimeSeries combines continuous time series entries
func (a *ContainerProfileProcessor) consolidateContinuousTimeSeries(
	timeSeries []softwarecomposition.TimeSeriesContainers, creationTimestamp *metav1.Time) []softwarecomposition.TimeSeriesContainers {

	if len(timeSeries) == 0 {
		return nil
	}

	j := 0
	var newTimeSeries []softwarecomposition.TimeSeriesContainers

	for i := 0; i < len(timeSeries)-1; i++ {
		// time series are in reverse chronological order
		if timeSeries[j].PreviousReportTimestamp == timeSeries[i+1].ReportTimestamp {
			timeSeries[j].PreviousReportTimestamp = timeSeries[i+1].PreviousReportTimestamp
		} else {
			newTimeSeries = append(newTimeSeries, timeSeries[j])
			j = i + 1
		}
	}
	newTimeSeries = append(newTimeSeries, timeSeries[j])

	// Update creation timestamp if this is earlier
	if t, err := time.Parse(time.RFC3339, timeSeries[j].ReportTimestamp); err == nil && creationTimestamp.After(t) {
		*creationTimestamp = metav1.NewTime(t)
	}

	return newTimeSeries
}

// updateProfileStatus updates the profile status based on time series state.
//
// When expired=true, the profile is marked as Completed/Partial instead of Learning,
// unless it's already Completed/Full (safeguard). This ensures expired time series
// don't remain in Learning state indefinitely.
//
// Returns true if further processing should be skipped (e.g., profile is fully completed).
func (a *ContainerProfileProcessor) updateProfileStatus(ctx context.Context, key, seriesID string,
	profile *softwarecomposition.ContainerProfile, newTimeSeries []softwarecomposition.TimeSeriesContainers, expired bool) ([]softwarecomposition.TimeSeriesContainers, bool, error) {

	// If the time series is expired, we finalize it as Completed/Partial (unless it is already Completed/Full)
	// and clear the time series data so we don't leak zombie records.
	if expired {
		// Try to mark it as Completed/Full if we actually have a Completed status and the series is continuous
		var isFull bool
		if len(newTimeSeries) == 1 && isZeroTime(newTimeSeries[0].PreviousReportTimestamp) && newTimeSeries[0].Status == helpers.Completed {
			isFull = profile.SetCompletedStatus(newTimeSeries[0])
		}

		if isFull {
			logger.L().Debug("ContainerProfileProcessor.updateProfileStatus - expired profile is completed/full, skipping further processing",
				loggerhelpers.String("key", key), loggerhelpers.String("seriesID", seriesID))

			// Remove all time series data
			if err := a.ContainerProfileStorage.DeleteTimeSeriesContainerEntries(ctx, key); err != nil {
				return newTimeSeries, false, fmt.Errorf("failed to delete time series data: %w", err)
			}
			return newTimeSeries[:0], true, nil
		}

		// Otherwise, mark it as Completed/Partial (unless already Completed/Full)
		if profile.Annotations[helpers.StatusMetadataKey] != helpers.Completed || profile.Annotations[helpers.CompletionMetadataKey] != helpers.Full {
			profile.Annotations[helpers.StatusMetadataKey] = helpers.Completed
			profile.Annotations[helpers.CompletionMetadataKey] = helpers.Partial
		}
		return newTimeSeries[:0], false, nil
	}

	// Normal active (non-expired) flow below:
	// An aggregated series is removed only if it has one element, no previous report timestamp, and is completed or failed
	if len(newTimeSeries) != 1 || !isZeroTime(newTimeSeries[0].PreviousReportTimestamp) {
		profile.SetLearningStatus(newTimeSeries[0]) // series is missing some TS entries
		return newTimeSeries, false, nil
	}

	switch newTimeSeries[0].Status {
	case helpers.Completed:
		// Safeguard: if already fully completed, keep it that way
		if profile.SetCompletedStatus(newTimeSeries[0]) {
			logger.L().Debug("ContainerProfileProcessor.updateProfileStatus - profile is completed/full, skipping further processing",
				loggerhelpers.String("key", key), loggerhelpers.String("seriesID", seriesID))

			// Remove all time series data
			if err := a.ContainerProfileStorage.DeleteTimeSeriesContainerEntries(ctx, key); err != nil {
				return newTimeSeries, false, fmt.Errorf("failed to delete time series data: %w", err)
			}
			return newTimeSeries[:0], true, nil
		}
		// Clear this time series as it is finished
		newTimeSeries = newTimeSeries[:0]

	case helpers.Failed:
		profile.SetFailedStatus(newTimeSeries[0])
		// Clear this time series as it is finished
		newTimeSeries = newTimeSeries[:0]

	default:
		profile.SetLearningStatus(newTimeSeries[0]) // series is complete but not finished
	}

	return newTimeSeries, false, nil
}

// updateAggregatedProfiles updates the application profile and network neighborhood
func (a *ContainerProfileProcessor) updateAggregatedProfiles(ctx context.Context,
	key string, profile *softwarecomposition.ContainerProfile, prefix, root string, id armotypes.ProfileIdentifier,
	creationTimestamp metav1.Time) error {

	instanceID, err := instanceidhandlerv1.GenerateInstanceIDFromString(profile.Annotations[helpers.InstanceIDMetadataKey])
	if err != nil {
		return fmt.Errorf("failed to create instance ID: %w", err)
	}

	slug, err := instanceID.GetSlug(true)
	if err != nil {
		return fmt.Errorf("failed to get slug: %w", err)
	}

	wlid := profile.Annotations[helpers.WlidMetadataKey]

	// Update application profile
	if err := a.ContainerProfileStorage.UpdateApplicationProfile(ctx, key, prefix, root, id, slug, wlid, instanceID, profile, creationTimestamp); err != nil {
		return err
	}

	// Update network neighborhood
	if err := a.ContainerProfileStorage.UpdateNetworkNeighborhood(ctx, key, prefix, root, id, slug, wlid, instanceID, profile, creationTimestamp); err != nil {
		return err
	}

	return nil
}

// getAggregatedData computes various data of the aggregated profile.
// A profile status is completed only if all its main containers are completed.
// A profile completion is full only if all its init/main containers are full.
// A profile sync checksum is the checksum of all container checksums.
func (a *ContainerProfileProcessor) getAggregatedData(ctx context.Context, key string, parts map[string]string) (string, string, string) {
	mainContainers := 0
	completed := 0
	full := 0
	var tooLarge bool
	status := helpers.Learning
	completion := helpers.Partial
	hasher := sha256.New()
	// Sort keys to ensure deterministic iteration order for consistent checksum
	keys := make([]string, 0, len(parts))
	for k := range parts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
		defer cpCancel()
		profile, err := a.ContainerProfileStorage.GetContainerProfileMetadata(cpCtx, key)
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
		if profile.Annotations[helpers.StatusMetadataKey] == helpers.TooLarge {
			tooLarge = true
		}
		checksum := profile.Annotations[helpers.SyncChecksumMetadataKey]
		parts[key] = checksum
		hasher.Write([]byte(checksum)) // profile.Parts is sorted so the checksum is consistent
	}
	if completed == mainContainers && mainContainers > 0 {
		status = helpers.Completed
	} else if tooLarge {
		status = helpers.TooLarge
	}
	if full == len(parts) {
		completion = helpers.Full
	}
	hash := hex.EncodeToString(hasher.Sum(nil))
	logger.L().Debug("ContainerProfileProcessor.getAggregatedData - returning", loggerhelpers.String("key", key),
		loggerhelpers.Int("mainContainers", mainContainers), loggerhelpers.Int("completed", completed), loggerhelpers.Int("full", full),
		loggerhelpers.String("status", status), loggerhelpers.String("completion", completion), loggerhelpers.String("hash", hash))
	return status, completion, hash
}

func DeflateContainerProfileSpec(container softwarecomposition.ContainerProfileSpec, sbomSet mapset.Set[string], settings dynamicpathdetector.CollapseSettings) softwarecomposition.ContainerProfileSpec {
	opens, err := dynamicpathdetector.AnalyzeOpens(container.Opens, dynamicpathdetector.NewPathAnalyzerWithConfigs(settings.OpenDynamicThreshold, settings.CollapseConfigs), sbomSet)
	if err != nil {
		logger.L().Debug("ContainerProfileProcessor.deflateContainerProfileSpec - falling back to DeflateStringer for opens", loggerhelpers.Error(err))
		opens = DeflateStringer(container.Opens)
	}
	endpoints := dynamicpathdetector.AnalyzeEndpoints(&container.Endpoints, dynamicpathdetector.NewPathAnalyzerWithConfigs(settings.EndpointDynamicThreshold, settings.CollapseConfigs))
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

func SplitProfileName(profileName string) (name string, tsSuffix string) {
	lastHyphenIndex := strings.LastIndex(profileName, "-")
	if lastHyphenIndex == -1 {
		// No hyphen found, so the whole string is the name, and suffix is empty
		return profileName, ""
	}
	name = profileName[:lastHyphenIndex]
	tsSuffix = profileName[lastHyphenIndex+1:]
	return name, tsSuffix
}

// isKeyNotFoundErr returns true if err indicates the key was not found (already deleted).
// Checks the error chain so wrapped errors from different backends are handled.
func isKeyNotFoundErr(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if storage.IsNotFound(e) || strings.Contains(e.Error(), "key not found") {
			return true
		}
	}
	return storage.IsNotFound(err) || strings.Contains(err.Error(), "key not found")
}
