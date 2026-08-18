package file

import (
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestConsolidateContinuousTimeSeries_DuplicateReportRowsCollapse covers a single logical
// report that arrives as two physical rows sharing the same (previousReportTimestamp,
// reportTimestamp) pair - e.g. a chunk split client-side after a 413 from the queue. Before
// the fix these were treated as a fork, so a series that was really one completed report
// came out as two, and the caller's len(newTimeSeries) == 1 completion check never fired.
func TestConsolidateContinuousTimeSeries_DuplicateReportRowsCollapse(t *testing.T) {
	a := &ContainerProfileProcessor{}
	creation := metav1.NewTime(time.Now())

	timeSeries := []softwarecomposition.TimeSeriesContainers{
		{ReportTimestamp: "2026-01-01T00:10:00Z", PreviousReportTimestamp: "2026-01-01T00:00:00Z"},
		{ReportTimestamp: "2026-01-01T00:10:00Z", PreviousReportTimestamp: "2026-01-01T00:00:00Z"},
	}

	result := a.consolidateContinuousTimeSeries(timeSeries, &creation)

	assert.Len(t, result, 1, "two rows for the same logical report should collapse into one chain link")
}
