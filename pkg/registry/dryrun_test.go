package registry_test

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/generatednetworkpolicy"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/knownservers"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/vulnerabilitymanifestsummary"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/options"
	"k8s.io/apiserver/pkg/storage"
)

// TestDryRunDoesNotPanic is the regression test for Bug 1 (see
// .omc/plans/storage-locking-rewrite.md and pkg/registry/registry.go's
// NewCodec doc comment): every softwarecomposition/*/etcd.go's NewREST used
// to wire genericregistry.DryRunnableStorage{Codec: nil, ...}, and since
// each of these NewREST functions pre-sets Storage.Storage to the non-nil
// storageImpl before calling Store.CompleteWithOptions, that nil Codec was
// NEVER overwritten by opts.StorageConfig.Codec (vendor store.go's
// CompleteWithOptions only assigns it when Storage.Storage is nil) -- so any
// real dry-run Create/Update against ANY resource panicked inside
// DryRunnableStorage.copyInto's runtime.Encode(s.Codec, in) / s.Codec.Decode
// (nil-interface method call), surfaced to clients as an opaque 500 by the
// apiserver's panic-recovery middleware.
//
// This test exercises the real NewREST constructors (not a hand-rolled
// substitute) for three representative resources -- one cluster-scoped
// (knownservers), two namespaced (generatednetworkpolicy,
// vulnerabilitymanifestsummary) -- and performs a genuine dry-run Create and
// Update through the constructed rest.Storage, which is what actually
// invokes DryRunnableStorage.Create/GuaranteedUpdate's dry-run branches.
func TestDryRunDoesNotPanic(t *testing.T) {
	for _, tc := range []struct {
		name      string
		namespace string
		newREST   func(scheme *runtime.Scheme, s storage.Interface, o generic.RESTOptionsGetter) (rest.StandardStorage, error)
		newObj    func(name string) runtime.Object
	}{
		{
			name:      "knownservers",
			namespace: "", // cluster-scoped
			newREST: func(scheme *runtime.Scheme, s storage.Interface, o generic.RESTOptionsGetter) (rest.StandardStorage, error) {
				return knownservers.NewREST(scheme, s, o)
			},
			newObj: func(name string) runtime.Object {
				return &softwarecomposition.KnownServer{
					ObjectMeta: metav1.ObjectMeta{Name: name},
					Spec:       softwarecomposition.KnownServerSpec{{IPBlock: "10.0.0.0/8", Server: "s1", Name: "n1"}},
				}
			},
		},
		{
			name:      "generatednetworkpolicy",
			namespace: "default",
			newREST: func(scheme *runtime.Scheme, s storage.Interface, o generic.RESTOptionsGetter) (rest.StandardStorage, error) {
				return generatednetworkpolicy.NewREST(scheme, s, o)
			},
			newObj: func(name string) runtime.Object {
				return &softwarecomposition.GeneratedNetworkPolicy{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				}
			},
		},
		{
			name:      "vulnerabilitymanifestsummary",
			namespace: "default",
			newREST: func(scheme *runtime.Scheme, s storage.Interface, o generic.RESTOptionsGetter) (rest.StandardStorage, error) {
				return vulnerabilitymanifestsummary.NewREST(scheme, s, o)
			},
			newObj: func(name string) runtime.Object {
				return &softwarecomposition.VulnerabilityManifestSummary{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sch := newDryRunTestScheme(t)
			r, err := tc.newREST(sch, newDryRunTestStorage(t, sch), newDryRunOptsGetter())
			require.NoError(t, err)

			ctx := genericapirequest.WithNamespace(genericapirequest.NewContext(), tc.namespace)
			objName := "dry-run-obj"
			obj := tc.newObj(objName)

			// --- Dry-run Create must not panic and must not persist. ---
			var createOut runtime.Object
			assert.NotPanics(t, func() {
				createOut, err = r.Create(ctx, obj.DeepCopyObject(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
			}, "dry-run Create must not panic (nil Codec regression)")
			require.NoError(t, err)
			createOutMeta, err := meta.Accessor(createOut)
			require.NoError(t, err)
			assert.Equal(t, objName, createOutMeta.GetName())

			_, err = r.Get(ctx, objName, &metav1.GetOptions{})
			assert.True(t, apierrors.IsNotFound(err), "dry-run create must not actually persist")

			// --- Real create, so there is something to dry-run update. ---
			created, err := r.Create(ctx, obj.DeepCopyObject(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)
			createdMeta, err := meta.Accessor(created)
			require.NoError(t, err)

			updated := created.DeepCopyObject()
			updatedMeta, err := meta.Accessor(updated)
			require.NoError(t, err)
			labels := updatedMeta.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels["dry-run-changed"] = "true"
			updatedMeta.SetLabels(labels)

			// --- Dry-run Update must not panic and must not persist. ---
			var updateOut runtime.Object
			assert.NotPanics(t, func() {
				updateOut, _, err = r.Update(ctx, createdMeta.GetName(), rest.DefaultUpdatedObjectInfo(updated), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{DryRun: []string{metav1.DryRunAll}})
			}, "dry-run Update must not panic (nil Codec regression)")
			require.NoError(t, err)
			updateOutMeta, err := meta.Accessor(updateOut)
			require.NoError(t, err)
			assert.Equal(t, "true", updateOutMeta.GetLabels()["dry-run-changed"], "dry-run update's own return value should reflect the update")

			fresh, err := r.Get(ctx, createdMeta.GetName(), &metav1.GetOptions{})
			require.NoError(t, err)
			freshMeta, err := meta.Accessor(fresh)
			require.NoError(t, err)
			assert.NotEqual(t, "true", freshMeta.GetLabels()["dry-run-changed"], "dry-run update must not actually persist")
		})
	}
}

func newDryRunTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	sch := runtime.NewScheme()
	install.Install(sch)
	return sch
}

func newDryRunTestStorage(t *testing.T, scheme *runtime.Scheme) storage.Interface {
	t.Helper()
	fs := afero.NewMemMapFs()
	pool := file.NewTestPool(t.TempDir())
	t.Cleanup(func() { _ = pool.Close() })
	return file.NewStorageImpl(fs, file.DefaultStorageRoot, pool, nil, scheme)
}

func newDryRunOptsGetter() *options.StorageFactoryRestOptionsFactory {
	return &options.StorageFactoryRestOptionsFactory{StorageFactory: &options.SimpleStorageFactory{}}
}
