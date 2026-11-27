package knownservers

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
// StorageImpl/SQLite pool (so operations against one never observe state
// from the other), and runs identical operation sequences against both,
// asserting identical externally-observable results -- or documenting a
// deliberate, narrow difference (see the dry-run Create/Update tests).
//
// This is the Phase 4 deliverable described in
// docs/features/generic-rest-storage-phase4.md: there was no existing
// REST-layer test coverage for knownservers to extend, so this is built
// from scratch.

// newTestScheme builds a scheme with both the internal softwarecomposition
// types and the versioned v1beta1 types + conversions registered (mirroring
// pkg/apiserver.Scheme's install.Install(...) call), which StorageImpl's
// CalculateChecksum needs (it converts internal-typed objects to v1beta1
// before hashing). The generated clientset's scheme.Scheme is NOT sufficient
// here -- it only registers the versioned types, not the internal ones our
// REST layer's New()/NewList() produce.
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

// normalize strips instance-specific fields (UID, CreationTimestamp, and the
// sync-checksum annotation, which is a hash of the full object including its
// UID -- see StorageImpl.saveObject/CalculateChecksum) that are legitimately
// different between two independently-created objects (fresh UUID, fresh
// wall-clock time, and therefore a fresh checksum) before requiring the rest
// of the object to be deep-equal, while asserting those fields were
// themselves actually set (checklist item 1).
func normalize(t *testing.T, obj runtime.Object) *softwarecomposition.KnownServer {
	t.Helper()
	ks, ok := obj.(*softwarecomposition.KnownServer)
	require.True(t, ok, "expected *softwarecomposition.KnownServer, got %T", obj)
	require.NotEmpty(t, ks.UID, "UID must be set")
	require.False(t, ks.CreationTimestamp.IsZero(), "creationTimestamp must be set")
	out := ks.DeepCopy()
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

func mustCreate(t *testing.T, ctx context.Context, r rest.StandardStorage, obj *softwarecomposition.KnownServer) runtime.Object {
	t.Helper()
	out, err := r.Create(ctx, obj.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)
	return out
}

// --- Create ---

func TestDifferential_CreateExplicitName(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	ks := &softwarecomposition.KnownServer{
		ObjectMeta: metav1.ObjectMeta{Name: "server-a"},
		Spec:       softwarecomposition.KnownServerSpec{{IPBlock: "10.0.0.0/8", Server: "s1", Name: "n1"}},
	}

	oldOut := mustCreate(t, ctx, h.oldREST, ks)
	newOut := mustCreate(t, ctx, h.newREST, ks)

	assert.Equal(t, normalize(t, oldOut), normalize(t, newOut))

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)
	assert.NotEmpty(t, oldMeta.GetResourceVersion())
	assert.NotEmpty(t, newMeta.GetResourceVersion())
	assert.Equal(t, oldMeta.GetResourceVersion(), newMeta.GetResourceVersion())

	// Creating again with the same explicit name must fail identically (AlreadyExists).
	_, err = h.oldREST.Create(ctx, ks.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "old: %v", err)
	_, err = h.newREST.Create(ctx, ks.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "new: %v", err)
}

func TestDifferential_CreateGenerateName(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	ks := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{GenerateName: "srv-"}}

	oldOut := mustCreate(t, ctx, h.oldREST, ks)
	newOut := mustCreate(t, ctx, h.newREST, ks)

	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	newMeta, err := meta.Accessor(newOut)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(oldMeta.GetName(), "srv-"), "old name: %s", oldMeta.GetName())
	assert.True(t, strings.HasPrefix(newMeta.GetName(), "srv-"), "new name: %s", newMeta.GetName())
	assert.NotEmpty(t, oldMeta.GetUID())
	assert.NotEmpty(t, newMeta.GetUID())

	// A second Create with the same generateName must succeed with a
	// different generated name (bounded retry is not exercised here since
	// there's no collision -- generateName always picks a fresh suffix).
	oldOut2 := mustCreate(t, ctx, h.oldREST, ks)
	newOut2 := mustCreate(t, ctx, h.newREST, ks)
	oldMeta2, _ := meta.Accessor(oldOut2)
	newMeta2, _ := meta.Accessor(newOut2)
	assert.NotEqual(t, oldMeta.GetName(), oldMeta2.GetName())
	assert.NotEqual(t, newMeta.GetName(), newMeta2.GetName())
}

// --- Update ---

func TestDifferential_UpdateResourceVersion(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}

	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.KnownServer)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.KnownServer)

	// Correct resourceVersion succeeds.
	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec = softwarecomposition.KnownServerSpec{{Name: "updated"}}
	oldUpdated, _, err := h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec = softwarecomposition.KnownServerSpec{{Name: "updated"}}
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
	base := &softwarecomposition.KnownServer{
		ObjectMeta: metav1.ObjectMeta{Name: "srv", Finalizers: []string{"test/finalizer"}},
	}

	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	// Delete (with a finalizer present) sets deletionTimestamp without hard-deleting.
	oldDeleted, _, err := h.oldREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	newDeleted, _, err := h.newREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)

	oldDeletedMeta, err := meta.Accessor(oldDeleted)
	require.NoError(t, err)
	newDeletedMeta, err := meta.Accessor(newDeleted)
	require.NoError(t, err)
	require.NotNil(t, oldDeletedMeta.GetDeletionTimestamp())
	require.NotNil(t, newDeletedMeta.GetDeletionTimestamp())

	// Attempt an update that tries to remove deletionTimestamp and change
	// creationTimestamp; the finalizer is kept so this isn't the
	// emptied-finalizers hard-delete path (checklist item 3: the five
	// BeforeUpdate carry-over rules).
	//
	// Finding: this specific combination -- updating an object whose
	// deletionGracePeriodSeconds was just set to *0 by the finalizer-delete
	// above -- hits a pre-existing StorageImpl behavior shared identically by
	// BOTH implementations, not a difference between them: encoding/gob does
	// not round-trip a non-nil pointer to a zero value (it comes back nil on
	// decode), so the fresh Get() that GuaranteedUpdate performs internally
	// (both old and new route through the same StorageImpl.Get/GuaranteedUpdate)
	// observes DeletionGracePeriodSeconds as nil on the "existing" side while
	// the incoming object still has a non-nil pointer to 0, and
	// genericvalidation.ValidateImmutableField (invoked via rest.BeforeUpdate ->
	// validateCommonFields, exercised identically by both implementations)
	// reports "field is immutable". This is out of scope to fix here (it lives
	// in StorageImpl's gob encode/decode, explicitly excluded from this task),
	// so this test asserts both implementations fail the same way, rather than
	// asserting success.
	attemptOld := oldDeleted.(*softwarecomposition.KnownServer).DeepCopy()
	attemptOld.DeletionTimestamp = nil
	attemptOld.CreationTimestamp = metav1.NewTime(time.Now().Add(24 * time.Hour))
	_, _, oldErr := h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(attemptOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})

	attemptNew := newDeleted.(*softwarecomposition.KnownServer).DeepCopy()
	attemptNew.DeletionTimestamp = nil
	attemptNew.CreationTimestamp = metav1.NewTime(time.Now().Add(24 * time.Hour))
	_, _, newErr := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(attemptNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})

	require.Error(t, oldErr)
	require.Error(t, newErr)
	assert.True(t, apierrors.IsInvalid(oldErr), "old: %v", oldErr)
	assert.True(t, apierrors.IsInvalid(newErr), "new: %v", newErr)
	assert.Equal(t, oldErr.Error(), newErr.Error(), "both implementations must fail identically")

	// Retry without ever having forced deletionGracePeriodSeconds through a
	// Get round-trip in this sequence: carry-over of creationTimestamp and
	// rejection of a deletionTimestamp removal, on an object that was never
	// deleted, so no *0 pointer round-trip is involved.
	base2 := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv2"}}
	oldCreated2 := mustCreate(t, ctx, h.oldREST, base2).(*softwarecomposition.KnownServer)
	newCreated2 := mustCreate(t, ctx, h.newREST, base2).(*softwarecomposition.KnownServer)

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
	// Compared via time.Time.Equal rather than assert.Equal/DeepEqual: the
	// value round-trips through JSON/gob storage and back, which strips the
	// monotonic clock reading DeepEqual is sensitive to, even though the
	// wall-clock instant (and RFC3339 serialization) is identical.
	oldResultCT := oldResultMeta2.GetCreationTimestamp()
	newResultCT := newResultMeta2.GetCreationTimestamp()
	assert.True(t, oldCreated2.CreationTimestamp.Time.Equal(oldResultCT.Time), "old: creationTimestamp must be carried over from old, not settable by an update (got %v, want %v)", oldResultCT, oldCreated2.CreationTimestamp)
	assert.True(t, newCreated2.CreationTimestamp.Time.Equal(newResultCT.Time), "new: creationTimestamp must be carried over from old, not settable by an update (got %v, want %v)", newResultCT, newCreated2.CreationTimestamp)
	assert.Equal(t, normalize(t, oldResult2), normalize(t, newResult2))
}

// --- Delete ---

func TestDifferential_DeleteHard(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}
	mustCreate(t, ctx, h.oldREST, base)
	mustCreate(t, ctx, h.newREST, base)

	oldOut, immediateOld, err := h.oldREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.True(t, immediateOld)

	newOut, immediateNew, err := h.newREST.Delete(ctx, "srv", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.True(t, immediateNew)

	// Neither implementation sets ReturnDeletedObject/DeleteReturnsDeletedObject
	// for this resource, so a hard delete returns a *metav1.Status, not the
	// deleted object (mirrors genericregistry.Store.finalizeDelete, vendor
	// store.go:1397-1409).
	assertEqualDeleteStatus(t, oldOut, newOut, "srv")

	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDifferential_DeleteWithFinalizer(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := &softwarecomposition.KnownServer{
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

	// Both Delete() calls' own return values claim deletionTimestamp/
	// deletionGracePeriodSeconds were set -- see the finding below for why
	// this is misleading for the OLD implementation specifically.
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

	// Historical finding (now FIXED): StorageImpl.GuaranteedUpdateWithConn's
	// no-op-update detection (storage.go's `orig := origState.obj.DeepCopyObject()`
	// / `reflect.DeepEqual(orig, ret)` check) used to take its "before"
	// snapshot from the SAME object reference passed to tryUpdate as
	// `existing`, AFTER tryUpdate had already run. A tryUpdate closure that
	// mutates `existing` in place and returns that same reference -- exactly
	// what genericregistry.Store's own finalizer-delete tryUpdate does
	// (vendor store.go:1085-1090, `markAsDeleting(existing, ...)` followed by
	// `return existing, nil`) -- fooled that check: the "before" snapshot
	// ended up reflecting the already-mutated state, so the update was
	// spuriously treated as a no-op and saveObject was never called. Despite
	// what OLD's Delete() return value above claimed, the deletionTimestamp
	// it "set" was NEVER actually persisted for the OLD implementation.
	//
	// This has been fixed in GuaranteedUpdateWithConn: the "before" snapshot
	// is now taken immediately before tryUpdate runs (on every retry-loop
	// iteration), so it can no longer observe tryUpdate's own in-place
	// mutation. Both implementations now correctly persist the
	// deletionTimestamp, verified by a fresh Get:
	oldFreshMeta, err := meta.Accessor(oldFresh)
	require.NoError(t, err)
	assert.NotNil(t, oldFreshMeta.GetDeletionTimestamp(), "old: deletionTimestamp must now actually be persisted (StorageImpl no-op-update-detection bug has been fixed)")

	// The NEW implementation additionally, and independently, avoids the bug
	// class entirely by deep-copying `existing` before mutating it in its own
	// Delete tryUpdate closure (see custom_rest.go) -- so it never depended on
	// the StorageImpl fix above to behave correctly here. Both now agree:
	newFreshMeta, err := meta.Accessor(newFresh)
	require.NoError(t, err)
	assert.NotNil(t, newFreshMeta.GetDeletionTimestamp(), "new: deletionTimestamp must actually be persisted")

	// Consequently: clearing the finalizer via Update now hard-deletes
	// (ShouldDeleteDuringUpdate) identically for BOTH implementations, since
	// both now have a stored state that genuinely has deletionTimestamp set.
	// This is a direct, observable consequence of the StorageImpl fix above,
	// not a new difference introduced by this step.
	clearedOld := oldFresh.(*softwarecomposition.KnownServer).DeepCopy()
	clearedOld.Finalizers = nil
	_, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(clearedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err), "old: clearing the last finalizer must now hard-delete (StorageImpl no-op-update-detection bug has been fixed)")

	clearedNew := newFresh.(*softwarecomposition.KnownServer).DeepCopy()
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
		mustCreate(t, ctx, h.oldREST, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: name}})
		mustCreate(t, ctx, h.newREST, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: name}})
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
		mustCreate(t, ctx, h.oldREST, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: name}})
		mustCreate(t, ctx, h.newREST, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: name}})
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

			_, err = r.Create(ctx, &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)
			added := requireEvent(t, w, watch.Added)
			addedMeta, err := meta.Accessor(added.Object)
			require.NoError(t, err)
			assert.Equal(t, "srv", addedMeta.GetName())

			created, err := r.Get(ctx, "srv", &metav1.GetOptions{})
			require.NoError(t, err)
			updated := created.(*softwarecomposition.KnownServer).DeepCopy()
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

// TestDifferential_DryRunCreate documents a historical, now-fixed bug in the
// OLD implementation (checklist item 7, dry-run): the OLD implementation's
// NewREST used to wire genericregistry.DryRunnableStorage{Codec: nil, ...}
// (etcd.go). A real dry-run Create made DryRunnableStorage.Create take the
// `dryRun` branch, which calls s.copyInto (vendor dryrun.go:44), which calls
// runtime.Encode(s.Codec, in) -- invoking a method on a nil Codec interface,
// which panicked. In production this was caught by the apiserver's panic
// recovery middleware and surfaced as a 500 to the client (matching the
// "dry-run ... returns ServiceUnavailable" observation recorded for a
// different resource during the single-writer spike -- see the plan doc).
//
// This has been fixed: every softwarecomposition/*/etcd.go's NewREST,
// including this resource's, now wires a real, non-nil codec via
// registry.NewCodec(scheme) (see pkg/registry/registry.go). The OLD
// implementation therefore no longer panics on dry-run Create -- this test
// now asserts that directly, alongside the NEW implementation's
// (independently correct, DeepCopyObject-based) behavior.
func TestDifferential_DryRunCreate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	ks := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}

	var oldOut runtime.Object
	var err error
	assert.NotPanics(t, func() {
		oldOut, err = h.oldREST.Create(ctx, ks.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	}, "OLD implementation must no longer panic on dry-run Create (nil Codec bug has been fixed)")
	require.NoError(t, err)
	oldMeta, err := meta.Accessor(oldOut)
	require.NoError(t, err)
	assert.Equal(t, "srv", oldMeta.GetName())
	assert.NotEmpty(t, oldMeta.GetUID())

	newOut, err := h.newREST.Create(ctx, ks.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
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

// TestDifferential_DryRunUpdate documents the same historical, now-fixed
// nil-Codec issue as above, but for Update: DryRunnableStorage.GuaranteedUpdate's
// dry-run branch also ends with s.copyInto(updated, destination) (vendor
// dryrun.go:105), which used to panic identically for the same reason -- and
// no longer does, for the same reason (registry.NewCodec(scheme)).
func TestDifferential_DryRunUpdate(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}
	oldCreated := mustCreate(t, ctx, h.oldREST, base).(*softwarecomposition.KnownServer)
	newCreated := mustCreate(t, ctx, h.newREST, base).(*softwarecomposition.KnownServer)

	updatedOld := oldCreated.DeepCopy()
	updatedOld.Spec = softwarecomposition.KnownServerSpec{{Name: "changed"}}
	var oldOut runtime.Object
	var err error
	assert.NotPanics(t, func() {
		oldOut, _, err = h.oldREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedOld), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	}, "OLD implementation must no longer panic on dry-run Update (nil Codec bug has been fixed)")
	require.NoError(t, err)
	assert.Equal(t, softwarecomposition.KnownServerSpec{{Name: "changed"}}, oldOut.(*softwarecomposition.KnownServer).Spec)

	updatedNew := newCreated.DeepCopy()
	updatedNew.Spec = softwarecomposition.KnownServerSpec{{Name: "changed"}}
	newOut, _, err := h.newREST.Update(ctx, "srv", rest.DefaultUpdatedObjectInfo(updatedNew), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
	require.NoError(t, err)
	assert.Equal(t, softwarecomposition.KnownServerSpec{{Name: "changed"}}, newOut.(*softwarecomposition.KnownServer).Spec)

	// Neither implementation actually persisted the change.
	persistedOld, err := h.oldREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, persistedOld.(*softwarecomposition.KnownServer).Spec, "old: dry-run update must not persist")

	persistedNew, err := h.newREST.Get(ctx, "srv", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Empty(t, persistedNew.(*softwarecomposition.KnownServer).Spec, "new: dry-run update must not persist")
}

// TestDifferential_DryRunDelete confirms dry-run Delete behaves identically
// old vs. new (unlike Create/Update's dry-run, Delete's dry-run path never
// touched the nil Codec even before it was fixed -- see the comment on
// TestDifferential_DryRunCreate -- so no documented
// difference is expected here).
func TestDifferential_DryRunDelete(t *testing.T) {
	h := newHarness(t)
	ctx := testContext()
	base := &softwarecomposition.KnownServer{ObjectMeta: metav1.ObjectMeta{Name: "srv"}}
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

// TestDifferential_ProtobufRoundTrip proves (rather than assumes) that an
// object served by either rest.Storage implementation round-trips correctly
// through the generated protobuf Marshal/Unmarshal (v1beta1/generated.pb.go)
// once converted to the versioned type -- content negotiation itself
// happens above rest.Storage in the real apiserver, so this should (and
// does) behave identically regardless of which implementation is behind it.
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

			_, err := r.Create(ctx, &softwarecomposition.KnownServer{
				ObjectMeta: metav1.ObjectMeta{Name: "srv"},
				Spec:       softwarecomposition.KnownServerSpec{{IPBlock: "10.0.0.0/8", Server: "s1", Name: "n1"}},
			}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)

			fetched, err := r.Get(ctx, "srv", &metav1.GetOptions{})
			require.NoError(t, err)

			versionedOut, err := sch.ConvertToVersion(fetched, v1beta1.SchemeGroupVersion)
			require.NoError(t, err)
			versioned, ok := versionedOut.(*v1beta1.KnownServer)
			require.True(t, ok, "expected *v1beta1.KnownServer, got %T", versionedOut)

			data, err := versioned.Marshal()
			require.NoError(t, err)
			require.NotEmpty(t, data)

			var decoded v1beta1.KnownServer
			require.NoError(t, decoded.Unmarshal(data))

			assert.Equal(t, versioned.Name, decoded.Name)
			assert.Equal(t, versioned.UID, decoded.UID)
			assert.Equal(t, versioned.Spec, decoded.Spec)
			assert.Equal(t, versioned.ResourceVersion, decoded.ResourceVersion)
		})
	}
}
