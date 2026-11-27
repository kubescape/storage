package file

// Round-trip a user-authored multi-container ContainerProfile (subtype groups)
// through the REAL StorageImpl (gob persistence + processor) and through the
// internal<->v1beta1 conversion, asserting the groups survive both. Diagnoses
// the component-test observation of an adopted grouped document arriving with
// empty groups.

import (
	"context"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
)

func groupedUserCP(ns, name string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   ns,
			Annotations: map[string]string{"kubescape.io/managed-by": "User"},
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Architectures: []string{"amd64"},
			Containers: []softwarecomposition.ContainerProfileContainer{
				{Name: "app", Execs: []softwarecomposition.ExecCalls{{Path: "/usr/bin/id", Args: []string{"/usr/bin/id"}}}},
			},
			InitContainers: []softwarecomposition.ContainerProfileContainer{
				{Name: "setup", Execs: []softwarecomposition.ExecCalls{{Path: "/bin/sh", Args: []string{"/bin/sh", "-c", "x"}}}},
			},
			EphemeralContainers: []softwarecomposition.ContainerProfileContainer{
				{Name: "debug", Execs: []softwarecomposition.ExecCalls{{Path: "/usr/bin/whoami", Args: []string{"/usr/bin/whoami"}}}},
			},
		},
	}
}

// TestUserCP_SubtypeGroups_SurviveFileStorage pins the gob-persistence layer.
func TestUserCP_SubtypeGroups_SurviveFileStorage(t *testing.T) {
	const ns, name = "kubescape", "mc37"
	s, _ := newGuardTestStorage(t, ns, name)
	key := "/spdx.softwarecomposition.kubescape.io/containerprofiles/" + ns + "/" + name

	ctx := context.TODO()
	require.NoError(t, s.Create(ctx, key, groupedUserCP(ns, name), &softwarecomposition.ContainerProfile{}, 0))

	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, got))

	require.Len(t, got.Spec.Containers, 1, "containers group must survive file storage")
	require.Len(t, got.Spec.InitContainers, 1, "initContainers group must survive file storage")
	require.Len(t, got.Spec.EphemeralContainers, 1, "ephemeralContainers group must survive file storage")
	assert.Equal(t, "app", got.Spec.Containers[0].Name)
	assert.Equal(t, "/usr/bin/id", got.Spec.Containers[0].Execs[0].Path)
	assert.Equal(t, "setup", got.Spec.InitContainers[0].Name)
	assert.Equal(t, "debug", got.Spec.EphemeralContainers[0].Name)
}

// TestUserCP_SubtypeGroups_SurviveConversion pins the internal<->v1beta1
// conversion the aggregated apiserver performs on serve.
func TestUserCP_SubtypeGroups_SurviveConversion(t *testing.T) {
	internal := groupedUserCP("kubescape", "mc37")

	versioned := &v1beta1.ContainerProfile{}
	require.NoError(t, scheme.Scheme.Convert(internal, versioned, nil))
	require.Len(t, versioned.Spec.Containers, 1, "containers group must survive internal->v1beta1")
	require.Len(t, versioned.Spec.InitContainers, 1)
	require.Len(t, versioned.Spec.EphemeralContainers, 1)
	assert.Equal(t, "/usr/bin/id", versioned.Spec.Containers[0].Execs[0].Path)

	back := &softwarecomposition.ContainerProfile{}
	require.NoError(t, scheme.Scheme.Convert(versioned, back, nil))
	require.Len(t, back.Spec.Containers, 1, "containers group must survive v1beta1->internal")
	require.Len(t, back.Spec.InitContainers, 1)
	require.Len(t, back.Spec.EphemeralContainers, 1)
}
