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
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	OpenDynamicThreshold             = 50
	EndpointDynamicThreshold         = 100
	DefaultMaxApplicationProfileSize = 10000
)

type ApplicationProfileProcessor struct {
	maxApplicationProfileSize int
}

func NewApplicationProfileProcessor() *ApplicationProfileProcessor {
	maxApplicationProfileSize, err := strconv.Atoi(os.Getenv("MAX_APPLICATION_PROFILE_SIZE"))
	if err != nil {
		maxApplicationProfileSize = DefaultMaxApplicationProfileSize
	}
	logger.L().Debug("maxApplicationProfileSize", loggerhelpers.Int("size", maxApplicationProfileSize))
	return &ApplicationProfileProcessor{
		maxApplicationProfileSize: maxApplicationProfileSize,
	}
}

var _ Processor = (*ApplicationProfileProcessor)(nil)

func (a ApplicationProfileProcessor) PreSave(object runtime.Object) error {
	profile, ok := object.(*softwarecomposition.ApplicationProfile)
	if !ok {
		return fmt.Errorf("given object is not an ApplicationProfile")
	}

	// size is the sum of all execs/opens in all containers
	var size int

	// Define a function to process a slice of containers
	processContainers := func(containers []softwarecomposition.ApplicationProfileContainer) []softwarecomposition.ApplicationProfileContainer {
		for i, container := range containers {
			containers[i] = deflateApplicationProfileContainer(container)
			size += len(containers[i].Execs)
			size += len(containers[i].Opens)
		}
		return containers
	}

	// Use the function for InitContainers, EphemeralContainers and Containers
	profile.Spec.EphemeralContainers = processContainers(profile.Spec.EphemeralContainers)
	profile.Spec.InitContainers = processContainers(profile.Spec.InitContainers)
	profile.Spec.Containers = processContainers(profile.Spec.Containers)

	profile.Spec.Architectures = mapset.Sorted(mapset.NewThreadUnsafeSet(profile.Spec.Architectures...))

	// check the size of the profile
	if size > a.maxApplicationProfileSize {
		return fmt.Errorf("application profile size exceeds the limit of %d: %w", a.maxApplicationProfileSize, TooLargeObjectError)
	}

	// make sure annotations are initialized
	if profile.Annotations == nil {
		profile.Annotations = make(map[string]string)
	}
	profile.Annotations[helpers.ResourceSizeMetadataKey] = strconv.Itoa(size)
	return nil
}

func deflateApplicationProfileContainer(container softwarecomposition.ApplicationProfileContainer) softwarecomposition.ApplicationProfileContainer {
	opens, err := dynamicpathdetector.AnalyzeOpens(container.Opens, dynamicpathdetector.NewPathAnalyzer(OpenDynamicThreshold))
	if err != nil {
		logger.L().Warning("failed to analyze opens", loggerhelpers.Error(err))
		opens = DeflateStringer(container.Opens)
	}

	if opens == nil {
		opens = []softwarecomposition.OpenCalls{}
	}

	endpoints, err := dynamicpathdetector.AnalyzeEndpoints(&container.Endpoints, dynamicpathdetector.NewPathAnalyzer(EndpointDynamicThreshold))
	if err != nil {
		logger.L().Warning("failed to analyze endpoints", loggerhelpers.Error(err))
		endpoints = container.Endpoints
	}
	return softwarecomposition.ApplicationProfileContainer{
		Name:           container.Name,
		Capabilities:   mapset.Sorted(mapset.NewThreadUnsafeSet(container.Capabilities...)),
		Execs:          DeflateStringer(container.Execs),
		Opens:          opens,
		Syscalls:       mapset.Sorted(mapset.NewThreadUnsafeSet(container.Syscalls...)),
		SeccompProfile: container.SeccompProfile,
		Endpoints:      endpoints,
		ImageTag:       container.ImageTag,
		ImageID:        container.ImageID,
		PolicyByRuleId: container.PolicyByRuleId,
	}
}
