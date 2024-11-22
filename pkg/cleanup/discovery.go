package cleanup

import (
	"context"
	"fmt"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"

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
	Client dynamic.Interface
}

func NewKubernetesAPI(client dynamic.Interface, discovery discovery.DiscoveryInterface) *KubernetesAPI {
	k8sinterface.InitializeMapResources(discovery)
	return &KubernetesAPI{Client: client}
}

var _ ResourcesFetcher = (*KubernetesAPI)(nil)

// ResourceMaps is a map of running resources in the cluster, based on these maps we can decide which files to delete
type ResourceMaps struct {
	RunningWlidsToContainerNames *maps.SafeMap[string, mapset.Set[string]]
	RunningInstanceIds           mapset.Set[string]
	RunningContainerImageIds     mapset.Set[string]
	RunningTemplateHash          mapset.Set[string]
}

// FetchResources builds a map of running resources in the cluster needed for cleanup
func (h *KubernetesAPI) FetchResources() (ResourceMaps, error) {
	resourceMaps := ResourceMaps{
		RunningInstanceIds:           mapset.NewSet[string](),
		RunningContainerImageIds:     mapset.NewSet[string](),
		RunningTemplateHash:          mapset.NewSet[string](),
		RunningWlidsToContainerNames: new(maps.SafeMap[string, mapset.Set[string]]),
	}

	if err := h.fetchInstanceIdsAndImageIdsAndReplicasFromRunningPods(&resourceMaps); err != nil {
		return resourceMaps, fmt.Errorf("failed to fetch instance ids and image ids from running pods: %w", err)
	}

	if err := h.fetchWlidsFromRunningWorkloads(&resourceMaps); err != nil {
		return resourceMaps, fmt.Errorf("failed to fetch wlids from running workloads: %w", err)
	}

	return resourceMaps, nil
}

func (h *KubernetesAPI) fetchWlidsFromRunningWorkloads(resourceMaps *ResourceMaps) error {
	for _, resource := range Workloads.ToSlice() {
		gvr, err := k8sinterface.GetGroupVersionResource(resource)
		if err != nil {
			return fmt.Errorf("failed to get group version resource for %s: %w", resource, err)
		}

		workloads, err := h.Client.Resource(gvr).List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list %s: %w", gvr, err)
		}
		for _, workload := range workloads.Items {
			// we don't care about the cluster name, so we remove it to avoid corner cases
			wlid := wlidPkg.GetK8sWLID("", workload.GetNamespace(), workload.GetKind(), workload.GetName())
			wlid = wlidWithoutClusterName(wlid)

			resourceMaps.RunningWlidsToContainerNames.Set(wlid, mapset.NewSet[string]())

			c, ok := workloadinterface.InspectMap(workload.Object, append(workloadinterface.PodSpec(workload.GetKind()), "containers")...)
			if !ok {
				continue
			}
			containers := c.([]interface{})
			for _, container := range containers {
				name, ok := workloadinterface.InspectMap(container, "name")
				if !ok {
					logger.L().Debug("container has no name", helpers.String("resource", resource))
					continue
				}
				nameStr := name.(string)
				resourceMaps.RunningWlidsToContainerNames.Get(wlid).Add(nameStr)
			}

			initC, ok := workloadinterface.InspectMap(workload.Object, append(workloadinterface.PodSpec(workload.GetKind()), "initContainers")...)
			if !ok {
				continue
			}
			initContainers := initC.([]interface{})
			for _, container := range initContainers {
				name, ok := workloadinterface.InspectMap(container, "name")
				if !ok {
					logger.L().Debug("container has no name", helpers.String("resource", resource))
					continue
				}
				nameStr := name.(string)
				resourceMaps.RunningWlidsToContainerNames.Get(wlid).Add(nameStr)
			}

			ephemeralC, ok := workloadinterface.InspectMap(workload.Object, append(workloadinterface.PodSpec(workload.GetKind()), "ephemeralContainers")...)
			if !ok {
				continue
			}
			ephemralContainers := ephemeralC.([]interface{})
			for _, container := range ephemralContainers {
				name, ok := workloadinterface.InspectMap(container, "name")
				if !ok {
					logger.L().Debug("container has no name", helpers.String("resource", resource))
					continue
				}
				nameStr := name.(string)
				resourceMaps.RunningWlidsToContainerNames.Get(wlid).Add(nameStr)
			}

		}
	}
	return nil
}

func (h *KubernetesAPI) fetchInstanceIdsAndImageIdsAndReplicasFromRunningPods(resourceMaps *ResourceMaps) error {
	pods, err := h.Client.Resource(schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}).List(context.TODO(), metav1.ListOptions{
		FieldSelector: "status.phase=Running",
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	for _, p := range pods.Items {
		pod := workloadinterface.NewWorkloadObj(p.Object)

		if replicaHash, ok := pod.GetLabel("pod-template-hash"); ok {
			resourceMaps.RunningTemplateHash.Add(replicaHash)
		}

		instanceIds, err := instanceidhandler.GenerateInstanceID(pod)
		if err != nil {
			return fmt.Errorf("failed to generate instance id for pod %s: %w", pod.GetName(), err)
		}
		for _, instanceId := range instanceIds {
			resourceMaps.RunningInstanceIds.Add(instanceId.GetStringFormatted())
		}

		s, ok := workloadinterface.InspectMap(p.Object, "status", "containerStatuses")
		if !ok {
			continue
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
			continue
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
			continue
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
	}
	return nil
}
