package file

import (
	"fmt"
	"strconv"

	sets "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"k8s.io/apimachinery/pkg/runtime"
)

type Processor interface {
	PreSave(object runtime.Object) error
}

type DefaultProcessor struct {
}

var _ Processor = (*DefaultProcessor)(nil)

func (d DefaultProcessor) PreSave(_ runtime.Object) error {
	return nil
}

type ApplicationProfileProcessor struct {
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
			containers[i] = deflate(container)
			size += len(containers[i].Execs)
			size += len(containers[i].Opens)
		}
		return containers
	}

	// Use the function for InitContainers, EphemeralContainers and Containers
	profile.Spec.EphemeralContainers = processContainers(profile.Spec.EphemeralContainers)
	profile.Spec.InitContainers = processContainers(profile.Spec.InitContainers)
	profile.Spec.Containers = processContainers(profile.Spec.Containers)

	profile.Annotations[helpers.ResourceSizeMetadataKey] = strconv.Itoa(size)
	return nil
}

func deflate(container softwarecomposition.ApplicationProfileContainer) softwarecomposition.ApplicationProfileContainer {
	return softwarecomposition.ApplicationProfileContainer{
		Name:         container.Name,
		Capabilities: sets.NewThreadUnsafeSet(container.Capabilities...).ToSlice(),
		Execs:        deflateStringer(container.Execs),
		Opens:        deflateStringer(container.Opens),
		Syscalls:     sets.NewThreadUnsafeSet(container.Syscalls...).ToSlice(),
	}
}

type Stringer interface {
	String() string
}

func deflateStringer[T Stringer](in []T) []T {
	var out []T
	set := sets.NewThreadUnsafeSet[string]()
	for _, item := range in {
		if set.Contains(item.String()) {
			continue
		}
		set.Add(item.String())
		out = append(out, item)
	}
	return out
}
