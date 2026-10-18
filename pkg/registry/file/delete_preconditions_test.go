package file

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage"
)

// deleteBackend is one file-backed storage.Interface whose Delete must honour
// preconditions and the validator: the ungated StorageImpl, the StorageImpl
// sharing the write gate, and the SQLite ObjectStore.
type deleteBackend struct {
	name  string
	store storage.Interface
	key   func(name string) string
	// plainMissingIsNoop: with no preconditions and no validator, deleting an
	// absent key is a silent no-op on StorageImpl (existing behaviour, kept).
	plainMissingIsNoop bool
}

func newDeleteBackends(t *testing.T) []deleteBackend {
	t.Helper()
	enableSingleWriter(t)

	ungated, cleanup := newSingleWriterTestStorage(t)
	t.Cleanup(cleanup)

	pool := NewTestPool(t.TempDir())
	t.Cleanup(func() { _ = pool.Close() })
	armUngatedWriteCheck(t, pool)
	sch := runtime.NewScheme()
	install.Install(sch)
	gated := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch).(*StorageImpl)
	gate, err := NewWriteGate(t.Context(), pool)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, gate.Close()) })
	gated.SetWriteGate(gate)

	e := newObjectStoreEnv(t)
	return []deleteBackend{
		{"StorageImpl", ungated, testProfileKey, true},
		{"StorageImpl/gated", gated, testProfileKey, true},
		{"ObjectStore", e.store, e.key, false},
	}
}

func seedForDelete(t *testing.T, b deleteBackend, name string) (string, *softwarecomposition.ContainerProfile) {
	t.Helper()
	key := b.key(name)
	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1", UID: types.UID("uid-" + name)},
	}
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, b.store.Create(context.Background(), key, obj, out, 0))
	return key, out
}

func deleteErr(b deleteBackend, key string, pre *storage.Preconditions, validate storage.ValidateObjectFunc) (*softwarecomposition.ContainerProfile, error) {
	out := &softwarecomposition.ContainerProfile{}
	err := b.store.Delete(context.Background(), key, out, pre, validate, nil, storage.DeleteOptions{})
	return out, err
}

func requireStillStored(t *testing.T, b deleteBackend, key string, want *softwarecomposition.ContainerProfile) {
	t.Helper()
	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, b.store.Get(context.Background(), key, storage.GetOptions{}, got))
	assert.Equal(t, want.UID, got.UID)
	assert.Equal(t, want.ResourceVersion, got.ResourceVersion)
}

func requireDeleted(t *testing.T, b deleteBackend, key string) {
	t.Helper()
	err := b.store.Get(context.Background(), key, storage.GetOptions{}, &softwarecomposition.ContainerProfile{})
	require.Error(t, err)
	assert.True(t, storage.IsNotFound(err), "%v", err)
}

func TestDelete_Preconditions(t *testing.T) {
	for _, b := range newDeleteBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			t.Run("matching UID deletes", func(t *testing.T) {
				key, created := seedForDelete(t, b, "uid-ok")
				uid := created.UID
				out, err := deleteErr(b, key, &storage.Preconditions{UID: &uid}, nil)
				require.NoError(t, err)
				assert.Equal(t, created.UID, out.UID)
				requireDeleted(t, b, key)
			})
			t.Run("mismatched UID conflicts and preserves", func(t *testing.T) {
				key, created := seedForDelete(t, b, "uid-bad")
				other := types.UID("someone-else")
				_, err := deleteErr(b, key, &storage.Preconditions{UID: &other}, nil)
				require.Error(t, err)
				assert.True(t, storage.IsInvalidObj(err), "%v", err)
				requireStillStored(t, b, key, created)
			})
			t.Run("matching resourceVersion deletes", func(t *testing.T) {
				key, created := seedForDelete(t, b, "rv-ok")
				rv := created.ResourceVersion
				_, err := deleteErr(b, key, &storage.Preconditions{ResourceVersion: &rv}, nil)
				require.NoError(t, err)
				requireDeleted(t, b, key)
			})
			t.Run("stale resourceVersion conflicts and preserves", func(t *testing.T) {
				key, created := seedForDelete(t, b, "rv-stale")
				stale := created.ResourceVersion
				updated := &softwarecomposition.ContainerProfile{}
				require.NoError(t, b.store.GuaranteedUpdate(context.Background(), key, updated, false, nil, setLabel("a", "b"), nil))
				require.NotEqual(t, stale, updated.ResourceVersion)

				_, err := deleteErr(b, key, &storage.Preconditions{ResourceVersion: &stale}, nil)
				require.Error(t, err)
				assert.True(t, storage.IsInvalidObj(err), "%v", err)
				requireStillStored(t, b, key, updated)
			})
			t.Run("both preconditions matching delete", func(t *testing.T) {
				key, created := seedForDelete(t, b, "both-ok")
				uid, rv := created.UID, created.ResourceVersion
				_, err := deleteErr(b, key, &storage.Preconditions{UID: &uid, ResourceVersion: &rv}, nil)
				require.NoError(t, err)
				requireDeleted(t, b, key)
			})
		})
	}
}

func TestDelete_ValidateObjectFunc(t *testing.T) {
	for _, b := range newDeleteBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			t.Run("validator receives the stored object", func(t *testing.T) {
				key, created := seedForDelete(t, b, "validate-sees")
				var seen *softwarecomposition.ContainerProfile
				_, err := deleteErr(b, key, nil, func(_ context.Context, obj runtime.Object) error {
					seen = obj.(*softwarecomposition.ContainerProfile).DeepCopy()
					return nil
				})
				require.NoError(t, err)
				require.NotNil(t, seen)
				assert.Equal(t, created.Name, seen.Name)
				assert.Equal(t, created.UID, seen.UID)
				assert.Equal(t, created.ResourceVersion, seen.ResourceVersion)
				requireDeleted(t, b, key)
			})
			t.Run("validator error aborts and preserves", func(t *testing.T) {
				key, created := seedForDelete(t, b, "validate-deny")
				denied := errors.New("denied by validator")
				_, err := deleteErr(b, key, nil, func(context.Context, runtime.Object) error { return denied })
				require.ErrorIs(t, err, denied)
				requireStillStored(t, b, key, created)
			})
			t.Run("preconditions are checked before the validator", func(t *testing.T) {
				key, created := seedForDelete(t, b, "validate-order")
				other := types.UID("someone-else")
				var called bool
				_, err := deleteErr(b, key, &storage.Preconditions{UID: &other}, func(context.Context, runtime.Object) error {
					called = true
					return nil
				})
				require.Error(t, err)
				assert.True(t, storage.IsInvalidObj(err), "%v", err)
				assert.False(t, called, "validator must not run after a failed precondition")
				requireStillStored(t, b, key, created)
			})
		})
	}
}

func TestDelete_NilPreconditionsAndValidatorUnchanged(t *testing.T) {
	for _, b := range newDeleteBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			key, created := seedForDelete(t, b, "plain")
			out, err := deleteErr(b, key, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, created.UID, out.UID)
			requireDeleted(t, b, key)

			// An empty (non-nil) Preconditions constrains nothing.
			key2, _ := seedForDelete(t, b, "empty-pre")
			_, err = deleteErr(b, key2, &storage.Preconditions{}, nil)
			require.NoError(t, err)
			requireDeleted(t, b, key2)
		})
	}
}

func TestDelete_MissingKeyIsNotFound(t *testing.T) {
	uid := types.UID("x")
	for _, b := range newDeleteBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			key := b.key("never-created")
			for name, tc := range map[string]struct {
				pre      *storage.Preconditions
				validate storage.ValidateObjectFunc
			}{
				"nil":           {},
				"preconditions": {pre: &storage.Preconditions{UID: &uid}},
				"validator": {validate: func(context.Context, runtime.Object) error {
					t.Error("validator must not run for a missing object")
					return nil
				}},
			} {
				_, err := deleteErr(b, key, tc.pre, tc.validate)
				if name == "nil" && b.plainMissingIsNoop {
					assert.NoError(t, err)
					continue
				}
				require.Error(t, err, name)
				assert.True(t, storage.IsNotFound(err), "%s: %v", name, err)
			}
		})
	}
}

// TestDelete_StaleResourceVersionNeverDeletesChangedObject hammers a key with
// updates while deleters hold a resourceVersion that is already stale: none of
// them may delete the object, whatever the interleaving.
func TestDelete_StaleResourceVersionNeverDeletesChangedObject(t *testing.T) {
	for _, b := range newDeleteBackends(t) {
		t.Run(b.name, func(t *testing.T) {
			ctx := context.Background()
			key, created := seedForDelete(t, b, "stale-race")
			stale := created.ResourceVersion
			require.NoError(t, b.store.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, false, nil, setLabel("n", "0"), nil))

			var wg sync.WaitGroup
			var deleted atomic.Int32
			for i := 0; i < 4; i++ {
				wg.Add(2)
				go func() {
					defer wg.Done()
					for j := 0; j < 5; j++ {
						_ = b.store.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, false, nil, setLabel("n", "x"), nil)
					}
				}()
				go func() {
					defer wg.Done()
					for j := 0; j < 5; j++ {
						rv := stale
						if err := b.store.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, &storage.Preconditions{ResourceVersion: &rv}, nil, nil, storage.DeleteOptions{}); err == nil {
							deleted.Add(1)
						}
					}
				}()
			}
			wg.Wait()
			assert.Zero(t, deleted.Load(), "a delete with a stale resourceVersion succeeded")
			got := &softwarecomposition.ContainerProfile{}
			require.NoError(t, b.store.Get(ctx, key, storage.GetOptions{}, got))
			assert.Equal(t, created.UID, got.UID)
		})
	}
}

// TestObjectStore_Delete_ConcurrentUpdateRetriesAgainstFreshState drives the
// window between the ObjectStore's read-and-validate and its DELETE: the
// validator updates the row, so the DELETE's CAS must fail and the delete must
// be re-evaluated against the new object rather than removing it on stale state.
// StorageImpl has no equivalent window: it validates under the per-key write
// lock every writer takes to commit, and a validator that wrote the same key
// would deadlock on that lock by design.
func TestObjectStore_Delete_ConcurrentUpdateRetriesAgainstFreshState(t *testing.T) {
	ctx := context.Background()

	t.Run("no precondition: re-validates the updated object then deletes", func(t *testing.T) {
		e := newObjectStoreEnv(t)
		key := e.key("retry-ok")
		e.create(e.plain("retry-ok"))

		var calls int
		var labelOnLast string
		err := e.store.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, nil, func(_ context.Context, obj runtime.Object) error {
			calls++
			labelOnLast = obj.(*softwarecomposition.ContainerProfile).Labels["a"]
			if calls == 1 {
				return e.store.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, false, nil, setLabel("a", "b"), nil)
			}
			return nil
		}, nil, storage.DeleteOptions{})
		require.NoError(t, err)
		assert.Equal(t, 2, calls, "the changed row must be validated again")
		assert.Equal(t, "b", labelOnLast, "the retry must validate the updated object, not the stale one")
		_, getErr := e.get(key)
		assert.True(t, storage.IsNotFound(getErr), "%v", getErr)
	})

	t.Run("resourceVersion precondition: becomes a conflict, object preserved", func(t *testing.T) {
		e := newObjectStoreEnv(t)
		key := e.key("retry-conflict")
		created := e.create(e.plain("retry-conflict"))
		rv := created.ResourceVersion

		var calls int
		err := e.store.Delete(ctx, key, &softwarecomposition.ContainerProfile{}, &storage.Preconditions{ResourceVersion: &rv}, func(context.Context, runtime.Object) error {
			calls++
			if calls == 1 {
				return e.store.GuaranteedUpdate(ctx, key, &softwarecomposition.ContainerProfile{}, false, nil, setLabel("a", "b"), nil)
			}
			return nil
		}, nil, storage.DeleteOptions{})
		require.Error(t, err)
		assert.True(t, storage.IsInvalidObj(err), "%v", err)
		assert.Equal(t, 1, calls, "the stale resourceVersion must fail before the validator runs again")
		got := e.mustGet(key)
		assert.Equal(t, "b", got.Labels["a"])
		assert.NotEqual(t, rv, got.ResourceVersion)
	})
}
