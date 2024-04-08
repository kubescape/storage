package file

import (
	sets "github.com/deckarep/golang-set/v2"
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

type Stringer interface {
	String() string
}

func deflateStringer[T Stringer](in []T) []T {
	out := make([]T, 0)
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
