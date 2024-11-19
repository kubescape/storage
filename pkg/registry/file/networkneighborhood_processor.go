package file

import (
	"fmt"
	"os"
	"strconv"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/go-logger"
	loggerhelpers "github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	DefaultMaxNetworkNeighborhoodSize = 1000
)

type NetworkNeighborhoodProcessor struct {
	maxNetworkNeighborhoodSize int
}

func NewNetworkNeighborhoodProcessor() *NetworkNeighborhoodProcessor {
	maxNetworkNeighborhoodSize, err := strconv.Atoi(os.Getenv("MAX_NETWORK_NEIGHBORHOOD_SIZE"))
	if err != nil {
		maxNetworkNeighborhoodSize = DefaultMaxNetworkNeighborhoodSize
	}
	logger.L().Debug("maxApplicationProfileSize", loggerhelpers.Int("size", maxNetworkNeighborhoodSize))
	return &NetworkNeighborhoodProcessor{
		maxNetworkNeighborhoodSize: maxNetworkNeighborhoodSize,
	}
}

var _ Processor = (*NetworkNeighborhoodProcessor)(nil)

func (a NetworkNeighborhoodProcessor) PreSave(object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.NetworkNeighborhood)
	if !ok {
		return fmt.Errorf("given object is not an NetworkNeighborhood")
	}

	// size is the sum of all ingress/egress in all containers
	var size int

	// Define a function to process a slice of containers
	processContainers := func(containers []softwarecomposition.NetworkNeighborhoodContainer) []softwarecomposition.NetworkNeighborhoodContainer {
		for i, container := range containers {
			containers[i] = deflateNetworkNeighborhoodContainer(container)
			size += len(containers[i].Ingress)
			size += len(containers[i].Egress)
		}
		return containers
	}

	// Use the function for InitContainers, EphemeralContainers and Containers
	profile.Spec.EphemeralContainers = processContainers(profile.Spec.EphemeralContainers)
	profile.Spec.InitContainers = processContainers(profile.Spec.InitContainers)
	profile.Spec.Containers = processContainers(profile.Spec.Containers)

	// check the size of the profile
	if size > a.maxNetworkNeighborhoodSize {
		return fmt.Errorf("application profile size exceeds the limit of %d: %w", a.maxNetworkNeighborhoodSize, TooLargeObjectError)
	}

	// make sure annotations are initialized
	if profile.Annotations == nil {
		profile.Annotations = make(map[string]string)
	}
	profile.Annotations[helpers.ResourceSizeMetadataKey] = strconv.Itoa(size)
	return nil
}

func deflateNetworkNeighborhoodContainer(container softwarecomposition.NetworkNeighborhoodContainer) softwarecomposition.NetworkNeighborhoodContainer {
	return softwarecomposition.NetworkNeighborhoodContainer{
		Name:    container.Name,
		Ingress: deflateNetworkNeighbors(container.Ingress),
		Egress:  deflateNetworkNeighbors(container.Egress),
	}
}

// NetworkNeighbors are merged on Identifier
// DNSNames are deduplicated
// Ports are merged on Name
func deflateNetworkNeighbors(in []softwarecomposition.NetworkNeighbor) []softwarecomposition.NetworkNeighbor {
	if in == nil {
		return nil
	}
	out := make([]softwarecomposition.NetworkNeighbor, 0)
	seen := map[string]int{}
	toDeflate := mapset.NewThreadUnsafeSet[int]()
	for _, item := range in {
		if index, ok := seen[item.Identifier]; ok {
			out[index].DNSNames = append(out[index].DNSNames, item.DNSNames...)
			out[index].Ports = append(out[index].Ports, item.Ports...)
			toDeflate.Add(index)
		} else {
			out = append(out, item)
			seen[item.Identifier] = len(out) - 1 // index of the appended item
		}
	}
	for _, i := range mapset.Sorted(toDeflate) {
		out[i].DNSNames = DeflateSortString(out[i].DNSNames)
		out[i].Ports = DeflateStringer(out[i].Ports)
	}
	return out
}
