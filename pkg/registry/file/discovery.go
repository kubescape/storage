package file

import (
	"context"
	"fmt"

	wlidPkg "github.com/armosec/utils-k8s-go/wlid"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1"
	"github.com/kubescape/k8s-interface/k8sinterface"
	"github.com/kubescape/storage/pkg/config"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"
	"zombiezen.com/go/sqlite"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	Workloads = mapset.NewSet[string]([]string{
		"cronjob",
		"daemonset",
		"deployment",
		"job",
		"replicaset",
		"statefulset",
	}...) // FIXME put in a configmap
)

type ResourcesFetcher interface {
	FetchResources(ns string) (ResourceMaps, error)
	ListNamespaces(conn *sqlite.Conn) ([]string, error)
}

type KubernetesAPI struct {
	cfg    config.Config
	client *kubernetes.Clientset
}

func NewKubernetesAPI(cfg config.Config, client *kubernetes.Clientset) *KubernetesAPI {
	return &KubernetesAPI{
		cfg:    cfg,
		client: client,
	}
}

var _ ResourcesFetcher = (*KubernetesAPI)(nil)

// ResourceMaps is a map of running resources in the cluster, based on these maps we can decide which files to delete
type ResourceMaps struct {
	// CLUSTER level
	RunningContainerImageIds mapset.Set[string]
	RunningTemplateHash      mapset.Set[string]
	// NAMESPACE level
	RunningInstanceIds           mapset.Set[string]
	RunningWlidsToContainerNames *maps.SafeMap[string, mapset.Set[string]]
	// FIXME add nodes
	// FIXME how about hosts?
}

func (h *KubernetesAPI) ListNamespaces(conn *sqlite.Conn) ([]string, error) {
	var namespaces []string
	dbNamespaces, err := listNamespaces(conn)
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces from db: %w", err)
	}
	for _, ns := range dbNamespaces {
		// exclude kubescape namespace - TODO enable it again when we move the cluster-scoped resources
		if ns == h.cfg.DefaultNamespace {
			continue
		}
		namespaces = append(namespaces, ns)
	}
	return namespaces, nil
}

// FetchResources builds a map of running resources in the cluster needed for cleanup
func (h *KubernetesAPI) FetchResources(ns string) (ResourceMaps, error) {
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}

	if err := h.fetchDataFromPods(ns, &resourceMaps); err != nil {
		return resourceMaps, fmt.Errorf("failed to fetch instance ids and image ids from running pods: %w", err)
	}

	if err := h.fetchDataFromWorkloads(ns, &resourceMaps); err != nil {
		return resourceMaps, fmt.Errorf("failed to fetch wlids from running workloads: %w", err)
	}

	return resourceMaps, nil
}

func (h *KubernetesAPI) chooseLister(ns string, kind string, opts metav1.ListOptions) (runtime.Object, error) {
	switch kind {
	case "cronjob":
		return h.client.BatchV1().CronJobs(ns).List(context.Background(), opts)
	case "daemonset":
		return h.client.AppsV1().DaemonSets(ns).List(context.Background(), opts)
	case "deployment":
		return h.client.AppsV1().Deployments(ns).List(context.Background(), opts)
	case "job":
		return h.client.BatchV1().Jobs(ns).List(context.Background(), opts)
	case "replicaset":
		return h.client.AppsV1().ReplicaSets(ns).List(context.Background(), opts)
	case "statefulset":
		return h.client.AppsV1().StatefulSets(ns).List(context.Background(), opts)
	}
	return nil, errors.NewNotFound(schema.GroupResource{Resource: kind}, "not implemented")
}

// fetchDataFromWorkloads iterates through a predefined list of Kubernetes workload types (e.g., Deployment, StatefulSet).
// For each running workload instance, it extracts:
// 1. The Template Hash, which links a workload to its pods.
// 2. A mapping from the workload's unique ID (WLID) to the names of all containers (main, init, ephemeral) defined in its spec.
// This data is used to identify which stored profiles correspond to currently active workloads.
func (h *KubernetesAPI) fetchDataFromWorkloads(ns string, resourceMaps *ResourceMaps) error {
	for _, resource := range Workloads.ToSlice() {
		gvr, err := k8sinterface.GetGroupVersionResource(resource)
		if err != nil {
			return fmt.Errorf("failed to get group version resource for %s: %w", resource, err)
		}

		err = pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			return h.chooseLister(ns, resource, opts)
		}).EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
			runtimeObj := obj.(runtime.Object)
			meta := obj.(metav1.Object)
			gvk, err := instanceidhandler.GetGvkFromRuntimeObj(runtimeObj)
			if err != nil {
				return fmt.Errorf("failed to get gvk from workload %s: %w", meta.GetName(), err)
			}

			instanceIds, err := instanceidhandler.GenerateInstanceIDFromRuntimeObj(runtimeObj, h.cfg.ExcludeJsonPaths)
			if err != nil {
				return fmt.Errorf("failed to generate instance id for workload %s: %w", meta.GetName(), err)
			}
			for _, instanceId := range instanceIds {
				if templateHash := instanceId.GetTemplateHash(); templateHash != "" {
					resourceMaps.RunningTemplateHash.Add(instanceId.GetTemplateHash())
					break // templateHash is the same for every instanceId
				}
			}

			// we don't care about the cluster name, so we remove it to avoid corner cases
			wlid := wlidPkg.GetK8sWLID("", meta.GetNamespace(), gvk.Kind, meta.GetName())
			wlid = wlidWithoutClusterName(wlid)

			containerNames := mapset.NewSet[string]()
			resourceMaps.RunningWlidsToContainerNames.Set(wlid, containerNames)

			podSpec, err := instanceidhandler.GetPodSpecFromRuntimeObj(runtimeObj)
			if err != nil {
				return fmt.Errorf("failed to get pod spec from workload %s: %w", wlid, err)
			}

			for _, containers := range [][]corev1.Container{podSpec.Containers, podSpec.InitContainers, convertEphemeralToContainers(podSpec.EphemeralContainers)} {
				for _, container := range containers {
					containerNames.Add(container.Name)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to list %s: %w", gvr, err)
		}
	}
	return nil
}

func convertEphemeralToContainers(e []corev1.EphemeralContainer) []corev1.Container {
	c := make([]corev1.Container, len(e))
	for i := range e {
		c[i] = corev1.Container(e[i].EphemeralContainerCommon)
	}
	return c
}

// fetchDataFromPods iterates over all running pods in the cluster.
// For each pod, it extracts:
// 1. Instance IDs, which uniquely identify a container instance.
// 2. Template Hashes, used to group pods belonging to the same controller revision.
// 3. Container Image IDs, which are the unique identifiers for the container images.
// It populates the corresponding sets in the ResourceMaps struct.
func (h *KubernetesAPI) fetchDataFromPods(ns string, resourceMaps *ResourceMaps) error {
	if err := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return h.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{})
	}).EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
		pod := obj.(*corev1.Pod)

		instanceIds, err := instanceidhandler.GenerateInstanceIDFromRuntimeObj(pod, h.cfg.ExcludeJsonPaths)
		if err != nil {
			return fmt.Errorf("failed to generate instance id for pod %s: %w", pod.GetName(), err)
		}
		for _, instanceId := range instanceIds {
			if templateHash := instanceId.GetTemplateHash(); templateHash != "" {
				resourceMaps.RunningTemplateHash.Add(instanceId.GetTemplateHash())
				break // templateHash is the same for every instanceId
			}
		}
		for _, instanceId := range instanceIds {
			resourceMaps.RunningInstanceIds.Add(instanceId.GetStringFormatted())
			resourceMaps.RunningTemplateHash.Add(instanceId.GetTemplateHash())
		}

		// we don't care about the cluster name, so we remove it to avoid corner cases
		wlid := wlidPkg.GetK8sWLID("", pod.Namespace, pod.Kind, pod.Name)
		wlid = wlidWithoutClusterName(wlid)

		containerNames := mapset.NewSet[string]()
		resourceMaps.RunningWlidsToContainerNames.Set(wlid, containerNames)

		podSpec, err := instanceidhandler.GetPodSpecFromRuntimeObj(pod)
		if err != nil {
			return fmt.Errorf("failed to get pod spec from workload %s: %w", wlid, err)
		}

		for _, containers := range [][]corev1.Container{podSpec.Containers, podSpec.InitContainers, convertEphemeralToContainers(podSpec.EphemeralContainers)} {
			for _, container := range containers {
				containerNames.Add(container.Name)
			}
		}

		for _, statuses := range [][]corev1.ContainerStatus{pod.Status.ContainerStatuses, pod.Status.InitContainerStatuses, pod.Status.EphemeralContainerStatuses} {
			for _, cs := range statuses {
				resourceMaps.RunningContainerImageIds.Add(cs.ImageID)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}
