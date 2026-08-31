package genericrest_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/kubescape/storage/pkg/registry/genericrest"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/knownservers"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage"
)

// This file is a package-level test suite for genericrest.Store itself, as
// opposed to only exercising it indirectly through each resource package's
// own differential suite (knownservers/openvulnerabilityexchange). Those
// suites cover per-resource behavior; this one covers what's genuinely
// generic to the Store type, most notably concurrent access to a single
// shared instance -- Store has no mutable state after construction and each
// Update's tryUpdate closure runs synchronously per call (per code
// inspection), so TestStore_ConcurrentAccess exists to prove that under
// real concurrent traffic (with -race) rather than by inspection alone.
//
// Built on knownservers' real wiring (NewCustomREST, a genericrest.Store
// under a type alias) rather than hand-rolling a minimal Strategy, since
// knownservers is already the simplest real consumer (cluster-scoped,
// no-op strategy).

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sch := runtime.NewScheme()
	install.Install(sch)
	return sch
}

func newTestStore(t *testing.T) *genericrest.Store {
	t.Helper()
	sch := newTestScheme(t)
	fs := afero.NewMemMapFs()
	pool := file.NewTestPool(t.TempDir())
	t.Cleanup(func() { _ = pool.Close() })
	storageImpl := file.NewStorageImpl(fs, file.DefaultStorageRoot, pool, nil, sch)
	optsGetter := &options.StorageFactoryRestOptionsFactory{StorageFactory: &options.SimpleStorageFactory{}}

	store, err := knownservers.NewCustomREST(sch, storageImpl, optsGetter)
	require.NoError(t, err)
	return store
}

func testContext() context.Context {
	return genericapirequest.WithNamespace(genericapirequest.NewContext(), "")
}

// TestStore_ConcurrentAccess runs concurrent Create/Get/Update/Delete calls
// (a mix of distinct keys, and repeated Update contention on one shared key)
// against one shared *genericrest.Store, under -race. It asserts no data
// races and that every operation's own success/failure is internally
// consistent -- not a specific interleaving, since none is guaranteed.
//
// Note: the contended-key Updates below do NOT exercise
// GuaranteedUpdate's optimistic-concurrency retry loop -- StorageImpl
// serializes all writers to a given key on a single per-key mutex held for
// the whole GuaranteedUpdate call, so concurrent Update calls for the same
// key never actually interleave; each one completes with exactly one
// tryUpdate invocation and no conflict. What this still genuinely covers:
// concurrent access to a single shared Store instance (Store itself has no
// mutable state after construction) and Update's own correctness under
// contention (no torn writes, no data race), just not conflict-retry.
func TestStore_ConcurrentAccess(t *testing.T) {
	store := newTestStore(t)
	ctx := testContext()

	const workers = 20
	const contendedName = "contended"

	// Seed the contended object that every worker will race to update.
	_, err := store.Create(ctx, &softwarecomposition.KnownServer{
		ObjectMeta: metav1.ObjectMeta{Name: contendedName},
	}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// Distinct key: Create, Get, then Delete -- exercises the
			// non-contended path concurrently across workers.
			name := fmt.Sprintf("worker-%02d", i)
			_, err := store.Create(ctx, &softwarecomposition.KnownServer{
				ObjectMeta: metav1.ObjectMeta{Name: name},
			}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			assert.NoError(t, err)

			_, err = store.Get(ctx, name, &metav1.GetOptions{})
			assert.NoError(t, err)

			_, _, err = store.Delete(ctx, name, rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
			assert.NoError(t, err)

			// Contended key: every worker updates the same object. The
			// transformer builds the new object from oldObj (carrying over
			// its current resourceVersion) rather than submitting a
			// pre-built static object with no resourceVersion, which the
			// strategy's AllowUnconditionalUpdate()==false would just
			// reject as invalid. In practice each worker's Update is fully
			// serialized by StorageImpl's per-key mutex (see the doc
			// comment above), so no worker should ever actually see a
			// conflict here -- but tolerate one anyway rather than assert
			// zero-conflict, since that's not a contract this test needs to
			// pin down; only a panic/race or a non-conflict error is not
			// acceptable.
			newServer := fmt.Sprintf("server-%02d", i)
			_, _, err = store.Update(ctx, contendedName,
				rest.DefaultUpdatedObjectInfo(nil, func(_ context.Context, _, oldObj runtime.Object) (runtime.Object, error) {
					old := oldObj.(*softwarecomposition.KnownServer)
					updated := old.DeepCopy()
					updated.Spec = softwarecomposition.KnownServerSpec{{Server: newServer}}
					return updated, nil
				}),
				rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
			if err != nil {
				assert.True(t, apierrors.IsConflict(err), "unexpected non-conflict error: %v", err)
			}
		}(i)
	}
	wg.Wait()

	// The contended object must still exist and be internally consistent
	// (exactly one worker's write, or the seed value, won -- not a torn
	// write from two updates interleaving).
	final, err := store.Get(ctx, contendedName, &metav1.GetOptions{})
	require.NoError(t, err)
	server := final.(*softwarecomposition.KnownServer)
	if len(server.Spec) > 0 {
		assert.Regexp(t, `^server-\d{2}$`, server.Spec[0].Server)
	}
}

// countingDeleteStorage wraps a storage.Interface, invoking cancel once its
// Delete method has been called cancelAfter times. Used to deterministically
// cancel a context partway through a bulk delete without depending on
// rest.ValidateObjectFunc (StorageImpl.Delete ignores that parameter
// entirely -- ValidateObjectFunc is not a usable interception point here).
type countingDeleteStorage struct {
	storage.Interface
	count       int32
	cancelAfter int32
	cancel      context.CancelFunc
}

func (c *countingDeleteStorage) Delete(ctx context.Context, key string, out runtime.Object, preconditions *storage.Preconditions, validateDeletion storage.ValidateObjectFunc, cachedExistingObject runtime.Object, opts storage.DeleteOptions) error {
	// Cancel only after the counted delete has itself already completed
	// successfully -- cancelling first would make that very call (not just
	// the next one) race against an already-cancelled ctx.
	err := c.Interface.Delete(ctx, key, out, preconditions, validateDeletion, cachedExistingObject, opts)
	if err == nil && atomic.AddInt32(&c.count, 1) == c.cancelAfter {
		c.cancel()
	}
	return err
}

// TestStore_DeleteCollection_StopsOnCancellationBetweenPages proves the fix
// for DeleteCollection's implicit reliance on List's old default page size as
// its cancellation checkpoint (see genericrest.DefaultDeleteCollectionPageSize's
// doc comment). DeleteCollection's only cancellation check is the ctx.Done()
// select at the top of its pagination loop -- once per page, not once per
// item. Since List (StorageImpl.GetList) itself was fixed to return every
// matching item in one call when no Limit is requested, a "delete all" call
// with no explicit Limit would list and delete an entire large collection in
// a single, uninterruptible pass unless DeleteCollection requests its own
// bounded page size independent of List's behavior.
//
// This creates more than DefaultDeleteCollectionPageSize objects (all with
// distinct names, so no per-item lock contention -- MapMutex.Lock's fast path
// for an uncontended key does not itself check ctx, see pkg/utils/mutex.go,
// so cancellation here can only be caught by DeleteCollection's own
// between-pages check, not incidentally by lock acquisition), cancels the
// context a few real storage deletes into the first page via
// countingDeleteStorage, and asserts DeleteCollection stops with a context
// error having deleted only the first page -- not the whole collection.
func TestStore_DeleteCollection_StopsOnCancellationBetweenPages(t *testing.T) {
	store := newTestStore(t)
	base := testContext()

	const total = genericrest.DefaultDeleteCollectionPageSize + 20
	for i := range total {
		name := fmt.Sprintf("server-%04d", i)
		_, err := store.Create(base, &softwarecomposition.KnownServer{
			ObjectMeta: metav1.ObjectMeta{Name: name},
		}, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
		require.NoError(t, err)
	}

	ctx, cancel := context.WithCancel(base)
	defer cancel()

	// Cancel exactly once the first page's real deletes have all completed
	// successfully (not partway through), so the only thing left to prove is
	// whether the *next* iteration's top-of-loop ctx.Done() check fires
	// before a second List/Delete round -- isolated from any incidental
	// cancellation-awareness inside an individual Delete call's own
	// connection acquisition (which, if cancellation happened mid-page
	// instead, would confound which mechanism actually stopped the loop).
	const cancelAfter = genericrest.DefaultDeleteCollectionPageSize
	store.Storage = &countingDeleteStorage{Interface: store.Storage, cancelAfter: cancelAfter, cancel: cancel}

	_, err := store.DeleteCollection(ctx, rest.ValidateAllObjectFunc, &metav1.DeleteOptions{}, &metainternalversion.ListOptions{})
	require.Error(t, err, "DeleteCollection must stop once its context is cancelled, not run the whole collection to completion")
	assert.True(t, errors.Is(err, context.Canceled), "expected a context-cancellation error, got: %v", err)

	remainingList, err := store.List(base, &metainternalversion.ListOptions{Limit: int64(total) + 1})
	require.NoError(t, err)
	remaining, err := meta.ExtractList(remainingList)
	require.NoError(t, err)
	assert.Len(t, remaining, total-genericrest.DefaultDeleteCollectionPageSize,
		"only the first page should have been deleted before cancellation was noticed at the top of the second page")
}
