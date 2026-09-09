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
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/kubescape/storage/pkg/registry/file/callstack"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/kubescape/storage/pkg/utils"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
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
	// Hooks are test seams; see ConsolidationHooks.
	Hooks ConsolidationHooks
	// seriesOrder returns the order updateProfile processes a key's series in.
	// nil means map iteration order (random); tests override it to force the
	// order, which decides which series a terminal branch leaves unreached.
	seriesOrder func(timeSeries map[string][]softwarecomposition.TimeSeriesContainers) []string
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
		MaxContainerProfileSize: cfg.MaxContainerProfileSize,
		CollapseSettings:        dynamicpathdetector.DefaultCollapseSettings,
		Workers:                 max(1, DefaultPoolSize/4),
	}
}

var _ Processor = (*ContainerProfileProcessor)(nil)

var _ TimeSeriesRowProvider = (*ContainerProfileProcessor)(nil)

// ConsolidationHooks are test seams on the consolidation pass; nil in
// production.
type ConsolidationHooks struct {
	// BeforeProcessedDeletes runs, per key, after the pass has merged the
	// time-series objects and immediately before it deletes them: for a store
	// that stages the deletes (ProcessedDeleteStager) this is inside the
	// tick's transaction window, for the legacy store it is after the commit.
	BeforeProcessedDeletes func(key string)
}

// TimeSeriesRowFor returns the time_series row a TS ContainerProfile create
// records and the base key whose Completed/Full or TooLarge state must refuse
// it; ok=false for a non-TS profile.
func (a *ContainerProfileProcessor) TimeSeriesRowFor(object runtime.Object) (TimeSeriesRow, string, bool) {
	profile, ok := object.(*softwarecomposition.ContainerProfile)
	if !ok {
		return TimeSeriesRow{}, "", false
	}
	seriesID, ok := profile.Annotations[helpers.ReportSeriesIdMetadataKey]
	if !ok {
		return TimeSeriesRow{}, "", false
	}
	// remove the suffix from the name after the last hyphen
	name, tsSuffix := SplitProfileName(profile.Name)
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
	return TimeSeriesRow{
		Kind:                    ContainerProfileKind,
		Namespace:               profile.Namespace,
		Name:                    name,
		SeriesID:                seriesID,
		TsSuffix:                tsSuffix,
		ReportTimestamp:         profile.Annotations[helpers.ReportTimestampMetadataKey],
		Status:                  profile.Annotations[helpers.StatusMetadataKey],
		Completion:              profile.Annotations[helpers.CompletionMetadataKey],
		PreviousReportTimestamp: profile.Annotations[helpers.PreviousReportTimestampMetadataKey],
		HasData:                 true,
	}, BuildContainerProfileKey(id, ContainerProfileKind), true
}

// AfterCreate is called after a TS ContainerProfile is created to store metadata.
func (a *ContainerProfileProcessor) AfterCreate(ctx context.Context, object runtime.Object) error {
	if _, ok := object.(*softwarecomposition.ContainerProfile); !ok {
		return fmt.Errorf("given object is not an ContainerProfile")
	}
	row, _, ok := a.TimeSeriesRowFor(object)
	if !ok {
		// if the container ID annotation is not set, it's not a TS ContainerProfile and we skip it
		return nil
	}
	writer, ok := a.ContainerProfileStorage.(TimeSeriesEntryWriter)
	if !ok {
		return fmt.Errorf("container profile storage %T cannot write time series entries", a.ContainerProfileStorage)
	}
	// add sequence info via storage interface
	err := writer.WriteTimeSeriesEntry(ctx, row.Kind, row.Namespace, row.Name, row.SeriesID, row.TsSuffix, row.ReportTimestamp, row.Status, row.Completion, row.PreviousReportTimestamp, row.HasData)
	if err != nil {
		logger.L().Ctx(ctx).Error("ContainerProfileProcessor.AfterCreate - failed to write time series data for container profile",
			loggerhelpers.Error(err),
			loggerhelpers.String("name", row.Name+"-"+row.TsSuffix),
			loggerhelpers.String("namespace", row.Namespace),
			loggerhelpers.String("completion", row.Completion),
			loggerhelpers.String("seriesID", row.SeriesID),
			loggerhelpers.String("tsSuffix", row.TsSuffix),
			loggerhelpers.Interface("previousReportTimestamp", row.PreviousReportTimestamp),
			loggerhelpers.Interface("reportTimestamp", row.ReportTimestamp),
			loggerhelpers.String("status", row.Status))
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
		existingProfile, err := a.ContainerProfileStorage.GetContainerProfileMetadataNoLock(ctx, key)
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

	// Consolidated (non-TS) profile path: enforce completed-immutability.
	// A direct patch of the consolidated ContainerProfile carries no
	// ReportSeriesId, so it skips the TS branch above. If the stored
	// consolidated profile is already Completed, a regression of the incoming
	// status back to Learning/Ready (helpers.Learning == "ready") must be
	// reverted so the profile cannot leave the completed state. The update
	// still proceeds (no error) with the reverted status. A Completed->Completed
	// amendment (partial->full completion) and TooLarge handling are left
	// untouched.
	{
		id := armotypes.ProfileIdentifier{
			ProfileScope: armotypes.ProfileScope{
				HostType:               a.HostType,
				Cluster:                profile.Annotations[helpers.ClusterMetadataKey],
				Namespace:              profile.Namespace,
				CloudAccountIdentifier: profile.Annotations[helpers.CloudAccountIdentifierMetadataKey],
				Region:                 profile.Annotations[helpers.RegionMetadataKey],
				HostID:                 profile.Annotations[helpers.HostIDMetadataKey],
			},
			Name: profile.Name,
		}
		key := BuildContainerProfileKey(id, "containerprofile")
		// Use the no-lock metadata read: PreSave is invoked from within
		// GuaranteedUpdate, which already holds the write lock for this key, so
		// GetContainerProfileMetadata (which takes a read lock) would self-deadlock.
		// If the consolidated profile does not exist yet (or cannot be loaded),
		// this is a create and there is nothing to guard.
		if existingProfile, err := a.ContainerProfileStorage.GetContainerProfileMetadataNoLock(ctx, key); err == nil {
			if existingProfile.Annotations[helpers.StatusMetadataKey] == helpers.Completed &&
				profile.Annotations[helpers.StatusMetadataKey] == helpers.Learning {
				if profile.Annotations == nil {
					profile.Annotations = make(map[string]string)
				}
				profile.Annotations[helpers.StatusMetadataKey] = helpers.Completed
			}
		} else if !storage.IsNotFound(err) {
			// A genuine read error (not "does not exist yet") means we could
			// not verify whether the consolidated profile is already Completed.
			// Leave the incoming status untouched, but leave a diagnostic trail
			// so a Completed->Learning regression that slips through here is not
			// silent.
			logger.L().Debug("ContainerProfileProcessor.PreSave - failed to check consolidated completed status", loggerhelpers.Error(err), loggerhelpers.String("key", key))
		}
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
		// keyed by the storage kind segment, not the REST resource name:
		// container profiles live under the singular "containerprofile"
		ContainerProfileKind: a.CleanupHandler.ContainerProfileHandlers(),
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
	err := a.consolidateKeyTimeSeriesOnce(ctx, key, expired)
	if errors.Is(err, ErrWriteConflict) {
		// A store whose tick commits atomically (ObjectStore) reports a
		// compare-and-swap conflict when the base or a processed TS object
		// changed between the pass's reads and its commit: re-read and retry
		// once (design §3.7 Phase 3, N=2); a second conflict is next tick's.
		logger.L().Debug("ContainerProfileProcessor.consolidateKeyTimeSeries - write conflict, retrying once", loggerhelpers.String("key", key))
		err = a.consolidateKeyTimeSeriesOnce(ctx, key, expired)
	}
	return err
}

func (a *ContainerProfileProcessor) consolidateKeyTimeSeriesOnce(ctx context.Context, key string, expired bool) error {
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

	// The divergence check, in autocommit BEFORE the pass's transaction (a
	// metadata SELECT as the transaction's first statement would take a read
	// snapshot the first DELETE must upgrade). profile is the payload; the row
	// is what LIST, WATCH and PreSave read. They are written together under
	// Lock(key) by every writer and diverge only through a crash or a failed
	// COMMIT between saveObject's payload rename and the row's commit.
	// A synthesised profile (not found) has empty annotations and its row read
	// is NotFound, so neither arm fires for it.
	meta, merr := a.ContainerProfileStorage.GetContainerProfileMetadataNoLock(ctx, key)
	switch {
	case merr != nil:
		if !isKeyNotFoundErr(merr) {
			// Fail open: the pass proceeds as it always has.
			logger.L().Warning("ContainerProfileProcessor.consolidateKeyTimeSeries - metadata read failed; divergence check skipped",
				loggerhelpers.Error(merr), loggerhelpers.String("key", key))
		}
	case softwarecomposition.IsCompletedFull(profile.Annotations) && !softwarecomposition.IsCompletedFull(meta.Annotations):
		// Payload-ahead: the completing save's rename landed, its COMMIT did
		// not. Without the heal the frozen gate below would reclaim every row
		// while the row stayed Learning for the container's life (PreSave keeps
		// admitting reports, LIST/WATCH never show Full).
		if err := a.ContainerProfileStorage.HealDivergence(ctx, key); err != nil {
			metrics.IncConsolidationHealFailed(healFailureReason(err))
			// This tick fails before the frozen gate; the next tick retries.
			return fmt.Errorf("failed to heal payload/metadata divergence for key %s: %w", key, err)
		}
	case !softwarecomposition.IsCompletedFull(profile.Annotations) && softwarecomposition.IsCompletedFull(meta.Annotations):
		// Metadata-ahead: the row's commit survived and the payload rename was
		// lost (power loss with an un-fsynced directory). Observed, not healed:
		// the payload is the data, and today's merge-and-re-save restores
		// Completed from the row through PreSave's revert.
		metrics.IncConsolidationDivergence(metrics.DivergenceMetadataAhead)
		logger.L().Warning("ContainerProfileProcessor.consolidateKeyTimeSeries - metadata row is Completed/Full but payload is not; merging the payload, PreSave will restore Completed from the row",
			loggerhelpers.String("key", key))
	}

	// frozen is the persisted state the frozen gate in updateProfile will read,
	// captured BEFORE the pass: profile is passed by value below, but its
	// Annotations map is shared by every copy, so the completing tick's
	// SetCompletedStatus stamps Completed/Full onto this copy too. Evaluating
	// the predicate after the pass would turn the slug guard into "never send".
	frozen := softwarecomposition.IsCompletedFull(profile.Annotations)

	processed, deletesStaged, err := a.processTimeSeriesInTransaction(ctx, timeSeries, key, profile, prefix, root, id, expired)
	if err != nil {
		return err
	}

	// Send consolidated slug to channel before deleting processed time series
	// This allows downstream processing even if the ingester dies after consolidation
	// Only send for k8s host type.
	//
	// A frozen tick (the persisted profile was already Completed/Full, so
	// updateProfile reclaimed its rows and merged nothing) sends no slug: a slug
	// means a consolidation happened. The completing tick (persisted Learning,
	// stamped Full by the pass) still sends -- see frozen above.
	if a.HostType == armotypes.HostTypeKubernetes && !frozen {
		if err := a.sendConsolidatedSlugToChannel(ctx, profile, id); err != nil {
			return err
		}
	}

	if !deletesStaged {
		if a.Hooks.BeforeProcessedDeletes != nil {
			a.Hooks.BeforeProcessedDeletes(key)
		}
		if err := a.deleteProcessedTimeSeries(ctx, processed); err != nil {
			return err
		}
	}

	logger.L().Debug("ContainerProfileProcessor.consolidateKeyTimeSeries - finished consolidating data for key", loggerhelpers.String("key", key))
	return nil
}

// sendConsolidatedSlugToChannel calculates the slug from the profile and sends it to the channel
// The slug is calculated from the ContainerProfile
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
				Namespace: id.Namespace,
				Name:      id.Name,
				// A standard REST Create gets a UID via
				// rest.FillObjectMetaSystemFields/uuid.NewUUID(); this path bypasses
				// that (it writes directly through storage.Interface, never through
				// k8s.io/apiserver's generic Create), so a new profile must generate
				// its own or stay permanently UID-less. A UID-less object makes
				// k8s.io/apiserver's generic PATCH handler treat it as nonexistent
				// (vendor/k8s.io/apiserver/pkg/endpoints/handlers/patch.go's hasUID
				// check) and refuse to apply, so kubectl annotate/label/edit and any
				// merge-patch client against it 404s (kubescape/storage#385).
				UID:         uuid.NewUUID(),
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
//
// deletesStaged reports that the processed time-series deletes were handed to
// the store inside the transaction (ProcessedDeleteStager) and must not be
// issued again by the caller.
func (a *ContainerProfileProcessor) processTimeSeriesInTransaction(ctx context.Context,
	timeSeries map[string][]softwarecomposition.TimeSeriesContainers, key string,
	profile softwarecomposition.ContainerProfile, prefix, root string, id armotypes.ProfileIdentifier, expired bool) (processed []string, deletesStaged bool, err error) {

	endFn, err := a.ContainerProfileStorage.BeginTransaction(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("failed to begin nested transaction: %w", err)
	}
	// Registered before endFn so it runs after it (LIFO) and wraps a failed
	// COMMIT the same way as a failed updateProfile. A CAS conflict discovered
	// by endFn's COMMIT (not just one raised by updateProfile) passes through
	// unwrapped so the caller's errors.Is(err, ErrWriteConflict) retry still
	// fires.
	defer func() {
		if err != nil {
			processed = nil
			if !errors.Is(err, ErrWriteConflict) {
				err = fmt.Errorf("failed to process time series data for key %s (transaction rolled back): %w", key, err)
			}
		}
	}()
	// endFn must be deferred DIRECTLY: it recovers a panic raised inside
	// updateProfile, rolls the transaction back and re-panics. Called inline (or
	// from a closure, where its recover() sees nothing) a panic escaped with the
	// transaction open, leaving SQLite's write lock held by a connection that
	// went back to the pool with nobody left to end it.
	defer endFn(&err)

	processed, err = a.updateProfile(ctx, timeSeries, key, profile, prefix, root, id, expired)
	if err == nil {
		if st, ok := a.ContainerProfileStorage.(ProcessedDeleteStager); ok && st.StagesProcessedDeletes() {
			if a.Hooks.BeforeProcessedDeletes != nil {
				a.Hooks.BeforeProcessedDeletes(key)
			}
			err = a.deleteProcessedTimeSeries(ctx, processed)
			deletesStaged = true
		}
	}
	return processed, deletesStaged, err
}

// deleteProcessedTimeSeries removes processed time series profiles from storage.
// Treats "key not found" as success (idempotent delete): the profile may already have been
// deleted by a concurrent consolidation run for the same customer/cluster.
func (a *ContainerProfileProcessor) deleteProcessedTimeSeries(ctx context.Context, processed []string) error {
	for _, tsKey := range processed {
		err := a.deleteContainerProfileArbitrated(ctx, tsKey)
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

// deleteContainerProfileArbitrated deletes key's processed TS profile through
// the same per-key shard a live Create/GuaranteedUpdate for that key would
// use (see singleWriter.runOnShard's doc comment), rather than
// DeleteContainerProfile's raw pool connection.
//
// This was the single biggest lever found while tracing the residual
// component-tests write-timeout flakiness: unlike SaveContainerProfile
// (already routed through guaranteedUpdateSingleWriter, priorityLow) and
// Create/GuaranteedUpdate's own commits (routed through the 8-shard system),
// this delete's raw SQLite transaction had no way to yield to -- or be
// yielded by -- a live commit, so it could collide directly for SQLite's own
// lock. A local repro (containerprofile_load_test.go, LOAD_CONSOLIDATORS=1)
// showed one such collision alone blocking the loser for the full
// busy-timeout, and under a sustained write burst plus periodic consolidation
// this collapsed write throughput by two orders of magnitude.
//
// The shard-routed fn is storageImpl.deleteLocked, NOT storageImpl.delete
// (unlike wrapping ConsolidateTimeSeries's whole per-key unit, which
// self-deadlocks via its own SaveContainerProfile call for the SAME
// key/shard). deleteLocked is a leaf write that takes no lock of its own,
// calls back into nothing shard- or lock-routed, and blocks on nothing but
// conn. delete additionally ends in watchDispatcher.Deleted, whose send
// blocks until a stalled watch client resumes reading or its ctx ends --
// run inside fn that would freeze the whole shard for as long as a remote
// client chooses. So the event is dispatched here, on the caller, after the
// shard turn (connection and per-key lock) is released, exactly as
// createSingleWriter and guaranteedUpdateSingleWriter do.
func (a *ContainerProfileProcessor) deleteContainerProfileArbitrated(ctx context.Context, key string) error {
	csi, ok := a.ContainerProfileStorage.(*ContainerProfileStorageImpl)
	if !ok || !singleWriterEnabled {
		return a.ContainerProfileStorage.DeleteContainerProfile(ctx, key)
	}
	metaOut := &softwarecomposition.ContainerProfile{}
	err := csi.storageImpl.ensureWriter().runOnShard(ctx, key, priorityLow, func(conn *sqlite.Conn) error {
		return csi.storageImpl.deleteLocked(ctx, conn, key, metaOut)
	})
	if err != nil {
		return err
	}
	csi.storageImpl.watchDispatcher.Deleted(key, metaOut)
	return nil
}

func (a *ContainerProfileProcessor) updateProfile(ctx context.Context, timeSeries map[string][]softwarecomposition.TimeSeriesContainers, key string, profile softwarecomposition.ContainerProfile, prefix, root string, id armotypes.ProfileIdentifier, expired bool) ([]string, error) {
	var processed []string

	// The frozen gate: once a profile is Completed/Full nothing updates it.
	// profile is the PERSISTED payload as loadOrInitializeProfile read it (a
	// synthesised one has empty annotations and never qualifies), never the
	// in-memory copy the loop below is about to stamp, so the tick that makes
	// a profile Full always passes. A late series (another replica, a row that
	// landed after the completing pass, a row admitted by PreSave's unlocked
	// read) is reclaimed unmerged, with its objects: no TS object is read,
	// nothing is merged, nothing is saved. Replace's delete is
	// (seriesID, tsSuffix IN listed)-scoped, so a row landing after this pass's
	// list is untouched and reclaimed on the next tick. Applies on the expired
	// path too.
	if softwarecomposition.IsCompletedFull(profile.Annotations) {
		var rows, objects int
		for seriesID, series := range timeSeries {
			suffixes := make([]string, 0, len(series))
			for _, ts := range series {
				suffixes = append(suffixes, ts.TsSuffix)
				if ts.HasData {
					processed = append(processed, key+"-"+ts.TsSuffix)
					objects++
				}
			}
			rows += len(suffixes)
			if err := a.ContainerProfileStorage.ReplaceTimeSeriesContainerEntries(ctx, key, seriesID, suffixes, nil); err != nil {
				return nil, fmt.Errorf("failed to reclaim time series of a completed profile: %w", err)
			}
		}
		metrics.IncConsolidationFrozenReclaimed(metrics.FrozenReclaimedRow, rows)
		metrics.IncConsolidationFrozenReclaimed(metrics.FrozenReclaimedObject, objects)
		logger.L().Warning("ContainerProfileProcessor.updateProfile - profile is Completed/Full; reclaiming late time series unmerged",
			loggerhelpers.String("key", key), loggerhelpers.Int("series", len(timeSeries)),
			loggerhelpers.Int("rows", rows), loggerhelpers.Int("objects", objects))
		return processed, nil
	}

	creationTimestamp := metav1.Now()
	var newData bool

	order := a.seriesOrder
	if order == nil {
		order = func(timeSeries map[string][]softwarecomposition.TimeSeriesContainers) []string {
			ids := make([]string, 0, len(timeSeries))
			for seriesID := range timeSeries {
				ids = append(ids, seriesID)
			}
			return ids
		}
	}
	// Process each time series
	for _, seriesID := range order(timeSeries) {
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
		// so the observed save has no target. INV-PROCESSED: a tsKey is returned
		// only when the profile it was merged into was persisted by this pass
		// (or reclaimed unmerged by the frozen gate); returning the merged keys
		// here would delete their objects while the merge itself is lost.
		logger.L().Debug("ContainerProfileProcessor.updateProfile - skip saving invalid profile", loggerhelpers.String("key", key), loggerhelpers.Interface("profile", profile))
		return nil, nil
	}

	// Persist the canonical observed CP only when time-series consolidation
	// produced new data this tick. The observed CP is the time-series-only
	// view (kubescape/storage#315 review).
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

	return processed, nil
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
	deleteTimeSeries, processed, kept, hasNewData := a.mergeTimeSeriesData(ctx, timeSeries[seriesID], key, profile)
	result.processed = processed
	result.hasNewData = hasNewData
	if len(kept) == 0 {
		// Every row of the series took the transient-read arm: nothing was
		// merged and nothing may be written. updateProfileStatus indexes
		// newTimeSeries[0] unconditionally, so this guard is what keeps an
		// all-transient series from panicking inside the open transaction.
		return result, nil
	}

	// Consolidate continuous time series entries. The input is kept, not
	// timeSeries[seriesID]: a row whose object read failed transiently must not
	// be collapsed across, or the chain forks around it on every later tick.
	newTimeSeries := a.consolidateContinuousTimeSeries(kept, creationTimestamp)

	// Update profile status based on time series state
	newTimeSeries, skipFurtherProcessing := a.updateProfileStatus(key, seriesID, profile, newTimeSeries, expired)
	result.skipFurtherProcessing = skipFurtherProcessing

	// Write consolidated data back to database. This is the ONLY time_series
	// delete on the consolidation path, and it is scoped to this series'
	// listed suffixes: on a terminal branch newTimeSeries is empty, so this
	// deletes exactly the rows the pass read and inserts nothing. Rows of a
	// series the pass never reached, or that landed after the list, survive
	// to the next tick, where the frozen gate reclaims them with their objects.
	if err := a.ContainerProfileStorage.ReplaceTimeSeriesContainerEntries(ctx, key, seriesID, deleteTimeSeries, newTimeSeries); err != nil {
		return result, fmt.Errorf("failed to replace consolidated time series data: %w", err)
	}

	return result, nil
}

// mergeTimeSeriesData merges time series data into the profile.
//
// kept is the input minus the rows whose object read failed transiently (a
// non-NotFound error), order preserved, carrying the loop's HasData=false
// mutations. Such a row is neither deleted (its suffix is not in deleteList)
// nor consolidated across (it is not in kept): it stays in the table with
// HasData=true and is retried on the next tick. Every other arm is unchanged.
func (a *ContainerProfileProcessor) mergeTimeSeriesData(ctx context.Context,
	timeSeriesContainers []softwarecomposition.TimeSeriesContainers, key string, profile *softwarecomposition.ContainerProfile) (deleteList []string, processed []string, kept []softwarecomposition.TimeSeriesContainers, hasNewData bool) {

	kept = make([]softwarecomposition.TimeSeriesContainers, 0, len(timeSeriesContainers))
	for k, ts := range timeSeriesContainers {
		if ts.HasData {
			// Load TS profile from disk
			tsKey := key + "-" + ts.TsSuffix
			tsProfile, err := a.ContainerProfileStorage.GetTsContainerProfile(ctx, tsKey)

			switch {
			case storage.IsNotFound(err):
				timeSeriesContainers[k].HasData = false
			case err != nil:
				logger.L().Warning("ContainerProfileProcessor.mergeTimeSeriesData - failed to get ts profile; row left in place for retry on the next tick",
					loggerhelpers.Error(err), loggerhelpers.String("tsKey", tsKey))
				continue
			default:
				hasNewData = true
				mergeContainerProfileTS(profile, &tsProfile)
				timeSeriesContainers[k].HasData = false
				processed = append(processed, tsKey)
			}
		}
		deleteList = append(deleteList, ts.TsSuffix)
		kept = append(kept, timeSeriesContainers[k])
	}

	return deleteList, processed, kept, hasNewData
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
		switch {
		case timeSeries[j].PreviousReportTimestamp == timeSeries[i+1].ReportTimestamp:
			timeSeries[j].PreviousReportTimestamp = timeSeries[i+1].PreviousReportTimestamp
		case timeSeries[j].ReportTimestamp == timeSeries[i+1].ReportTimestamp &&
			timeSeries[j].PreviousReportTimestamp == timeSeries[i+1].PreviousReportTimestamp:
			// same logical report split across two rows (e.g. a chunk too large for the
			// queue got resent as two halves) - not a new link in the chain, so don't fork.
		default:
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
// It is a pure function of its arguments: it executes no SQL (the caller's
// Replace is the only time_series write on this path), which is what lets a
// terminal branch leave the rows of an unreached series untouched instead of
// deleting them unmerged with their objects orphaned.
//
// When expired=true, the profile is marked as Completed/Partial instead of Learning,
// unless it's already Completed/Full (safeguard). This ensures expired time series
// don't remain in Learning state indefinitely.
//
// Returns true if further processing should be skipped (e.g., profile is fully completed).
func (a *ContainerProfileProcessor) updateProfileStatus(key, seriesID string,
	profile *softwarecomposition.ContainerProfile, newTimeSeries []softwarecomposition.TimeSeriesContainers, expired bool) ([]softwarecomposition.TimeSeriesContainers, bool) {

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
			return newTimeSeries[:0], true
		}

		// Otherwise, mark it as Completed/Partial (unless already Completed/Full)
		if !softwarecomposition.IsCompletedFull(profile.Annotations) {
			profile.Annotations[helpers.StatusMetadataKey] = helpers.Completed
			profile.Annotations[helpers.CompletionMetadataKey] = helpers.Partial
		}
		return newTimeSeries[:0], false
	}

	// Normal active (non-expired) flow below:
	// An aggregated series is removed only if it has one element, no previous report timestamp, and is completed or failed
	if len(newTimeSeries) != 1 || !isZeroTime(newTimeSeries[0].PreviousReportTimestamp) {
		profile.SetLearningStatus(newTimeSeries[0]) // series is missing some TS entries
		return newTimeSeries, false
	}

	switch newTimeSeries[0].Status {
	case helpers.Completed:
		// Safeguard: if already fully completed, keep it that way
		if profile.SetCompletedStatus(newTimeSeries[0]) {
			logger.L().Debug("ContainerProfileProcessor.updateProfileStatus - profile is completed/full, skipping further processing",
				loggerhelpers.String("key", key), loggerhelpers.String("seriesID", seriesID))
			return newTimeSeries[:0], true
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

	return newTimeSeries, false
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
		profile, err := a.ContainerProfileStorage.GetContainerProfileMetadataNoLock(cpCtx, key)
		cpCancel()
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
		Ingress: deflateNetworkNeighbors(container.Ingress, settings),
		Egress:  deflateNetworkNeighbors(container.Egress, settings),
		// User-authored multi-container documents carry per-subtype container
		// groups. Rebuilding the spec without them silently strips every group
		// on write (learned profiles leave them empty, so this only affects
		// authored documents). Deflate each section's surfaces with the same
		// treatment as the flat spec.
		Containers:          deflateContainerProfileContainers(container.Containers, sbomSet, settings),
		InitContainers:      deflateContainerProfileContainers(container.InitContainers, sbomSet, settings),
		EphemeralContainers: deflateContainerProfileContainers(container.EphemeralContainers, sbomSet, settings),
	}
}

// deflateContainerProfileContainers applies the per-surface deflation to each
// container section of a user-authored multi-container document.
func deflateContainerProfileContainers(sections []softwarecomposition.ContainerProfileContainer, sbomSet mapset.Set[string], settings dynamicpathdetector.CollapseSettings) []softwarecomposition.ContainerProfileContainer {
	if len(sections) == 0 {
		return nil
	}
	out := make([]softwarecomposition.ContainerProfileContainer, 0, len(sections))
	for _, s := range sections {
		opens, err := dynamicpathdetector.AnalyzeOpens(s.Opens, dynamicpathdetector.NewPathAnalyzerWithConfigs(settings.OpenDynamicThreshold, settings.CollapseConfigs), sbomSet)
		if err != nil {
			opens = DeflateStringer(s.Opens)
		}
		endpoints := dynamicpathdetector.AnalyzeEndpoints(&s.Endpoints, dynamicpathdetector.NewPathAnalyzerWithConfigs(settings.EndpointDynamicThreshold, settings.CollapseConfigs))
		out = append(out, softwarecomposition.ContainerProfileContainer{
			Name:                 s.Name,
			Capabilities:         DeflateSortString(s.Capabilities),
			Execs:                DeflateStringer(s.Execs),
			Opens:                opens,
			Syscalls:             DeflateSortString(s.Syscalls),
			SeccompProfile:       s.SeccompProfile,
			Endpoints:            endpoints,
			ImageTag:             s.ImageTag,
			ImageID:              s.ImageID,
			PolicyByRuleId:       DeflateRulePolicies(s.PolicyByRuleId),
			IdentifiedCallStacks: callstack.UnifyIdentifiedCallStacks(s.IdentifiedCallStacks),
			Ingress:              deflateNetworkNeighbors(s.Ingress, settings),
			Egress:               deflateNetworkNeighbors(s.Egress, settings),
		})
	}
	return out
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
