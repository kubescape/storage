package file

import (
	"context"
	"errors"
	"testing"

	"github.com/kubescape/storage/pkg/config"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestFetchNodesIncludesNotReadyNodes(t *testing.T) {
	ready := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "ready"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}},
	}
	notReady := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "not-ready"},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}},
	}
	client := fake.NewSimpleClientset(ready, notReady)
	nodes, err := NewKubernetesAPI(config.Config{}, client).FetchNodes(t.Context())
	require.NoError(t, err)
	require.ElementsMatch(t, []corev1.Node{*ready, *notReady}, nodes)
}

func TestFetchNodesPagination(t *testing.T) {
	for _, failSecondPage := range []bool{false, true} {
		name := "success"
		if failSecondPage {
			name = "error discards partial results"
		}
		t.Run(name, func(t *testing.T) {
			client := fake.NewSimpleClientset()
			listErr := errors.New("node listing unavailable")
			calls := 0
			client.PrependReactor("list", "nodes", func(action ktesting.Action) (bool, runtime.Object, error) {
				calls++
				opts := action.(ktesting.ListActionImpl).GetListOptions()
				require.Positive(t, opts.Limit)
				require.Empty(t, opts.FieldSelector)
				require.Empty(t, opts.LabelSelector)
				if calls == 1 {
					require.Empty(t, opts.Continue)
					return true, &corev1.NodeList{
						ListMeta: metav1.ListMeta{Continue: "next-page"},
						Items:    []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "first"}}},
					}, nil
				}
				require.Equal(t, 2, calls)
				require.Equal(t, "next-page", opts.Continue)
				if failSecondPage {
					return true, nil, listErr
				}
				return true, &corev1.NodeList{Items: []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: "second"}}}}, nil
			})
			nodes, err := NewKubernetesAPI(config.Config{}, client).FetchNodes(t.Context())
			require.Equal(t, 2, calls)
			if failSecondPage {
				require.ErrorIs(t, err, listErr)
				require.Nil(t, nodes)
				return
			}
			require.NoError(t, err)
			require.Len(t, nodes, 2)
			require.Equal(t, "first", nodes[0].Name)
			require.Equal(t, "second", nodes[1].Name)
		})
	}
}

func TestFetchNodesAPIError(t *testing.T) {
	client := fake.NewSimpleClientset()
	listErr := errors.New("forbidden to list nodes")
	client.PrependReactor("list", "nodes", func(ktesting.Action) (bool, runtime.Object, error) {
		return true, nil, listErr
	})
	nodes, err := NewKubernetesAPI(config.Config{}, client).FetchNodes(t.Context())
	require.ErrorIs(t, err, listErr)
	require.Nil(t, nodes)
}

func TestFetchNodesCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	nodes, err := NewKubernetesAPI(config.Config{}, fake.NewSimpleClientset()).FetchNodes(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, nodes)
}
