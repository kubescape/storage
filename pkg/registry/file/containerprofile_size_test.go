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

func TestContainerProfilePreSave_DNSCountsNamesProcessesAndAddresses(t *testing.T) {
	profile := &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "dns-profile"}, Spec: softwarecomposition.ContainerProfileSpec{DNSBaselineVersion: "v1", DNSLookups: []softwarecomposition.DNSLookup{{Name: "example.com.", ExePaths: []string{"/app", "/usr/bin/python3"}, Addresses: []string{"192.0.2.1"}}}}}
	processor := ContainerProfileProcessor{MaxContainerProfileSize: 3, ContainerProfileStorage: &fakeStorage{}}
	require.NoError(t, processor.PreSave(t.Context(), profile))
	require.Equal(t, "4", profile.Annotations[helpersv1.ResourceSizeMetadataKey])
	require.Equal(t, helpersv1.TooLarge, profile.Annotations[helpersv1.StatusMetadataKey])
}

func TestContainerProfilePreSave_DNSCountsContainerGroups(t *testing.T) {
	for _, group := range []string{"containers", "initContainers", "ephemeralContainers", "all"} {
		t.Run(group, func(t *testing.T) {
			section := softwarecomposition.ContainerProfileContainer{Name: "app", DNSBaselineVersion: "v1", DNSLookups: []softwarecomposition.DNSLookup{{Name: "example.com.", ExePaths: []string{"/app", "/usr/bin/python3"}, Addresses: []string{"192.0.2.1"}}}}
			profile := &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "grouped-dns"}}
			if group == "containers" || group == "all" {
				profile.Spec.Containers = []softwarecomposition.ContainerProfileContainer{section}
			}
			if group == "initContainers" || group == "all" {
				profile.Spec.InitContainers = []softwarecomposition.ContainerProfileContainer{section}
			}
			if group == "ephemeralContainers" || group == "all" {
				profile.Spec.EphemeralContainers = []softwarecomposition.ContainerProfileContainer{section}
			}
			count := 4
			if group == "all" {
				count = 12
			}
			processor := ContainerProfileProcessor{MaxContainerProfileSize: count - 1, ContainerProfileStorage: &fakeStorage{}}
			require.NoError(t, processor.PreSave(t.Context(), profile))
			require.Equal(t, strconv.Itoa(count), profile.Annotations[helpersv1.ResourceSizeMetadataKey])
			require.Equal(t, helpersv1.TooLarge, profile.Annotations[helpersv1.StatusMetadataKey])
		})
	}
}
