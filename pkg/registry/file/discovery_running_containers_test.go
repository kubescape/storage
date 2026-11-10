package file

import (
	"errors"
	"testing"

	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

// runningPod returns a standalone pod in ns whose one container runs imageID.
func runningPod(ns, name, imageID string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: name + ":1"}}},
		Status: corev1.PodStatus{
			Phase:             corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{Name: "main", Image: name + ":1", ImageID: imageID}},
		},
	}
}

// FetchRunningContainers lists the pods of all namespaces page by page, and a
// failed page returns no sets rather than the pods read so far.
func TestFetchRunningContainersListsAllNamespacesByPage(t *testing.T) {
	for _, failSecondPage := range []bool{false, true} {
		name := "success"
		if failSecondPage {
			name = "error discards partial results"
		}
		t.Run(name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			listErr := errors.New("pod listing unavailable")
			calls := 0
			// assert, not require: the pager may call the lister off the test goroutine.
			client.PrependReactor("list", "pods", func(action ktesting.Action) (bool, runtime.Object, error) {
				calls++
				assert.Equal(t, metav1.NamespaceAll, action.GetNamespace())
				opts := action.(ktesting.ListActionImpl).GetListOptions()
				assert.Positive(t, opts.Limit, "the pager's page size must reach the API server")
				if calls == 1 {
					assert.Empty(t, opts.Continue)
					return true, &corev1.PodList{
						ListMeta: metav1.ListMeta{Continue: "next-page"},
						Items:    []corev1.Pod{*runningPod("apps", "web", "sha256:web")},
					}, nil
				}
				assert.Equal(t, "next-page", opts.Continue)
				if failSecondPage {
					return true, nil, listErr
				}
				return true, &corev1.PodList{Items: []corev1.Pod{*runningPod("jobs", "batch", "sha256:batch")}}, nil
			})

			imageIds, instanceIds, err := NewKubernetesAPI(config.Config{}, client).FetchRunningContainers(t.Context())
			require.Equal(t, 2, calls)
			if failSecondPage {
				require.ErrorIs(t, err, listErr)
				require.Nil(t, imageIds)
				require.Nil(t, instanceIds)
				return
			}
			require.NoError(t, err)
			assert.ElementsMatch(t, []string{"sha256:web", "sha256:batch"}, imageIds.ToSlice())
			assert.Equal(t, 2, instanceIds.Cardinality())
		})
	}
}
