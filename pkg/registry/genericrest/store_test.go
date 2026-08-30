package genericrest_test

import (
	"context"
	"fmt"
	"sync"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/options"
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
// (a mix of distinct keys and, for Update, repeated contention on the same
// key to force GuaranteedUpdate's retry-on-conflict path) against one shared
// *genericrest.Store, under -race. It asserts no data races and that every
// operation's own success/failure is internally consistent -- not a
// specific interleaving, since none is guaranteed.
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
			// transformer carries the current resourceVersion over from
			// oldObj on every GuaranteedUpdate retry attempt (mimicking a
			// real client's read-modify-write), rather than resubmitting a
			// stale one that would just fail validation instead of
			// exercising the conflict-retry path. GuaranteedUpdate's
			// conflict-retry must resolve this without corrupting the
			// object or racing internally, but a given attempt can
			// legitimately still lose to another writer and return a
			// conflict error after retries -- both outcomes are acceptable,
			// only a panic/race or a non-conflict error is not.
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
