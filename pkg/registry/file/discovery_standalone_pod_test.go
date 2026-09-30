package file

import (
	"testing"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// Typed client-go strips TypeMeta from the objects it returns, so pod.Kind is
// empty at runtime. The running-pod wlid must not depend on it: a standalone
// pod's profile carries "namespace-<ns>/pod-<name>", and the cleanup keeps the
// profile only when that exact key is registered.
func TestKubernetesAPI_fetchDataFromPods_StandalonePodWlidWithoutTypeMeta(t *testing.T) {
	pod := &corev1.Pod{
		// no TypeMeta on purpose: this is what h.client.CoreV1().Pods(ns).List returns
		ObjectMeta: metav1.ObjectMeta{Name: "airbyte-worker", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "airbyte/worker:1"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	kubernetesAPI := NewKubernetesAPI(config.Config{}, fake.NewSimpleClientset(pod))
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}

	require.NoError(t, kubernetesAPI.fetchDataFromPods("default", &resourceMaps))

	containers, found := resourceMaps.RunningWlidsToContainerNames.Load("namespace-default/pod-airbyte-worker")
	require.True(t, found, "running standalone pod must be registered under its pod wlid, got keys: %v", resourceMaps.RunningWlidsToContainerNames.Keys())
	assert.True(t, containers.Contains("main"))

	profile := &metav1.ObjectMeta{
		Labels:      map[string]string{helpersv1.RelatedKindMetadataKey: "Pod"},
		Annotations: map[string]string{helpersv1.WlidMetadataKey: "wlid://cluster-kind-kind/namespace-default/pod-airbyte-worker"},
	}
	assert.False(t, deleteByTemplateHashOrWlid("", "", profile, resourceMaps), "profile of a running standalone pod must survive cleanup")
}
