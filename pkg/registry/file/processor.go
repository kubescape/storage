package file

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"zombiezen.com/go/sqlite"
)

type Processor interface {
	AfterCreate(ctx context.Context, object runtime.Object) error
	PreSave(ctx context.Context, object runtime.Object) error
	SetStorage(storageImpl ContainerProfileStorage)
}

type DefaultProcessor struct {
}

var _ Processor = (*DefaultProcessor)(nil)

func (d DefaultProcessor) AfterCreate(_ context.Context, _ runtime.Object) error {
	return nil
}

func (d DefaultProcessor) PreSave(_ context.Context, _ runtime.Object) error {
	return nil
}

func (d DefaultProcessor) SetStorage(_ ContainerProfileStorage) {}

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

func DeflateLabelSelectorRequirement(in []metav1.LabelSelectorRequirement) []metav1.LabelSelectorRequirement {
	out := make([]metav1.LabelSelectorRequirement, 0)
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
		item.AllowedProcesses = DeflateSortString(item.AllowedProcesses)
		in[key] = item
	}
	return in
}

func DeflateSortString(in []string) []string {
	if in == nil {
		return nil
	}
	return mapset.Sorted(mapset.NewThreadUnsafeSet(in...))
}

// PreCommitProcessor is an optional Processor extension: SQL that must commit
// atomically with the object itself (inside saveObject's savepoint, before the
// payload rename). A part profile's time-series row is the canonical example —
// committing it separately allowed a part to exist without its row, invisible
// to consolidation and expiry forever (DURESS.md row 8).
type PreCommitProcessor interface {
	PreCommitSQL(ctx context.Context, conn *sqlite.Conn, object runtime.Object) error
}
