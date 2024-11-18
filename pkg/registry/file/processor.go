package file

import (
	mapset "github.com/deckarep/golang-set/v2"
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

type Stringer interface {
	String() string
}

func DeflateStringer[T Stringer](in []T) []T {
	out := make([]T, 0)
	set := mapset.NewThreadUnsafeSet[string]()
	for _, item := range in {
		if set.Contains(item.String()) {
			continue
		}
		set.Add(item.String())
		out = append(out, item)
	}
	return out
}

func DeflateRulePolicies(in map[string]softwarecomposition.RulePolicy) map[string]softwarecomposition.RulePolicy {
	if in == nil {
		return nil
	}

	for key, item := range in {
		item.AllowedProcesses = mapset.Sorted(mapset.NewThreadUnsafeSet(item.AllowedProcesses...))
		in[key] = item
	}
	return in
}
