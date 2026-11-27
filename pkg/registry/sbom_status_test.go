package registry_test

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/registry"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/sbomsyftfiltereds"
	"github.com/kubescape/storage/pkg/registry/softwarecomposition/sbomsyfts"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/generic"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

func sbomFields(obj runtime.Object) (*softwarecomposition.SBOMSyftSpec, *softwarecomposition.SBOMSyftStatus) {
	switch o := obj.(type) {
	case *softwarecomposition.SBOMSyft:
		return &o.Spec, &o.Status
	case *softwarecomposition.SBOMSyftFiltered:
		return &o.Spec, &o.Status
	default:
		panic("unexpected SBOM type")
	}
}

func TestSBOMStatusStores(t *testing.T) {
	type constructor func(*runtime.Scheme, storage.Interface, generic.RESTOptionsGetter) (*registry.REST, error)
	for _, tc := range []struct {
		name   string
		main   constructor
		status func(*runtime.Scheme, storage.Interface, generic.RESTOptionsGetter) (*registry.StatusREST, error)
		obj    runtime.Object
	}{
		{"sbomsyfts", sbomsyfts.NewREST, sbomsyfts.NewStatusREST, &softwarecomposition.SBOMSyft{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}},
		{"sbomsyftfiltereds", sbomsyftfiltereds.NewREST, sbomsyftfiltereds.NewStatusREST, &softwarecomposition.SBOMSyftFiltered{ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scheme := newDryRunTestScheme(t)
			backing := newDryRunTestStorage(t, scheme)
			main, err := tc.main(scheme, backing, newDryRunOptsGetter())
			require.NoError(t, err)
			status, err := tc.status(scheme, backing, newDryRunOptsGetter())
			require.NoError(t, err)
			_, creates := any(status).(rest.Creater)
			_, deletes := any(status).(rest.GracefulDeleter)
			require.False(t, creates, "status must not expose create")
			require.False(t, deletes, "status must not expose delete")
			ctx := genericapirequest.WithNamespace(genericapirequest.NewContext(), "default")
			key, err := main.KeyFunc(ctx, "test")
			require.NoError(t, err)
			statusKey, err := status.Store.KeyFunc(ctx, "test")
			require.NoError(t, err)
			require.Equal(t, key, statusKey, "status must use the migrated main storage prefix")
			spec, state := sbomFields(tc.obj)
			spec.Metadata.Tool.Name = "original"
			state.Status = "completed"
			created, err := main.Create(ctx, tc.obj, rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
			require.NoError(t, err)
			_, createdStatus := sbomFields(created)
			require.Empty(t, createdStatus.Status, "create must not set status")
			updated := created.DeepCopyObject()
			newSpec, newStatus := sbomFields(updated)
			newSpec.Metadata.Tool.Name = "must-not-change"
			*newStatus = softwarecomposition.SBOMSyftStatus{Status: "completed", StatusReason: "scan done", Limit: "maxImageSize"}
			expected := *newStatus
			for _, dry := range []bool{true, false} {
				options := &metav1.UpdateOptions{}
				if dry {
					options.DryRun = []string{metav1.DryRunAll}
				}
				result, _, err := status.Update(ctx, "test", rest.DefaultUpdatedObjectInfo(updated.DeepCopyObject()), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, options)
				require.NoError(t, err)
				gotSpec, gotStatus := sbomFields(result)
				require.Equal(t, "original", gotSpec.Metadata.Tool.Name)
				require.Equal(t, expected, *gotStatus)
				persisted, err := main.Get(ctx, "test", &metav1.GetOptions{})
				require.NoError(t, err)
				_, persistedStatus := sbomFields(persisted)
				if dry {
					require.Empty(t, persistedStatus.Status)
				} else {
					require.Equal(t, expected, *persistedStatus)
				}
			}
			persisted, err := main.Get(ctx, "test", &metav1.GetOptions{})
			require.NoError(t, err)
			changedSpec, changedStatus := sbomFields(persisted)
			changedSpec.Metadata.Tool.Name = "updated-main"
			changedStatus.Status = "failed"
			result, _, err := main.Update(ctx, "test", rest.DefaultUpdatedObjectInfo(persisted), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
			require.NoError(t, err)
			gotSpec, gotStatus := sbomFields(result)
			require.Equal(t, "updated-main", gotSpec.Metadata.Tool.Name)
			require.Equal(t, expected, *gotStatus, "main update must preserve status")
			invalid := result.DeepCopyObject()
			_, invalidStatus := sbomFields(invalid)
			invalidStatus.Status = "typo"
			_, _, err = status.Update(ctx, "test", rest.DefaultUpdatedObjectInfo(invalid), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
			require.ErrorContains(t, err, "status.status")
		})
	}
}
