package file

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Storage kinds for container profile artifacts. ContainerProfileKind is the
// canonical observed CP produced by time-series consolidation. MergedKind is
// the derived "effective" CP — observed plus the user-managed ug- AP/NN
// overlay — written under a parallel key so consumers can prefer it without
// the consolidator ever reading it back. The split exists to preserve a
// canonical observed CP that retracts cleanly when a user edits or deletes
// a ug- CRD (kubescape/storage#315 review).
const (
	ContainerProfileKind       = "containerprofile"
	ContainerProfileMergedKind = "containerprofile-merged"
)

// MergedKeyFor returns the merged-CP storage key corresponding to an
// observed-CP key. Replaces the kind segment "/containerprofile/" with
// "/containerprofile-merged/". The path layout from K8sKeysToPath / ECSKeysToPath
// / HostKeysToPath always places kind at the same segment, so a single replace
// is correct for every host type.
func MergedKeyFor(observedKey string) string {
	return strings.Replace(observedKey, "/"+ContainerProfileKind+"/", "/"+ContainerProfileMergedKind+"/", 1)
}

// ContainerProfileStorageImpl implements ContainerProfileStorage using SQLite as the backend.
type ContainerProfileStorageImpl struct {
	storageImpl *StorageImpl
	pool        *sqlitemigration.Pool
}

// NewContainerProfileStorageImpl creates a new SQLite-backed ContainerProfileStorage.
func NewContainerProfileStorageImpl(storageImpl *StorageImpl, pool *sqlitemigration.Pool) *ContainerProfileStorageImpl {
	return &ContainerProfileStorageImpl{
		storageImpl: storageImpl,
		pool:        pool,
	}
}

var _ ContainerProfileStorage = (*ContainerProfileStorageImpl)(nil)

// WithConnection acquires a connection from the pool and returns a new context
// with the connection embedded, plus a cleanup function to return the connection to the pool.
func (c *ContainerProfileStorageImpl) WithConnection(ctx context.Context) (context.Context, func(), error) {
	conn, err := c.pool.Take(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to take connection from pool: %w", err)
	}
	var cleaned bool
	cleanup := func() {
		if !cleaned {
			cleaned = true
			c.pool.Put(conn)
		}
	}
	return context.WithValue(ctx, connKey, conn), cleanup, nil
}

// BeginTransaction starts a SQLite transaction (savepoint) and returns a function
// to commit or rollback based on the error state.
func (c *ContainerProfileStorageImpl) BeginTransaction(ctx context.Context) (func(*error), error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return sqlitex.Transaction(conn), nil
}

func (c *ContainerProfileStorageImpl) DeleteContainerProfile(ctx context.Context, key string) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return c.storageImpl.delete(ctx, conn, key, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
}

func (c *ContainerProfileStorageImpl) GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	profile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) GetContainerProfileMetadata(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	profile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) GetSbom(ctx context.Context, key string) (softwarecomposition.SBOMSyft, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	sbom := softwarecomposition.SBOMSyft{}
	err := c.storageImpl.GetWithConn(ctx, conn, key, storage.GetOptions{}, &sbom)
	return sbom, err
}

func (c *ContainerProfileStorageImpl) GetStorageImpl() *StorageImpl {
	return c.storageImpl
}

func (c *ContainerProfileStorageImpl) GetTsContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	tsProfile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.get(ctx, conn, key, storage.GetOptions{}, &tsProfile, noLock) // get instead of GetWithConn to bypass locking
	return tsProfile, err
}

func (c *ContainerProfileStorageImpl) SaveContainerProfile(ctx context.Context, key string, profile *softwarecomposition.ContainerProfile) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		return profile, nil, nil
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	// cachedExistingObject is deliberately nil. Passing a non-nil value (even an
	// empty object) tells GuaranteedUpdate to treat it as the current on-disk
	// state and skip the read-from-disk, so its "same serialized contents"
	// short-circuit compares the freshly consolidated profile against an empty
	// object — never equal — and rewrites the observed CP (bumping its
	// ResourceVersion) on every consolidation tick that carries new time-series
	// data, even when the consolidated content is byte-identical to what is
	// already persisted. That spurious RV bump then propagates to the merged CP
	// (whose merged-source-observed-rv annotation tracks observed.ResourceVersion),
	// refreshing it once per node-agent report. profile already carries the
	// persisted CP's identity (ResourceVersion, UID, creationTimestamp,
	// SyncChecksum) from loadOrInitializeProfile, so reading the real current
	// state lets an unchanged consolidation compare equal and skip the write
	// (kubescape/storage#315 review).
	err := c.storageImpl.GuaranteedUpdateWithConn(cpCtx, conn, key, &softwarecomposition.ContainerProfile{},
		true, nil, tryUpdate, nil, "")
	if err != nil {
		return fmt.Errorf("failed to update container profile: %w", err)
	}

	return nil
}

func (c *ContainerProfileStorageImpl) SaveMergedContainerProfile(ctx context.Context, observedKey string, profile *softwarecomposition.ContainerProfile) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	mergedKey := MergedKeyFor(observedKey)

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		// The merged CP is rebuilt from observed.DeepCopy() every tick, so it
		// carries the *observed* CP's identity (ResourceVersion, SyncChecksum,
		// UID, creationTimestamp) rather than this merged key's own. Left as-is
		// that mismatch makes GuaranteedUpdate's "same serialized contents"
		// short-circuit miss every time, rewriting the merged CP — and firing a
		// watch event to node-agent — on every consolidation tick even when the
		// merged content is unchanged. Carry the persisted merged object's
		// identity forward so an unchanged rebuild compares equal and the write
		// is skipped (kubescape/storage#315 review).
		out := profile.DeepCopy()
		if existing, ok := input.(*softwarecomposition.ContainerProfile); ok && existing.ResourceVersion != "" {
			out.ResourceVersion = existing.ResourceVersion
			out.UID = existing.UID
			out.CreationTimestamp = existing.CreationTimestamp
			if cs, set := existing.Annotations[helpers.SyncChecksumMetadataKey]; set {
				if out.Annotations == nil {
					out.Annotations = map[string]string{}
				}
				// Align with the persisted checksum for the equality probe only.
				// If content actually changed the probe fails and saveObject
				// recomputes the real checksum, so this never persists a stale one.
				out.Annotations[helpers.SyncChecksumMetadataKey] = cs
			}
		}
		return out, nil, nil
	}

	cpCtx, cpCancel := context.WithTimeout(ctx, 5*time.Second)
	defer cpCancel()

	// cachedExistingObject is deliberately nil: a non-nil value (even an empty
	// one) tells GuaranteedUpdate to treat it as the current state and skip the
	// read-from-disk, which would make tryUpdate's `input` always empty and the
	// no-op short-circuit never fire. We need the real persisted merged object
	// here so an unchanged rebuild is recognised and the write is skipped.
	if err := c.storageImpl.GuaranteedUpdateWithConn(cpCtx, conn, mergedKey, &softwarecomposition.ContainerProfile{},
		true, nil, tryUpdate, nil, ""); err != nil {
		return fmt.Errorf("failed to update merged container profile: %w", err)
	}
	return nil
}

func (c *ContainerProfileStorageImpl) GetMergedContainerProfile(ctx context.Context, observedKey string) (softwarecomposition.ContainerProfile, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	profile := softwarecomposition.ContainerProfile{}
	err := c.storageImpl.GetWithConn(ctx, conn, MergedKeyFor(observedKey), storage.GetOptions{}, &profile)
	return profile, err
}

func (c *ContainerProfileStorageImpl) DeleteMergedContainerProfile(ctx context.Context, observedKey string) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	mergedKey := MergedKeyFor(observedKey)

	// Lock-free existence probe (ReadMetadata takes no key lock) purely to stay
	// quiet on the common no-merged case — every workload's early ticks before a
	// ug- overlay exists. StorageImpl.delete logs at Error level for a missing
	// key, so we only attempt the delete when the merged artifact actually
	// exists; this removes the need for the caller's separate locked
	// GetMergedContainerProfile probe (the "extra lock") without re-introducing
	// the error-log spam.
	if _, err := ReadMetadata(conn, mergedKey); err != nil {
		if errors.Is(err, ErrMetadataNotFound) {
			return nil // nothing to retract — idempotent
		}
		return fmt.Errorf("probe merged container profile for deletion: %w", err)
	}

	// The delete itself goes through DeleteWithConn so it stays synchronized
	// (write-locked) with concurrent Get/Save on the same key, consistent with
	// GetMergedContainerProfile/SaveMergedContainerProfile — an unsynchronized
	// delete risks SQLite busy / lock contention (kubescape/storage#315 review).
	return c.storageImpl.DeleteWithConn(ctx, conn, mergedKey, &softwarecomposition.ContainerProfile{}, nil, nil, nil, storage.DeleteOptions{})
}

func (c *ContainerProfileStorageImpl) UpdateApplicationProfile(ctx context.Context, key, prefix, root string, id armotypes.ProfileIdentifier, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	id.Name = slug
	apKey := BuildContainerProfileKey(id, "applicationprofiles")
	var apChecksum string

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		output := input.DeepCopyObject()
		ap, ok := output.(*softwarecomposition.ApplicationProfile)
		if !ok {
			return nil, nil, fmt.Errorf("given object is not an ApplicationProfile")
		}

		ap.Name = slug
		if id.HostType == armotypes.HostTypeKubernetes {
			ap.Namespace = id.Namespace
		}
		if ap.CreationTimestamp.IsZero() {
			ap.CreationTimestamp = creationTimestamp
		}
		ap.SchemaVersion = SchemaVersion
		if ap.Parts == nil {
			ap.Parts = map[string]string{}
		}
		ap.Parts[key] = "" // checksum will be updated by getAggregatedData

		status, completion, checksum := ComputeAggregatedData(c, ctx, key, ap.Parts)
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

	apCtx, apCancel := context.WithTimeout(ctx, 5*time.Second)
	defer apCancel()

	err := c.storageImpl.GuaranteedUpdateWithConn(apCtx, conn, apKey, &softwarecomposition.ApplicationProfile{},
		true, nil, tryUpdate, nil, apChecksum)
	if err != nil {
		return fmt.Errorf("failed to update application profile: %w", err)
	}

	return nil
}

func (c *ContainerProfileStorageImpl) UpdateNetworkNeighborhood(ctx context.Context, key, prefix, root string, id armotypes.ProfileIdentifier, slug, wlid string, instanceID interface{ GetStringNoContainer() string }, profile *softwarecomposition.ContainerProfile, creationTimestamp metav1.Time) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)

	id.Name = slug
	nnKey := BuildContainerProfileKey(id, "networkneighborhoods")
	var nnChecksum string

	tryUpdate := func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
		output := input.DeepCopyObject()
		nn, ok := output.(*softwarecomposition.NetworkNeighborhood)
		if !ok {
			return nil, nil, fmt.Errorf("given object is not an NetworkNeighborhood")
		}

		nn.Name = slug
		if id.HostType == armotypes.HostTypeKubernetes {
			nn.Namespace = id.Namespace
		}
		if nn.CreationTimestamp.IsZero() {
			nn.CreationTimestamp = creationTimestamp
		}
		nn.SchemaVersion = SchemaVersion
		if nn.Parts == nil {
			nn.Parts = map[string]string{}
		}
		nn.Parts[key] = "" // checksum will be updated by getAggregatedData

		status, completion, checksum := ComputeAggregatedData(c, ctx, key, nn.Parts)
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

	nnCtx, nnCancel := context.WithTimeout(ctx, 5*time.Second)
	defer nnCancel()

	err := c.storageImpl.GuaranteedUpdateWithConn(nnCtx, conn, nnKey, &softwarecomposition.NetworkNeighborhood{},
		true, nil, tryUpdate, nil, nnChecksum)
	if err != nil {
		return fmt.Errorf("failed to update network neighborhood: %w", err)
	}

	return nil
}

// Time Series Operations

func (c *ContainerProfileStorageImpl) ListTimeSeriesExpired(ctx context.Context, threshold time.Duration) ([]string, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ListTimeSeriesExpired(conn, threshold)
}

func (c *ContainerProfileStorageImpl) ListTimeSeriesWithData(ctx context.Context) ([]string, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ListTimeSeriesWithData(conn)
}

func (c *ContainerProfileStorageImpl) ListTimeSeriesContainers(ctx context.Context, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ListTimeSeriesContainers(conn, key)
}

func (c *ContainerProfileStorageImpl) DeleteTimeSeriesContainerEntries(ctx context.Context, key string) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return DeleteTimeSeriesContainerEntries(conn, key)
}

func (c *ContainerProfileStorageImpl) ReplaceTimeSeriesContainerEntries(ctx context.Context, key, seriesID string, deleteTimeSeries []string, newTimeSeries []softwarecomposition.TimeSeriesContainers) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return ReplaceTimeSeriesContainerEntries(conn, key, seriesID, deleteTimeSeries, newTimeSeries)
}

func (c *ContainerProfileStorageImpl) WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	conn := ctx.Value(connKey).(*sqlite.Conn)
	return WriteTimeSeriesEntry(conn, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData)
}
