package file

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// runningContainersOf answers FetchRunningContainers for a mock whose
// FetchResources ignores its namespace: what runs in one namespace is what
// runs in all of them.
func runningContainersOf(f ResourcesFetcher) (mapset.Set[string], mapset.Set[string], error) {
	resources, err := f.FetchResources(metav1.NamespaceAll)
	if err != nil {
		return nil, nil, err
	}
	return resources.RunningContainerImageIds, resources.RunningInstanceIds, nil
}

func (m ResourcesFetchMock) FetchRunningContainers(context.Context) (mapset.Set[string], mapset.Set[string], error) {
	return runningContainersOf(&m)
}

func (m containerProfileFetchMock) FetchRunningContainers(context.Context) (mapset.Set[string], mapset.Set[string], error) {
	return runningContainersOf(&m)
}

func (m staleWorkloadFetchMock) FetchRunningContainers(context.Context) (mapset.Set[string], mapset.Set[string], error) {
	return runningContainersOf(&m)
}

func (m loadCleanupFetcher) FetchRunningContainers(context.Context) (mapset.Set[string], mapset.Set[string], error) {
	return runningContainersOf(&m)
}

func (m acg2Fetcher) FetchRunningContainers(context.Context) (mapset.Set[string], mapset.Set[string], error) {
	return runningContainersOf(&m)
}

func (m nsFetchMock) FetchRunningContainers(context.Context) (mapset.Set[string], mapset.Set[string], error) {
	return runningContainersOf(&m)
}
