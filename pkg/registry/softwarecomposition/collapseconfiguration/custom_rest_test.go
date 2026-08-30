package collapseconfiguration

import (
	"context"
	"strings"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
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
)

// Differential test suite: constructs BOTH the OLD (genericregistry.Store-
// based, etcd.go's NewREST) and the NEW (CustomREST, custom_rest.go)
// rest.Storage implementations, each against its own independent
// StorageImpl/SQLite pool, and runs identical operation sequences against
// both, asserting identical externally-observable results. Modeled
// directly on
// pkg/registry/softwarecomposition/knownservers/custom_rest_test.go for
// cluster scoping (CollapseConfiguration.NamespaceScoped() == false,
// strategy.go), plus validation-rejection cases modeled on
// pkg/registry/softwarecomposition/containerprofile/custom_rest_test.go's
// TestDifferential_NetworkNeighborInvalidEntryRejected pattern, since --
// unlike knownservers -- CollapseConfigurationStrategy has real (not no-op)
// Validate/ValidateUpdate logic: validateCollapseConfigurationSpec
// (strategy.go) rejects malformed or duplicate-prefix CollapseConfigs
// entries.

// newTestScheme builds a scheme with both the internal softwarecomposition
// types and the versioned v1beta1 types + conversions registered (mirroring
// pkg/apiserver.Scheme's install.Install(...) call), which StorageImpl's
// CalculateChecksum needs (it converts internal-typed objects to v1beta1
// before hashing).
func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sch := runtime.NewScheme()
	install.Install(sch)
	return sch
}

func newTestStorage(t *testing.T) storage.Interface {
	t.Helper()
	fs := afero.NewMemMapFs()
	pool := file.NewTestPool(t.TempDir())
	t.Cleanup(func() { _ = pool.Close() })
	return file.NewStorageImpl(fs, file.DefaultStorageRoot, pool, nil, newTestScheme(t))
}

func newOptsGetter() *options.StorageFactoryRestOptionsFactory {
	return &options.StorageFactoryRestOptionsFactory{StorageFactory: &options.SimpleStorageFactory{}}
}

func testContext() context.Context {
	return genericapirequest.WithNamespace(genericapirequest.NewContext(), "")
}

// harness holds an OLD and a NEW rest.Storage, each backed by its own,
// independent StorageImpl instance.
type harness struct {
	oldREST rest.StandardStorage
	newREST rest.StandardStorage
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	sch := newTestScheme(t)
	optsGetter := newOptsGetter()

	oldREST, err := NewREST(sch, newTestStorage(t), optsGetter)
	require.NoError(t, err)

	newREST, err := NewCustomREST(sch, newTestStorage(t), optsGetter)
	require.NoError(t, err)

	return &harness{oldREST: oldREST, newREST: newREST}
}

// newSharedHarness holds an OLD and a NEW rest.Storage backed by the SAME
// StorageImpl/pool instance -- used specifically to prove both
// implementations derive identical on-disk keys for the same name: a write
// through one and a read through the other must see the same object.
func newSharedHarness(t *testing.T) *harness {
	t.Helper()
	sch := newTestScheme(t)
	optsGetter := newOptsGetter()
	shared := newTestStorage(t)

	oldREST, err := NewREST(sch, shared, optsGetter)
	require.NoError(t, err)

	newREST, err := NewCustomREST(sch, shared, optsGetter)
	require.NoError(t, err)

	return &harness{oldREST: oldREST, newREST: newREST}
}

// normalize strips instance-specific fields (UID, CreationTimestamp, and the
// sync-checksum annotation, which is a hash of the full object including its
// UID -- see StorageImpl.saveObject/CalculateChecksum) that are legitimately
// different between two independently-created objects, while asserting
// those fields were themselves actually set (checklist item 1).
func normalize(t *testing.T, obj runtime.Object) *softwarecomposition.CollapseConfiguration {
	t.Helper()
	cc, ok := obj.(*softwarecomposition.CollapseConfiguration)
	require.True(t, ok, "expected *softwarecomposition.CollapseConfiguration, got %T", obj)
	require.NotEmpty(t, cc.UID, "UID must be set")
	require.False(t, cc.CreationTimestamp.IsZero(), "creationTimestamp must be set")
	out := cc.DeepCopy()
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

// assertEqualDeleteStatus asserts both hard-delete results are the
// *metav1.Status success object genericregistry.Store.finalizeDelete
// produces when ReturnDeletedObject is false (the only case in this repo).
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

func collapseConfig(name string, entries ...softwarecomposition.CollapseConfigEntry) *softwarecomposition.CollapseConfiguration {
	return &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       softwarecomposition.CollapseConfigurationSpec{CollapseConfigs: entries},
	}
}

func mustCreate(t *testing.T, ctx context.Context, r rest.StandardStorage, obj *softwarecomposition.CollapseConfiguration) runtime.Object {
	t.Helper()
	out, err := r.Create(ctx, obj.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	return out
}

// --- Create ---

func TestDifferential_CreateExplicitName(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	cc := collapseConfig("cc-a", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})

	oldOut := mustCreate(t, ctx, h.oldREST, cc)
	newOut := mustCreate(t, ctx, h.newREST, cc)

	assert.Equal(t, normalize(t, oldOut), normalize(t, newOut))

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)
	assert.NotEmpty(t, oldMeta.GetResourceVersion())
	assert.NotEmpty(t, newMeta.GetResourceVersion())
	assert.Equal(t, oldMeta.GetResourceVersion(), newMeta.GetResourceVersion())

	// Creating again with the same explicit name must fail identically (AlreadyExists).
	_, err = h.oldREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "old: %v", err)
	_, err = h.newREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "new: %v", err)
}

func TestDifferential_CreateGenerateName(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	cc := &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{GenerateName: "cc-"}}

	oldOut := mustCreate(t, ctx, h.oldREST, cc)
	newOut := mustCreate(t, ctx, h.newREST, cc)

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(oldMeta.GetName(), "cc-"), "old name: %s", oldMeta.GetName())
	assert.True(t, strings.HasPrefix(newMeta.GetName(), "cc-"), "new name: %s", newMeta.GetName())
	assert.NotEmpty(t, oldMeta.GetUID())
	assert.NotEmpty(t, newMeta.GetUID())

	// A second Create with the same generateName must succeed with a
	// different generated name.
	oldOut2 := mustCreate(t, ctx, h.oldREST, cc)
	newOut2 := mustCreate(t, ctx, h.newREST, cc)
	oldMeta2, _ := meta.Accessor(oldOut2)
	newMeta2, _ := meta.Accessor(newOut2)
	assert.NotEqual(t, oldMeta.GetName(), oldMeta2.GetName())
	assert.NotEqual(t, newMeta.GetName(), newMeta2.GetName())
}

// --- Validation ---

// TestDifferential_CreateValidSucceeds proves a well-formed
// CollapseConfiguration (non-empty, "/"-prefixed, unique prefixes, all
// thresholds >= 1) is accepted identically by both implementations.
func TestDifferential_CreateValidSucceeds(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	cc := collapseConfig("cc-valid",
		softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4},
		softwarecomposition.CollapseConfigEntry{Prefix: "/opt", Threshold: 8},
	)

	oldOut, err := h.oldREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	newOut, err := h.newREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)

	assert.Equal(t, normalize(t, oldOut), normalize(t, newOut))

	// Create's direct return value only carries ObjectMeta/SchemaVersion (see
	// saveObject/extractFields in storage.go) -- a pre-existing StorageImpl
	// behavior shared identically by both implementations. Verify the
	// actually-persisted Spec via a fresh Get.
	oldFresh, err := h.oldREST.Get(ctx, "cc-valid", &metav1.GetOptions{})
	require.NoError(t, err)
	newFresh, err := h.newREST.Get(ctx, "cc-valid", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, cc.Spec.CollapseConfigs, oldFresh.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs)
	assert.Equal(t, cc.Spec.CollapseConfigs, newFresh.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs)
}

// TestDifferential_CreateEmptyPrefixRejected exercises
// validateCollapseConfigurationSpec's requirement that every CollapseConfigs
// entry have a non-empty Prefix (strategy.go). Both implementations invoke
// this identically: genericregistry.Store's create path and
// genericrest.Store's create() both call rest.BeforeCreate, which calls
// strategy.Validate at the same point.
func TestDifferential_CreateEmptyPrefixRejected(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	cc := collapseConfig("cc-empty-prefix", softwarecomposition.CollapseConfigEntry{Prefix: "", Threshold: 4})

	_, oldErr := h.oldREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.Error(t, oldErr)
	assert.True(t, apierrors.IsInvalid(oldErr), "old: %v", oldErr)

	_, newErr := h.newREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.Error(t, newErr)
	assert.True(t, apierrors.IsInvalid(newErr), "new: %v", newErr)

	// Neither implementation persisted anything.
	_, err := h.oldREST.Get(ctx, "cc-empty-prefix", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: rejected create must not persist")
	_, err = h.newREST.Get(ctx, "cc-empty-prefix", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: rejected create must not persist")
}

// TestDifferential_CreateDuplicatePrefixRejected exercises
// validateCollapseConfigurationSpec's rejection of duplicate Prefix values
// across CollapseConfigs entries (strategy.go).
func TestDifferential_CreateDuplicatePrefixRejected(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	cc := collapseConfig("cc-dup-prefix",
		softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4},
		softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 8},
	)

	_, oldErr := h.oldREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.Error(t, oldErr)
	assert.True(t, apierrors.IsInvalid(oldErr), "old: %v", oldErr)

	_, newErr := h.newREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.Error(t, newErr)
	assert.True(t, apierrors.IsInvalid(newErr), "new: %v", newErr)

	// Neither implementation persisted anything.
	_, err := h.oldREST.Get(ctx, "cc-dup-prefix", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: rejected create must not persist")
	_, err = h.newREST.Get(ctx, "cc-dup-prefix", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: rejected create must not persist")
}

// TestDifferential_UpdateInvalidRejected exercises the same
// validateCollapseConfigurationSpec rule via ValidateUpdate (a zero
// Threshold this time, rather than an empty Prefix, for coverage variety):
// rest.BeforeUpdate calls strategy.ValidateUpdate at the same point for both
// implementations.
func TestDifferential_UpdateInvalidRejected(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := collapseConfig("cc-upd", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})

	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.CollapseConfiguration)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.CollapseConfiguration)

	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec.CollapseConfigs = []softwarecomposition.CollapseConfigEntry{{Prefix: "/etc", Threshold: 0}}
	_, _, oldErr := h.oldREST.Update(ctx, "cc-upd", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, oldErr)
	assert.True(t, apierrors.IsInvalid(oldErr), "old: %v", oldErr)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec.CollapseConfigs = []softwarecomposition.CollapseConfigEntry{{Prefix: "/etc", Threshold: 0}}
	_, _, newErr := h.newREST.Update(ctx, "cc-upd", rest.DefaultUpdatedObjectInfo(updatedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, newErr)
	assert.True(t, apierrors.IsInvalid(newErr), "new: %v", newErr)

	// Neither implementation persisted the rejected update: the original
	// Threshold is still there.
	oldFresh, err := h.oldREST.Get(ctx, "cc-upd", &metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, oldFresh.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, int32(4), oldFresh.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Threshold)
	newFresh, err := h.newREST.Get(ctx, "cc-upd", &metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, newFresh.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, int32(4), newFresh.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Threshold)
}

// TestDifferential_KeyDerivationMatches verifies -- against a SHARED
// StorageImpl -- that both implementations derive identical on-disk keys
// for the same (cluster-scoped) name: a Create through the OLD
// implementation must be readable through the NEW implementation and vice
// versa.
func TestDifferential_KeyDerivationMatches(t *testing.T) {
	h := newSharedHarness(t)
	ctx := testContext()

	// Written via OLD, read via NEW.
	mustCreate(t, ctx, h.oldREST, collapseConfig("via-old", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4}))
	viaNew, err := h.newREST.Get(ctx, "via-old", &metav1.GetOptions{})
	require.NoError(t, err, "NEW implementation must read a key written by the OLD implementation for the same name")
	require.Len(t, viaNew.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, "/etc", viaNew.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Prefix)

	// Written via NEW, read via OLD.
	mustCreate(t, ctx, h.newREST, collapseConfig("via-new", softwarecomposition.CollapseConfigEntry{Prefix: "/opt", Threshold: 8}))
	viaOld, err := h.oldREST.Get(ctx, "via-new", &metav1.GetOptions{})
	require.NoError(t, err, "OLD implementation must read a key written by the NEW implementation for the same name")
	require.Len(t, viaOld.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, "/opt", viaOld.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Prefix)
}

// --- Update ---

func TestDifferential_UpdateResourceVersion(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := collapseConfig("srv", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})

	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.CollapseConfiguration)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.CollapseConfiguration)

	// Correct resourceVersion succeeds. oldCreated/newCreated come straight
	// from mustCreate's return value, which only carries
	// ObjectMeta/SchemaVersion -- Spec.CollapseConfigs is empty there, so the
	// update assigns a whole new slice rather than indexing into an empty one.
	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec.CollapseConfigs = []softwarecomposition.CollapseConfigEntry{{Prefix: "/opt", Threshold: 8}}
	oldUpdated, _, err := h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec.CollapseConfigs = []softwarecomposition.CollapseConfigEntry{{Prefix: "/opt", Threshold: 8}}
	newUpdated, _, err := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	assert.Equal(t, normalize(t, oldUpdated), normalize(t, newUpdated))

	// Stale resourceVersion (the pre-update copy) is rejected identically with a Conflict.
	staleOld := oldCreated.DeepCopy()
	_, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(staleOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsConflict(err), "old: %v", err)

	staleNew := newCreated.DeepCopy()
	_, _, err = h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(staleNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.Error(t, err)
	assert.True(t, apierrors.IsConflict(err), "new: %v", err)

	// No resourceVersion at all is rejected identically with an Invalid error.
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
	ctx := testContext()
	base := &softwarecomposition.CollapseConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "srv"},
	}

	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldFetched, err := h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	newFetched, err := h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	oldCreated := oldFetched.(*softwarecomposition.CollapseConfiguration)
	newCreated := newFetched.(*softwarecomposition.CollapseConfiguration)

	// creationTimestamp is carried over from old, not settable by an update.
	attemptOld := oldCreated.DeepCopy()
	attemptOld.CreationTimestamp = metav1.NewTime(time.Now().Add(24 * time.Hour))
	oldResult, _, err := h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(attemptOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	attemptNew := newCreated.DeepCopy()
	attemptNew.CreationTimestamp = metav1.NewTime(time.Now().Add(24 * time.Hour))
	newResult, _, err := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(attemptNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	oldResultMeta, err := meta.Accessor(oldResult)
	require.NoError(t, err)
	newResultMeta, err := meta.Accessor(newResult)
	require.NoError(t, err)
	// Compared via time.Time.Equal rather than assert.Equal/DeepEqual: the
	// value round-trips through gob storage and back, which strips the
	// monotonic clock reading DeepEqual is sensitive to.
	oldResultCT := oldResultMeta.GetCreationTimestamp()
	newResultCT := newResultMeta.GetCreationTimestamp()
	assert.True(t, oldCreated.CreationTimestamp.Time.Equal(oldResultCT.Time), "old: creationTimestamp must be carried over from old, not settable by an update (got %v, want %v)", oldResultCT, oldCreated.CreationTimestamp)
	assert.True(t, newCreated.CreationTimestamp.Time.Equal(newResultCT.Time), "new: creationTimestamp must be carried over from old, not settable by an update (got %v, want %v)", newResultCT, newCreated.CreationTimestamp)
	assert.Equal(t, normalize(t, oldResult), normalize(t, newResult))
}

// --- Delete ---

func TestDifferential_DeleteHard(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := collapseConfig("srv", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})
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
	ctx := testContext()
	base := &softwarecomposition.CollapseConfiguration{
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

	// Still retrievable -- not hard-deleted.
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

	// Clearing the finalizer via Update hard-deletes identically for both implementations.
	clearedOld := oldFresh.(*softwarecomposition.CollapseConfiguration).DeepCopy()
	clearedOld.Finalizers = nil
	_, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(clearedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: clearing the last finalizer must hard-delete")

	clearedNew := newFresh.(*softwarecomposition.CollapseConfiguration).DeepCopy()
	clearedNew.Finalizers = nil
	_, _, err = h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(clearedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: clearing the last finalizer must hard-delete")
}

func TestDifferential_DeleteCollection(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	names := []string{"a", "b", "c"}
	for _, name := range names {
		mustCreate(t, ctx, h.oldREST, &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{Name: name}})
		mustCreate(t, ctx, h.newREST, &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{Name: name}})
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
	ctx := testContext()
	names := []string{"a", "b", "c", "d", "e"}
	for _, name := range names {
		mustCreate(t, ctx, h.oldREST, &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{Name: name}})
		mustCreate(t, ctx, h.newREST, &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}

	oldNames := paginateAllNames(t, ctx, h.oldREST, 2)
	newNames := paginateAllNames(t, ctx, h.newREST, 2)

	assert.ElementsMatch(t, names, oldNames)
	assert.ElementsMatch(t, names, newNames)
	assert.Len(t, oldNames, len(names), "old: pagination must not duplicate or drop items")
	assert.Len(t, newNames, len(names), "new: pagination must not duplicate or drop items")
}

// --- Watch ---

func requireEvent(t *testing.T, w watch.Interface, wantType watch.EventType) watch.Event {
	t.Helper()
	select {
	case ev, ok := <-w.ResultChan():
		require.True(t, ok, "watch channel closed unexpectedly while waiting for %s", wantType)
		require.Equal(t, wantType, ev.Type)
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for a %s event", wantType)
		return watch.Event{}
	}
}

// TestDifferential_Watch delivers real events (unlike the namespaced Group A
// resources): CollapseConfiguration is cluster-scoped, so its keys carry no
// namespace segment and never trigger StorageImpl.Watch's namespace-key
// rejection (storage.go) -- same as knownservers.
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
			ctx := testContext()
			r := tc.pickRR(h)

			w, err := r.Watch(ctx, &metainternalversion.ListOptions{})
			require.NoError(t, err)
			defer w.Stop()

			_, err = r.Create(ctx, &softwarecomposition.CollapseConfiguration{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)
			added := requireEvent(t, w, watch.Added)
			addedMeta, err := meta.Accessor(added.Object)
			require.NoError(t, err)
			assert.Equal(t, "srv", addedMeta.GetName())

			created, err := r.Get(ctx, "srv", &metav1.GetOptions{})
			require.NoError(t, err)
			updated := created.(*softwarecomposition.CollapseConfiguration).DeepCopy()
			updated.Labels = map[string]string{"changed": "true"}
			_, _, err = r.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updated), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
			require.NoError(t, err)
			requireEvent(t, w, watch.Modified)

			_, _, err = r.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
			require.NoError(t, err)
			requireEvent(t, w, watch.Deleted)
		})
	}
}

// --- Dry-run ---

func TestDifferential_DryRunCreate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	cc := collapseConfig("srv", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})

	var oldOut runtime.Object
	var err error
	assert.NotPanics(t, func() {
		oldOut, err = h.oldREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	}, "OLD implementation must not panic on dry-run Create")
	require.NoError(t, err)
	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	assert.Equal(t, "srv", oldMeta.GetName())
	assert.NotEmpty(t, oldMeta.GetUID())

	newOut, err := h.newREST.Create(ctx, cc.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)
	assert.Equal(t, "srv", newMeta.GetName())
	assert.NotEmpty(t, newMeta.GetUID())

	// Neither implementation actually persisted anything.
	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: dry-run create must not persist")
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "new: dry-run create must not persist")
}

func TestDifferential_DryRunUpdate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := collapseConfig("srv", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})
	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.CollapseConfiguration)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.CollapseConfiguration)

	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec.CollapseConfigs = []softwarecomposition.CollapseConfigEntry{{Prefix: "/opt", Threshold: 9}}
	var oldOut runtime.Object
	var err error
	assert.NotPanics(t, func() {
		oldOut, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	}, "OLD implementation must not panic on dry-run Update")
	require.NoError(t, err)
	require.Len(t, oldOut.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, "/opt", oldOut.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Prefix)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec.CollapseConfigs = []softwarecomposition.CollapseConfigEntry{{Prefix: "/opt", Threshold: 9}}
	newOut, _, err := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	require.Len(t, newOut.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, "/opt", newOut.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Prefix)

	// Neither implementation actually persisted the change.
	persistedOld, err := h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, persistedOld.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, "/etc", persistedOld.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Prefix, "old: dry-run update must not persist")

	persistedNew, err := h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	require.Len(t, persistedNew.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs, 1)
	assert.Equal(t, "/etc", persistedNew.(*softwarecomposition.CollapseConfiguration).Spec.CollapseConfigs[0].Prefix, "new: dry-run update must not persist")
}

func TestDifferential_DryRunDelete(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := collapseConfig("srv", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4})
	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldOut, immediateOld, err := h.oldREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	newOut, immediateNew, err := h.newREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)

	assert.Equal(t, immediateOld, immediateNew)
	assertEqualDeleteStatus(t, oldOut, newOut, "srv")

	// Not actually persisted -- the object is still there.
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
			ctx := testContext()
			r := tc.pickRR(h)

			_, err := r.Create(ctx, collapseConfig("srv", softwarecomposition.CollapseConfigEntry{Prefix: "/etc", Threshold: 4}), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)

			fetched, err := r.Get(ctx, "srv", &metav1.GetOptions{})
			require.NoError(t, err)

			versionedOut, err := sch.ConvertToVersion(fetched, v1beta1.SchemeGroupVersion)
			require.NoError(t, err)
			versioned, ok := versionedOut.(*v1beta1.CollapseConfiguration)
			require.True(t, ok, "expected *v1beta1.CollapseConfiguration, got %T", versionedOut)

			data, err := versioned.Marshal()
			require.NoError(t, err)
			require.NotEmpty(t, data)

			var decoded v1beta1.CollapseConfiguration
			require.NoError(t, decoded.Unmarshal(data))

			assert.Equal(t, versioned.Name, decoded.Name)
			assert.Equal(t, versioned.UID, decoded.UID)
			assert.Equal(t, versioned.Spec, decoded.Spec)
			assert.Equal(t, versioned.ResourceVersion, decoded.ResourceVersion)
		})
	}
}
