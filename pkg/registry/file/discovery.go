package file

import (
	"context"
	"fmt"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/config"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/pager"

	"k8s.io/client-go/discovery"

	wlidPkg "github.com/armosec/utils-k8s-go/wlid"
	mapset "github.com/deckarep/golang-set/v2"
	"github.com/goradd/maps"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1"
	"github.com/kubescape/k8s-interface/k8sinterface"
	"github.com/kubescape/k8s-interface/workloadinterface"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var (
	Workloads = mapset.NewSet[string]([]string{
		"cronjob",
		"daemonset",
		"deployment",
		"job",
		"pod",
		"replicaset",
		"service",
		"statefulset",
	}...) // FIXME put in a configmap
)

type ResourcesFetcher interface {
	FetchResources() (ResourceMaps, error)
}

type KubernetesAPI struct {
	cfg    config.Config
	client dynamic.Interface
}

func NewKubernetesAPI(cfg config.Config, client dynamic.Interface, discovery discovery.DiscoveryInterface) *KubernetesAPI {
	k8sinterface.InitializeMapResources(discovery)
	return &KubernetesAPI{
		cfg:    cfg,
		client: client,
	}
}

var _ ResourcesFetcher = (*KubernetesAPI)(nil)

// ResourceMaps is a map of running resources in the cluster, based on these maps we can decide which files to delete
type ResourceMaps struct {
	RunningWlidsToContainerNames *maps.SafeMap[string, mapset.Set[string]]
	RunningInstanceIds           mapset.Set[string]
	RunningContainerImageIds     mapset.Set[string]
	RunningTemplateHash          mapset.Set[string]
	// FIXME add nodes
	// FIXME how about hosts?
}

// FetchResources builds a map of running resources in the cluster needed for cleanup
func (h *KubernetesAPI) FetchResources() (ResourceMaps, error) {
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}

	if err := h.fetchDataFromPods(&resourceMaps); err != nil {
		return resourceMaps, fmt.Errorf("failed to fetch instance ids and image ids from running pods: %w", err)
	}

	if err := h.fetchDataFromWorkloads(&resourceMaps); err != nil {
		return resourceMaps, fmt.Errorf("failed to fetch wlids from running workloads: %w", err)
	}

	return resourceMaps, nil
}

// fetchDataFromWorkloads iterates through a predefined list of Kubernetes workload types (e.g., Deployment, StatefulSet).
// For each running workload instance, it extracts:
// 1. The Template Hash, which links a workload to its pods.
// 2. A mapping from the workload's unique ID (WLID) to the names of all containers (main, init, ephemeral) defined in its spec.
// This data is used to identify which stored profiles correspond to currently active workloads.
func (h *KubernetesAPI) fetchDataFromWorkloads(resourceMaps *ResourceMaps) error {
	for _, resource := range Workloads.ToSlice() {
		gvr, err := k8sinterface.GetGroupVersionResource(resource)
		if err != nil {
			return fmt.Errorf("failed to get group version resource for %s: %w", resource, err)
		}

		err = pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
			return h.client.Resource(gvr).List(ctx, opts)
		}).EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
			workload := obj.(*unstructured.Unstructured)
			workloadObj := workloadinterface.NewWorkloadObj(workload.Object)

			instanceIds, err := instanceidhandler.GenerateInstanceID(workloadObj, h.cfg.ExcludeJsonPaths)
			if err != nil {
				return fmt.Errorf("failed to generate instance id for workload %s: %w", workloadObj.GetName(), err)
			}
			for _, instanceId := range instanceIds {
				if templateHash := instanceId.GetTemplateHash(); templateHash != "" {
					resourceMaps.RunningTemplateHash.Add(instanceId.GetTemplateHash())
					break // templateHash is the same for every instanceId
				}
			}

			// we don't care about the cluster name, so we remove it to avoid corner cases
			wlid := wlidPkg.GetK8sWLID("", workload.GetNamespace(), workload.GetKind(), workload.GetName())
			wlid = wlidWithoutClusterName(wlid)

			containerNames := mapset.NewSet[string]()
			resourceMaps.RunningWlidsToContainerNames.Set(wlid, containerNames)

			podSpecPath := workloadinterface.PodSpec(workload.GetKind())
			containerPaths := [][]string{
				append(podSpecPath, "containers"),
				append(podSpecPath, "initContainers"),
				append(podSpecPath, "ephemeralContainers"),
			}

			for _, path := range containerPaths {
				items, ok := workloadinterface.InspectMap(workload.Object, path...)
				if !ok {
					continue // This container type doesn't exist in the spec, which is fine.
				}
				containers, ok := items.([]interface{})
				if !ok {
					continue
				}
				for _, container := range containers {
					name, ok := workloadinterface.InspectMap(container, "name")
					if !ok {
						logger.L().Debug("container has no name", helpers.String("wlid", wlid))
						continue
					}
					if nameStr, ok := name.(string); ok {
						containerNames.Add(nameStr)
					}
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

// fetchDataFromPods iterates over all running pods in the cluster.
// For each pod, it extracts:
// 1. Instance IDs, which uniquely identify a container instance.
// 2. Template Hashes, used to group pods belonging to the same controller revision.
// 3. Container Image IDs, which are the unique identifiers for the container images.
// It populates the corresponding sets in the ResourceMaps struct.
func (h *KubernetesAPI) fetchDataFromPods(resourceMaps *ResourceMaps) error {
	if err := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return h.client.Resource(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}).List(ctx, metav1.ListOptions{})
	}).EachListItem(context.Background(), metav1.ListOptions{}, func(obj runtime.Object) error {
		p := obj.(*unstructured.Unstructured)
		pod := workloadinterface.NewWorkloadObj(p.Object)

		instanceIds, err := instanceidhandler.GenerateInstanceID(pod, h.cfg.ExcludeJsonPaths)
		if err != nil {
			return fmt.Errorf("failed to generate instance id for pod %s: %w", pod.GetName(), err)
		}
		for _, instanceId := range instanceIds {
			resourceMaps.RunningInstanceIds.Add(instanceId.GetStringFormatted())
			resourceMaps.RunningTemplateHash.Add(instanceId.GetTemplateHash())
		}

		s, ok := workloadinterface.InspectMap(p.Object, "status", "containerStatuses")
		if !ok {
			return nil
		}
		containerStatuses := s.([]interface{})
		for _, cs := range containerStatuses {
			containerImageId, ok := workloadinterface.InspectMap(cs, "imageID")
			if !ok {
				continue
			}
			imageIdStr := containerImageId.(string)
			resourceMaps.RunningContainerImageIds.Add(imageIdStr)
		}

		initC, ok := workloadinterface.InspectMap(p.Object, "status", "initContainerStatuses")
		if !ok {
			return nil
		}
		initContainers := initC.([]interface{})
		for _, cs := range initContainers {
			containerImageId, ok := workloadinterface.InspectMap(cs, "imageID")
			if !ok {
				continue
			}
			imageIdStr := containerImageId.(string)
			resourceMaps.RunningContainerImageIds.Add(imageIdStr)
		}

		ephemeralC, ok := workloadinterface.InspectMap(p.Object, "status", "ephemeralContainerStatuses")
		if !ok {
			return nil
		}
		ephemeralContainers := ephemeralC.([]interface{})
		for _, cs := range ephemeralContainers {
			containerImageId, ok := workloadinterface.InspectMap(cs, "imageID")
			if !ok {
				continue
			}
			imageIdStr := containerImageId.(string)
			resourceMaps.RunningContainerImageIds.Add(imageIdStr)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}
