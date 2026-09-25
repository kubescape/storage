package file

import (
	"context"
	"strconv"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
