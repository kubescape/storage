package file

import (
	"context"

	mapset "github.com/deckarep/golang-set/v2"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

type Processor interface {
	AfterCreate(ctx context.Context, object runtime.Object) error
	PreSave(ctx context.Context, object runtime.Object) error
	SetStorage(storageImpl ContainerProfileStorage)
}

// TimeSeriesRow is one time_series table row.
type TimeSeriesRow struct {
	Kind, Namespace, Name, SeriesID, TsSuffix           string
	ReportTimestamp, Status, Completion                 string
	PreviousReportTimestamp                             string
	HasData                                             bool
}

// TimeSeriesRowProvider is implemented by a Processor whose AfterCreate side
// effect is a time_series row. A backend that can write that row inside the
// object's own transaction (ObjectStore) asks for the row here instead of
// calling AfterCreate on a second connection after the commit.
type TimeSeriesRowProvider interface {
	// TimeSeriesRowFor returns the row a Create of object must record, the
	// base (consolidated) key whose admission the write must re-check, and
	// ok=false when object is not a time-series profile.
	TimeSeriesRowFor(object runtime.Object) (row TimeSeriesRow, baseKey string, ok bool)
}

// TimeSeriesEntryWriter is the storage-side counterpart AfterCreate uses when
// the backend does not fold the row into Create's transaction.
type TimeSeriesEntryWriter interface {
	WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error
}

// ProcessedDeleteStager is implemented by a ContainerProfileStorage whose
// BeginTransaction stages writes for one atomic commit: the consolidation pass
// then hands it the processed time-series deletes BEFORE the end function
// runs, so they commit with the base write and the time_series rewrite
// (design §3.7). The legacy StorageImpl path does not implement it and keeps
// its commit-then-delete order unchanged.
type ProcessedDeleteStager interface {
	StagesProcessedDeletes() bool
}

// ConsolidationKeyReserver is implemented by a ContainerProfileStorage whose
// consolidation commit can lose a compare-and-swap to a concurrent writer of
// the same series (the ObjectStore). After a first conflict on key, the pass
// runs its one retry with the series reserved: writes to it that have not
// started yet wait for the retry, and the retry waits for those already in
// flight (both bounded), so the retry's window is free of same-series commits
// (sqliteobject_keyreserve.go). The pass MUST use the returned ctx for the
// retry and call release when it ends.
type ConsolidationKeyReserver interface {
	ReserveConsolidationKey(ctx context.Context, key string) (reservedCtx context.Context, release func())
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
