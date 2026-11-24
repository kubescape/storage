package file

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/endpoints/handlers/responsewriters"
	"k8s.io/apiserver/pkg/storage"
)

func TestContainerProfilePreSave_SizeAnnotations(t *testing.T) {
	for _, annotationState := range []string{"nil", "empty", "populated"} {
		for _, size := range []int{1, 2, 3} {
			t.Run(annotationState+"/size="+strconv.Itoa(size), func(t *testing.T) {
				profile := &softwarecomposition.ContainerProfile{
					ObjectMeta: metav1.ObjectMeta{Name: "profile", Namespace: "default"},
					Spec: softwarecomposition.ContainerProfileSpec{
						Syscalls: []string{"read", "write", "close"}[:size],
					},
				}
				if annotationState != "nil" {
					profile.Annotations = map[string]string{}
				}
				if annotationState == "populated" {
					profile.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Learning
					profile.Annotations["example.com/keep"] = "value"
				}
				processor := ContainerProfileProcessor{
					MaxContainerProfileSize: 2,
					ContainerProfileStorage: &fakeStorage{},
				}

				require.NotPanics(t, func() {
					require.NoError(t, processor.PreSave(context.Background(), profile))
				})
				require.Equal(t, strconv.Itoa(size), profile.Annotations[helpersv1.ResourceSizeMetadataKey])
				wantStatus := ""
				if annotationState == "populated" {
					wantStatus = helpersv1.Learning
					require.Equal(t, "value", profile.Annotations["example.com/keep"])
				}
				if size > processor.MaxContainerProfileSize {
					wantStatus = helpersv1.TooLarge
				}
				require.Equal(t, wantStatus, profile.Annotations[helpersv1.StatusMetadataKey])
				// Preserve the current count-cap behavior pending an owner decision.
				require.ElementsMatch(t, []string{"read", "write", "close"}[:size], profile.Spec.Syscalls)
			})
		}
	}
}

func TestCreateRejectsReportForTooLargeBase(t *testing.T) {
	f := loadDiffFixtures(t)
	for _, mode := range []string{"legacy", "single-writer", "sqlite-native"} {
		t.Run(mode, func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = mode != "legacy"
			t.Cleanup(func() { singleWriterEnabled = old })
			var b *backend
			if mode == "sqlite-native" {
				b = newObjectBackend(t, nil)
			} else {
				b = newLegacyBackend(t, nil)
			}
			defer b.watcher.Stop()
			b.processor.MaxContainerProfileSize = 1
			ctx := t.Context()
			base := f.plain(f.baseNm)
			base.Spec = softwarecomposition.ContainerProfileSpec{Syscalls: []string{"read", "close"}}
			saved := &softwarecomposition.ContainerProfile{}
			require.NoError(t, b.store.Create(ctx, f.baseKey, base, saved, 0))
			require.Equal(t, helpersv1.TooLarge, saved.Annotations[helpersv1.StatusMetadataKey])
			require.ElementsMatch(t, []string{"read", "close"}, saved.Spec.Syscalls)

			report := f.ts("r1", 1, helpersv1.Learning, helpersv1.Partial)
			report.Spec = softwarecomposition.ContainerProfileSpec{Syscalls: []string{"write"}}
			key := f.key(report.Name)
			err := b.store.Create(ctx, key, report, nil, 0)
			require.ErrorIs(t, err, ObjectTooLargeError)
			status := responsewriters.ErrorToAPIStatus(err)
			require.EqualValues(t, http.StatusConflict, status.Code)
			require.Equal(t, metav1.StatusReasonConflict, status.Reason)
			require.Equal(t, ObjectTooLargeError.Error(), status.Message)
			err = b.store.Get(ctx, key, storage.GetOptions{}, &softwarecomposition.ContainerProfile{})
			require.True(t, storage.IsNotFound(err), "get rejected report: %v", err)
			require.Zero(t, b.tsRows(t, f.baseKey))
		})
	}
}
