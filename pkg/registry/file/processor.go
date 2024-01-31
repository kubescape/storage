package file

import (
	"fmt"
	sets "github.com/deckarep/golang-set/v2"
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
	for i, container := range profile.Spec.Containers {
		profile.Spec.Containers[i] = deflate(container)
	}
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
