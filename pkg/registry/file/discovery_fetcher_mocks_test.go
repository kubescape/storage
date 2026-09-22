package file

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

func (ResourcesFetchMock) FetchNodes(context.Context) ([]corev1.Node, error) {
	return nil, nil
}

func (containerProfileFetchMock) FetchNodes(context.Context) ([]corev1.Node, error) {
	return nil, nil
}

func (staleWorkloadFetchMock) FetchNodes(context.Context) ([]corev1.Node, error) {
	return nil, nil
}

func (loadCleanupFetcher) FetchNodes(context.Context) ([]corev1.Node, error) {
	return nil, nil
}

func (acg2Fetcher) FetchNodes(context.Context) ([]corev1.Node, error) {
	return nil, nil
}

func (nsFetchMock) FetchNodes(context.Context) ([]corev1.Node, error) {
	return nil, nil
}
