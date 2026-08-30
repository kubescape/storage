package containerprofile

import (
	"context"
	"strings"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// Differential test suite: constructs BOTH the OLD (genericregistry.Store-
// based, etcd.go's NewREST) and the NEW (CustomREST, custom_rest.go)
// rest.Storage implementations and runs identical operation sequences
// against both, asserting identical externally-observable results. This is
// the from-scratch differential suite for Phase 4's third migrated resource
// (see .omc/plans/storage-locking-rewrite.md's "Phase 4 scoping"), modeled
// on knownservers'/openvulnerabilityexchange's suites, plus cases unique to
// this resource: the singular/plural qualified-resource inversion (see
// custom_rest.go's doc comment), the completed-profile immutability guard
// and NetworkNeighbor validation in strategy.go, and the consolidation
// processor's PreSave/AfterCreate hooks (containerprofile_processor.go),
// which both implementations inherit identically because they call
// storage.Interface.Create/GuaranteedUpdate on the SAME
// containerProfileStorageImpl instance -- the hooks live inside StorageImpl,
// not either REST layer.

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sch := runtime.NewScheme()
	install.Install(sch)
	return sch
}

// newTestStorage builds a StorageImpl wired with a real ContainerProfileProcessor
// (mirroring pkg/apiserver/apiserver.go's containerProfileStorageImpl
// construction) rather than the plain file.NewStorageImpl the first two
// migrated resources use -- containerprofile's REST wiring is passed this
// non-default storage.Interface (see apiserver.go's ep(containerprofile.NewREST,
// containerProfileStorageImpl)), so the differential harness must match that
// to exercise the same processor hooks either implementation would see in
// production.
func newTestStorage(t *testing.T) (storage.Interface, *sqlitemigration.Pool) {
	t.Helper()
	fs := afero.NewMemMapFs()
	pool := file.NewTestPool(t.TempDir())
	t.Cleanup(func() { _ = pool.Close() })
	processor := file.NewContainerProfileProcessor(config.Config{
		DefaultNamespace:        "kubescape",
		MaxContainerProfileSize: 40000,
	}, nil)
	// NewContainerProfileProcessor hardcodes Interval to 30s regardless of
	// config, and SetStorage (called by NewStorageImplWithCollector below)
	// unconditionally starts a background maintenance goroutine whenever
	// Interval > 0 -- which immediately calls the (here nil) CleanupHandler
	// and panics. That goroutine is unrelated to what this differential
	// suite exercises (REST-layer behavior), so disable it for the test
	// harness rather than construct a real ResourcesFetcher/CleanupHandler.
	processor.Interval = 0
	return file.NewStorageImplWithCollector(fs, file.DefaultStorageRoot, pool, nil, newTestScheme(t), processor), pool
}

func newOptsGetter() *options.StorageFactoryRestOptionsFactory {
	return &options.StorageFactoryRestOptionsFactory{StorageFactory: &options.SimpleStorageFactory{}}
}

func testContext(namespace string) context.Context {
	return genericapirequest.WithNamespace(genericapirequest.NewContext(), namespace)
}

// harness holds an OLD and a NEW rest.Storage, each backed by its own,
// independent StorageImpl instance -- so operations against one never
// observe state from the other.
type harness struct {
	oldREST rest.StandardStorage
	newREST rest.StandardStorage
	oldPool *sqlitemigration.Pool
	newPool *sqlitemigration.Pool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sch := newTestScheme(t)
	optsGetter := newOptsGetter()

	oldStorage, oldPool := newTestStorage(t)
	oldREST, err := NewREST(sch, oldStorage, optsGetter)
	require.NoError(t, err)

	newStorage, newPool := newTestStorage(t)
	newREST, err := NewCustomREST(sch, newStorage, optsGetter)
	require.NoError(t, err)

	return &harness{oldREST: oldREST, newREST: newREST, oldPool: oldPool, newPool: newPool}
}

// newSharedHarness holds an OLD and a NEW rest.Storage backed by the SAME
// StorageImpl/pool instance -- used specifically to prove both
// implementations derive identical on-disk keys for the same
// namespace+name, despite containerprofile's singular/plural
// qualified-resource inversion.
func newSharedHarness(t *testing.T) *harness {
	t.Helper()
	sch := newTestScheme(t)
	optsGetter := newOptsGetter()
	shared, pool := newTestStorage(t)

	oldREST, err := NewREST(sch, shared, optsGetter)
	require.NoError(t, err)

	newREST, err := NewCustomREST(sch, shared, optsGetter)
	require.NoError(t, err)

	return &harness{oldREST: oldREST, newREST: newREST, oldPool: pool, newPool: pool}
}

// normalize strips instance-specific fields (UID, CreationTimestamp, and the
// sync-checksum annotation, which is a hash of the full object including its
// UID) that are legitimately different between two independently-created
// objects, while asserting those fields were themselves actually set
// (checklist item 1).
func normalize(t *testing.T, obj runtime.Object) *softwarecomposition.ContainerProfile {
	t.Helper()
	cp, ok := obj.(*softwarecomposition.ContainerProfile)
	require.True(t, ok, "expected *softwarecomposition.ContainerProfile, got %T", obj)
	require.NotEmpty(t, cp.UID, "UID must be set")
	require.False(t, cp.CreationTimestamp.IsZero(), "creationTimestamp must be set")
	out := cp.DeepCopy()
	out.UID = ""
	out.CreationTimestamp = metav1.Time{}
	if out.Annotations != nil {
		delete(out.Annotations, helpersv1.SyncChecksumMetadataKey)
		if len(out.Annotations) == 0 {
			out.Annotations = nil
		}
	}
	return out
}

func assertEqualDeleteStatus(t *testing.T, oldOut, newOut runtime.Object, name string) {
	t.Helper()
	oldStatus, ok := oldOut.(*metav1.Status)
	require.True(t, ok, "old: expected *metav1.Status, got %T", oldOut)
	newStatus, ok := newOut.(*metav1.Status)
	require.True(t, ok, "new: expected *metav1.Status, got %T", newOut)

	assert.Equal(t, metav1.StatusSuccess, oldStatus.Status)
	assert.Equal(t, metav1.StatusSuccess, newStatus.Status)
	require.NotNil(t, oldStatus.Details)
	require.NotNil(t, newStatus.Details)
	assert.Equal(t, name, oldStatus.Details.Name)
	assert.Equal(t, name, newStatus.Details.Name)
	assert.NotEmpty(t, oldStatus.Details.UID)
	assert.NotEmpty(t, newStatus.Details.UID)
	assert.Equal(t, oldStatus.Details.Group, newStatus.Details.Group)
	assert.Equal(t, oldStatus.Details.Kind, newStatus.Details.Kind)
}

func profileNamed(name string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func mustCreate(t *testing.T, ctx context.Context, r rest.StandardStorage, obj *softwarecomposition.ContainerProfile) runtime.Object {
	t.Helper()
	out, err := r.Create(ctx, obj.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	return out
}

// --- Create ---

func TestDifferential_CreateExplicitName(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	cp := profileNamed("cp-a")

	oldOut := mustCreate(t, ctx, h.oldREST, cp)
	newOut := mustCreate(t, ctx, h.newREST, cp)

	assert.Equal(t, normalize(t, oldOut), normalize(t, newOut))

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)
	assert.NotEmpty(t, oldMeta.GetResourceVersion())
	assert.NotEmpty(t, newMeta.GetResourceVersion())
	assert.Equal(t, oldMeta.GetResourceVersion(), newMeta.GetResourceVersion())
	assert.Equal(t, "ns1", oldMeta.GetNamespace())
	assert.Equal(t, "ns1", newMeta.GetNamespace())

	// Creating again with the same explicit name must fail identically (AlreadyExists).
	_, err = h.oldREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "old: %v", err)
	_, err = h.newREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "new: %v", err)
}

func TestDifferential_CreateGenerateName(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	cp := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "cp-"},
	}

	oldOut := mustCreate(t, ctx, h.oldREST, cp)
	newOut := mustCreate(t, ctx, h.newREST, cp)

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(oldMeta.GetName(), "cp-"), "old name: %s", oldMeta.GetName())
	assert.True(t, strings.HasPrefix(newMeta.GetName(), "cp-"), "new name: %s", newMeta.GetName())
	assert.NotEmpty(t, oldMeta.GetUID())
	assert.NotEmpty(t, newMeta.GetUID())

	oldOut2 := mustCreate(t, ctx, h.oldREST, cp)
	newOut2 := mustCreate(t, ctx, h.newREST, cp)
	oldMeta2, _ := meta.Accessor(oldOut2)
	newMeta2, _ := meta.Accessor(newOut2)
	assert.NotEqual(t, oldMeta.GetName(), oldMeta2.GetName())
	assert.NotEqual(t, newMeta.GetName(), newMeta2.GetName())
}

// --- Strategy validation ---

// TestDifferential_CompletedProfileImmutableOnUpdate exercises
// ContainerProfileStrategy.PrepareForUpdate's completed-profile immutability
// guard (strategy.go): once a profile carries status=Completed and
// completion=Full, any update attempt is silently reset to the stored
// object rather than applied. Both implementations invoke
// rest.BeforeUpdate -> strategy.PrepareForUpdate at the same point, so this
// is exercised identically.
func TestDifferential_CompletedProfileImmutableOnUpdate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	completed := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cp-completed",
			Annotations: map[string]string{
				helpersv1.CompletionMetadataKey: helpersv1.Full,
				helpersv1.StatusMetadataKey:     helpersv1.Completed,
			},
		},
	}

	oldCreated := mustCreate(t, ctx, h.oldREST, completed).(*softwarecomposition.ContainerProfile)
	newCreated := mustCreate(t, ctx, h.newREST, completed).(*softwarecomposition.ContainerProfile)

	attemptOld := oldCreated.DeepCopy()
	attemptOld.Spec.Capabilities = []string{"NET_ADMIN"}
	oldOut, _, err := h.oldREST.Update(ctx, "cp-completed", rest.DefaultUpdatedObjectInfo(attemptOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err, "old: the guard resets the update rather than erroring")

	attemptNew := newCreated.DeepCopy()
	attemptNew.Spec.Capabilities = []string{"NET_ADMIN"}
	newOut, _, err := h.newREST.Update(ctx, "cp-completed", rest.DefaultUpdatedObjectInfo(attemptNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err, "new: the guard resets the update rather than erroring")

	assert.Empty(t, oldOut.(*softwarecomposition.ContainerProfile).Spec.Capabilities, "old: update to a completed profile must be silently reset")
	assert.Empty(t, newOut.(*softwarecomposition.ContainerProfile).Spec.Capabilities, "new: update to a completed profile must be silently reset")
	assert.Equal(t, normalize(t, oldOut), normalize(t, newOut))

	// Fresh reads confirm the reset was actually persisted, not just returned.
	oldFresh, err := h.oldREST.Get(ctx, "cp-completed", &metav1.GetOptions{})
	require.NoError(t, err)
	newFresh, err := h.newREST.Get(ctx, "cp-completed", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, oldFresh.(*softwarecomposition.ContainerProfile).Spec.Capabilities)
	assert.Empty(t, newFresh.(*softwarecomposition.ContainerProfile).Spec.Capabilities)
}

// TestDifferential_NetworkNeighborInvalidEntryRejected exercises
// validateNetworkProfileEntries (strategy.go), walking Ingress/Egress
// NetworkNeighbor IP/DNS entries against networkmatch's wildcard grammar.
func TestDifferential_NetworkNeighborInvalidEntryRejected(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	cp := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "cp-badneighbor"},
		Spec: softwarecomposition.ContainerProfileSpec{
			Ingress: []softwarecomposition.NetworkNeighbor{
				{IPAddresses: []string{"not-a-valid-ip-or-wildcard!!"}},
			},
		},
	}

	_, oldErr := h.oldREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.Error(t, oldErr)
	assert.True(t, apierrors.IsInvalid(oldErr), "old: %v", oldErr)

	_, newErr := h.newREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.Error(t, newErr)
	assert.True(t, apierrors.IsInvalid(newErr), "new: %v", newErr)

	_, err := h.oldREST.Get(ctx, "cp-badneighbor", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: rejected create must not persist")
	_, err = h.newREST.Get(ctx, "cp-badneighbor", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: rejected create must not persist")
}

// --- Consolidation processor ---

// TestDifferential_ConsolidationProcessorHooksFireIdentically proves the
// containerprofile_processor.go PreSave/AfterCreate hooks -- which live
// inside StorageImpl.Create, not either REST layer -- fire identically for
// both implementations, since both call storage.Interface.Create on their
// own containerProfileStorageImpl-equivalent instance. A time-series-annotated
// Create (carrying ReportSeriesIdMetadataKey) triggers AfterCreate's
// WriteTimeSeriesEntry, a raw SQL insert independent of Create/GuaranteedUpdate;
// this is observable via ListTimeSeriesWithData on the underlying pool.
func TestDifferential_ConsolidationProcessorHooksFireIdentically(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("kubescape")
	tsProfile := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cp-ts-1700000000",
			Annotations: map[string]string{
				helpersv1.ReportSeriesIdMetadataKey:   "series-1",
				helpersv1.ReportTimestampMetadataKey:  "1700000000",
				helpersv1.CompletionMetadataKey:       helpersv1.Partial,
				helpersv1.StatusMetadataKey:           helpersv1.Learning,
			},
		},
	}

	_, err := h.oldREST.Create(ctx, tsProfile.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = h.newREST.Create(ctx, tsProfile.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)

	oldConn, err := h.oldPool.Take(ctx)
	require.NoError(t, err)
	defer h.oldPool.Put(oldConn)
	oldSeries, err := file.ListTimeSeriesWithData(oldConn)
	require.NoError(t, err)

	newConn, err := h.newPool.Take(ctx)
	require.NoError(t, err)
	defer h.newPool.Put(newConn)
	newSeries, err := file.ListTimeSeriesWithData(newConn)
	require.NoError(t, err)

	require.NotEmpty(t, oldSeries, "old: AfterCreate's time-series write must have happened")
	require.NotEmpty(t, newSeries, "new: AfterCreate's time-series write must have happened")
	assert.Equal(t, oldSeries, newSeries, "both implementations must produce identical time-series storage keys")
}

// --- Namespace scoping ---

func TestDifferential_NamespaceIsolation(t *testing.T) {
	h := newHarness(t)
	ctxA := testContext("ns-a")
	ctxB := testContext("ns-b")

	mustCreate(t, ctxA, h.oldREST, profileNamed("shared-name"))
	mustCreate(t, ctxB, h.oldREST, profileNamed("shared-name"))
	mustCreate(t, ctxA, h.newREST, profileNamed("shared-name"))
	mustCreate(t, ctxB, h.newREST, profileNamed("shared-name"))

	oldList, err := h.oldREST.List(ctxA, &metainternalversion.ListOptions{})
	require.NoError(t, err)
	oldItems, err := meta.ExtractList(oldList)
	require.NoError(t, err)
	assert.Len(t, oldItems, 1)

	newList, err := h.newREST.List(ctxA, &metainternalversion.ListOptions{})
	require.NoError(t, err)
	newItems, err := meta.ExtractList(newList)
	require.NoError(t, err)
	assert.Len(t, newItems, 1)
}

// TestDifferential_KeyDerivationMatches verifies -- against a SHARED
// StorageImpl -- that both implementations derive identical on-disk keys for
// the same namespace+name, despite containerprofile's singular/plural
// qualified-resource inversion (see custom_rest.go's doc comment): a Create
// through the OLD implementation must be readable through the NEW
// implementation and vice versa.
func TestDifferential_KeyDerivationMatches(t *testing.T) {
	h := newSharedHarness(t)
	ctx := testContext("shared-ns")

	mustCreate(t, ctx, h.oldREST, profileNamed("via-old"))
	viaNew, err := h.newREST.Get(ctx, "via-old", &metav1.GetOptions{})
	require.NoError(t, err, "NEW implementation must read a key written by the OLD implementation for the same namespace+name")
	assert.Equal(t, "via-old", viaNew.(*softwarecomposition.ContainerProfile).Name)

	mustCreate(t, ctx, h.newREST, profileNamed("via-new"))
	viaOld, err := h.oldREST.Get(ctx, "via-new", &metav1.GetOptions{})
	require.NoError(t, err, "OLD implementation must read a key written by the NEW implementation for the same namespace+name")
	assert.Equal(t, "via-new", viaOld.(*softwarecomposition.ContainerProfile).Name)
}

// --- Update ---

func TestDifferential_UpdateResourceVersion(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	base := profileNamed("srv")

	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.ContainerProfile)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.ContainerProfile)

	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec.ImageTag = "v2"
	oldUpdated, _, err := h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec.ImageTag = "v2"
	newUpdated, _, err := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	assert.Equal(t, normalize(t, oldUpdated), normalize(t, newUpdated))

	staleOld := oldCreated.DeepCopy()
	_, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(staleOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsConflict(err), "old: %v", err)

	staleNew := newCreated.DeepCopy()
	_, _, err = h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(staleNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsConflict(err), "new: %v", err)

	noRVOld := oldCreated.DeepCopy()
	noRVOld.ResourceVersion = ""
	_, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(noRVOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsInvalid(err), "old: %v", err)

	noRVNew := newCreated.DeepCopy()
	noRVNew.ResourceVersion = ""
	_, _, err = h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(noRVNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsInvalid(err), "new: %v", err)
}

func TestDifferential_UpdateCarriesOverProtectedFields(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	base := profileNamed("srv2")
	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldFetched, err := h.oldREST.Get(ctx, "srv2", &metav1.GetOptions{})
	require.NoError(t, err)
	newFetched, err := h.newREST.Get(ctx, "srv2", &metav1.GetOptions{})
	require.NoError(t, err)
	oldCreated2 := oldFetched.(*softwarecomposition.ContainerProfile)
	newCreated2 := newFetched.(*softwarecomposition.ContainerProfile)

	attemptOld2 := oldCreated2.DeepCopy()
	attemptOld2.CreationTimestamp = metav1.NewTime(time.Now().Add(24 * time.Hour))
	oldResult2, _, err := h.oldREST.Update(ctx, "srv2", rest.DefaultUpdatedObjectInfo(attemptOld2), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	attemptNew2 := newCreated2.DeepCopy()
	attemptNew2.CreationTimestamp = metav1.NewTime(time.Now().Add(24 * time.Hour))
	newResult2, _, err := h.newREST.Update(ctx, "srv2", rest.DefaultUpdatedObjectInfo(attemptNew2), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	oldResultMeta2, err := meta.Accessor(oldResult2)
	require.NoError(t, err)
	newResultMeta2, err := meta.Accessor(newResult2)
	require.NoError(t, err)
	oldResultCT := oldResultMeta2.GetCreationTimestamp()
	newResultCT := newResultMeta2.GetCreationTimestamp()
	assert.True(t, oldCreated2.CreationTimestamp.Time.Equal(oldResultCT.Time), "old: creationTimestamp must be carried over from old, not settable by an update (got %v, want %v)", oldResultCT, oldCreated2.CreationTimestamp)
	assert.True(t, newCreated2.CreationTimestamp.Time.Equal(newResultCT.Time), "new: creationTimestamp must be carried over from old, not settable by an update (got %v, want %v)", newResultCT, newCreated2.CreationTimestamp)
	assert.Equal(t, normalize(t, oldResult2), normalize(t, newResult2))
}

// --- Delete ---

func TestDifferential_DeleteHard(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	base := profileNamed("srv")
	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldOut, immediateOld, err := h.oldREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.True(t, immediateOld)

	newOut, immediateNew, err := h.newREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.True(t, immediateNew)

	assertEqualDeleteStatus(t, oldOut, newOut, "srv")

	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDifferential_DeleteWithFinalizer(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	base := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "srv", Finalizers: []string{"test/finalizer"}},
	}
	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldOut, immediateOld, err := h.oldREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	newOut, immediateNew, err := h.newREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)

	assert.False(t, immediateOld, "old: must not be hard-deleted while finalizers remain")
	assert.False(t, immediateNew, "new: must not be hard-deleted while finalizers remain")

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)

	require.NotNil(t, oldMeta.GetDeletionTimestamp())
	require.NotNil(t, newMeta.GetDeletionTimestamp())
	require.NotNil(t, oldMeta.GetDeletionGracePeriodSeconds())
	require.NotNil(t, newMeta.GetDeletionGracePeriodSeconds())
	assert.Equal(t, int64(0), *oldMeta.GetDeletionGracePeriodSeconds())
	assert.Equal(t, int64(0), *newMeta.GetDeletionGracePeriodSeconds())

	oldFresh, err := h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	newFresh, err := h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)

	oldFreshMeta, err := meta.Accessor(oldFresh)
	require.NoError(t, err)
	assert.NotNil(t, oldFreshMeta.GetDeletionTimestamp(), "old: deletionTimestamp must actually be persisted")
	newFreshMeta, err := meta.Accessor(newFresh)
	require.NoError(t, err)
	assert.NotNil(t, newFreshMeta.GetDeletionTimestamp(), "new: deletionTimestamp must actually be persisted")

	clearedOld := oldFresh.(*softwarecomposition.ContainerProfile).DeepCopy()
	clearedOld.Finalizers = nil
	_, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(clearedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: clearing the last finalizer must hard-delete")

	clearedNew := newFresh.(*softwarecomposition.ContainerProfile).DeepCopy()
	clearedNew.Finalizers = nil
	_, _, err = h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(clearedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: clearing the last finalizer must hard-delete")
}

func TestDifferential_DeleteCollection(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	names := []string{"a", "b", "c"}
	for _, name := range names {
		mustCreate(t, ctx, h.oldREST, profileNamed(name))
		mustCreate(t, ctx, h.newREST, profileNamed(name))
	}

	_, err := h.oldREST.DeleteCollection(ctx, rest.ValidateAllObjectFunc, &metav1.DeleteOptions{}, &metainternalversion.ListOptions{})
	require.NoError(t, err)
	_, err = h.newREST.DeleteCollection(ctx, rest.ValidateAllObjectFunc, &metav1.DeleteOptions{}, &metainternalversion.ListOptions{})
	require.NoError(t, err)

	for _, name := range names {
		_, err := h.oldREST.Get(ctx, name, &metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "old: %s should be gone", name)
		_, err = h.newREST.Get(ctx, name, &metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "new: %s should be gone", name)
	}
}

// --- List pagination ---

func paginateAllNames(t *testing.T, ctx context.Context, r rest.StandardStorage, pageSize int64) []string {
	t.Helper()
	var allNames []string
	cont := ""
	for {
		listObj, err := r.List(ctx, &metainternalversion.ListOptions{Limit: pageSize, Continue: cont})
		require.NoError(t, err)
		items, err := meta.ExtractList(listObj)
		require.NoError(t, err)
		for _, item := range items {
			m, err := meta.Accessor(item)
			require.NoError(t, err)
			allNames = append(allNames, m.GetName())
		}
		la, err := meta.ListAccessor(listObj)
		require.NoError(t, err)
		cont = la.GetContinue()
		if cont == "" {
			break
		}
	}
	return allNames
}

func TestDifferential_ListPagination(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	names := []string{"a", "b", "c", "d", "e"}
	for _, name := range names {
		mustCreate(t, ctx, h.oldREST, profileNamed(name))
		mustCreate(t, ctx, h.newREST, profileNamed(name))
	}

	oldNames := paginateAllNames(t, ctx, h.oldREST, 2)
	newNames := paginateAllNames(t, ctx, h.newREST, 2)

	assert.ElementsMatch(t, names, oldNames)
	assert.ElementsMatch(t, names, newNames)
	assert.Len(t, oldNames, len(names), "old: pagination must not duplicate or drop items")
	assert.Len(t, newNames, len(names), "new: pagination must not duplicate or drop items")
}

// --- Watch ---

// TestDifferential_Watch documents a pre-existing, shared StorageImpl
// limitation (see openvulnerabilityexchange's equivalent test): namespaced
// resources currently get an idle, event-free watch from StorageImpl.Watch,
// identically for both implementations, since containerprofile is namespaced.
// TestDifferential_Watch asserts a namespace-scoped watch delivers a real
// event for an object created in its namespace, identically for both
// implementations. Namespaced watches were rejected (an idle, event-free
// watch) prior to the namespace-watch fix (see
// docs/features/namespace-watch-enabled.md); both REST layers here call
// through to that same now-fixed StorageImpl.Watch, so both must now
// deliver the event rather than stay idle.
func TestDifferential_Watch(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pickRR func(h *harness) rest.StandardStorage
	}{
		{"old", func(h *harness) rest.StandardStorage { return h.oldREST }},
		{"new", func(h *harness) rest.StandardStorage { return h.newREST }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := testContext("ns1")
			r := tc.pickRR(h)

			w, err := r.Watch(ctx, &metainternalversion.ListOptions{})
			require.NoError(t, err)
			defer w.Stop()

			_, err = r.Create(ctx, profileNamed("srv"), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)

			select {
			case ev, ok := <-w.ResultChan():
				require.True(t, ok, "namespace-scoped watch must deliver a real event, not a closed channel")
				assert.Equal(t, watch.Added, ev.Type)
				created, ok := ev.Object.(*softwarecomposition.ContainerProfile)
				require.True(t, ok, "expected *ContainerProfile, got %T", ev.Object)
				assert.Equal(t, "srv", created.Name)
			case <-time.After(200 * time.Millisecond):
				t.Fatal("namespace-scoped watch did not deliver the Added event for an object created in its namespace")
			}
		})
	}
}

// --- Dry-run ---

func TestDifferential_DryRunCreate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	cp := profileNamed("srv")

	var oldOut runtime.Object
	var err error
	assert.NotPanics(t, func() {
		oldOut, err = h.oldREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	}, "OLD implementation must not panic on dry-run Create")
	require.NoError(t, err)
	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	assert.Equal(t, "srv", oldMeta.GetName())
	assert.NotEmpty(t, oldMeta.GetUID())

	newOut, err := h.newREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)
	assert.Equal(t, "srv", newMeta.GetName())
	assert.NotEmpty(t, newMeta.GetUID())

	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: dry-run create must not persist")
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: dry-run create must not persist")
}

func TestDifferential_DryRunUpdate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	base := profileNamed("srv")
	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.ContainerProfile)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.ContainerProfile)

	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec.ImageTag = "changed"
	var oldOut runtime.Object
	var err error
	assert.NotPanics(t, func() {
		oldOut, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	}, "OLD implementation must not panic on dry-run Update")
	require.NoError(t, err)
	assert.Equal(t, "changed", oldOut.(*softwarecomposition.ContainerProfile).Spec.ImageTag)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec.ImageTag = "changed"
	newOut, _, err := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	assert.Equal(t, "changed", newOut.(*softwarecomposition.ContainerProfile).Spec.ImageTag)

	persistedOld, err := h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, persistedOld.(*softwarecomposition.ContainerProfile).Spec.ImageTag, "old: dry-run update must not persist")

	persistedNew, err := h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, persistedNew.(*softwarecomposition.ContainerProfile).Spec.ImageTag, "new: dry-run update must not persist")
}

func TestDifferential_DryRunDelete(t *testing.T) {
	h := newHarness(t)
	ctx := testContext("ns1")
	base := profileNamed("srv")
	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldOut, immediateOld, err := h.oldREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	newOut, immediateNew, err := h.newREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)

	assert.Equal(t, immediateOld, immediateNew)
	assertEqualDeleteStatus(t, oldOut, newOut, "srv")

	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.NoError(t, err, "old: dry-run delete must not persist")
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.NoError(t, err, "new: dry-run delete must not persist")
}

// --- Protobuf round-trip ---

func TestDifferential_ProtobufRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name   string
		pickRR func(h *harness) rest.StandardStorage
	}{
		{"old", func(h *harness) rest.StandardStorage { return h.oldREST }},
		{"new", func(h *harness) rest.StandardStorage { return h.newREST }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sch := newTestScheme(t)
			h := newHarness(t)
			ctx := testContext("ns1")
			r := tc.pickRR(h)

			_, err := r.Create(ctx, profileNamed("srv"), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)

			fetched, err := r.Get(ctx, "srv", &metav1.GetOptions{})
			require.NoError(t, err)

			versionedOut, err := sch.ConvertToVersion(fetched, v1beta1.SchemeGroupVersion)
			require.NoError(t, err)
			versioned, ok := versionedOut.(*v1beta1.ContainerProfile)
			require.True(t, ok, "expected *v1beta1.ContainerProfile, got %T", versionedOut)

			data, err := versioned.Marshal()
			require.NoError(t, err)
			require.NotEmpty(t, data)

			var decoded v1beta1.ContainerProfile
			require.NoError(t, decoded.Unmarshal(data))

			assert.Equal(t, versioned.Name, decoded.Name)
			assert.Equal(t, versioned.UID, decoded.UID)
			assert.Equal(t, versioned.Spec, decoded.Spec)
			assert.Equal(t, versioned.ResourceVersion, decoded.ResourceVersion)
		})
	}
}
