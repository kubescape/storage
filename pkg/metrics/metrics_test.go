package metrics

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/component-base/metrics/testutil"
)

// TestObserveLockWait verifies lock-wait observations are recorded under the
// correct kind/outcome label combination, both for the acquired and
// timed-out cases, and that they do not leak into unrelated label pairs.
func TestObserveLockWait(t *testing.T) {
	LockWaitDuration.Reset()

	ObserveLockWait("containerprofiles", OutcomeAcquired, 10*time.Millisecond)
	ObserveLockWait("containerprofiles", OutcomeAcquired, 20*time.Millisecond)
	ObserveLockWait("containerprofiles", OutcomeTimeout, 5*time.Second)
	ObserveLockWait("sbomsyfts", OutcomeAcquired, 1*time.Millisecond)

	acquiredCount, err := testutil.GetHistogramMetricCount(LockWaitDuration.WithLabelValues("containerprofiles", OutcomeAcquired))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), acquiredCount)

	timeoutCount, err := testutil.GetHistogramMetricCount(LockWaitDuration.WithLabelValues("containerprofiles", OutcomeTimeout))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), timeoutCount)

	otherKindCount, err := testutil.GetHistogramMetricCount(LockWaitDuration.WithLabelValues("sbomsyfts", OutcomeAcquired))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), otherKindCount)

	// A label combination that was never observed reports zero, not an error.
	unseenCount, err := testutil.GetHistogramMetricCount(LockWaitDuration.WithLabelValues("sbomsyfts", OutcomeTimeout))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), unseenCount)
}

// TestObservePoolWait mirrors TestObserveLockWait for the connection-pool-wait
// histogram: acquired and timed-out observations must land in distinct,
// correctly-labeled buckets.
func TestObservePoolWait(t *testing.T) {
	PoolWaitDuration.Reset()

	ObservePoolWait("containerprofiles", OutcomeAcquired, 2*time.Millisecond)
	ObservePoolWait("containerprofiles", OutcomeTimeout, 5*time.Second)
	ObservePoolWait("containerprofiles", OutcomeTimeout, 5*time.Second)

	acquiredCount, err := testutil.GetHistogramMetricCount(PoolWaitDuration.WithLabelValues("containerprofiles", OutcomeAcquired))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), acquiredCount)

	timeoutCount, err := testutil.GetHistogramMetricCount(PoolWaitDuration.WithLabelValues("containerprofiles", OutcomeTimeout))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), timeoutCount)

	acquiredSum, err := testutil.GetHistogramMetricValue(PoolWaitDuration.WithLabelValues("containerprofiles", OutcomeAcquired))
	require.NoError(t, err)
	assert.InDelta(t, (2 * time.Millisecond).Seconds(), acquiredSum, 1e-9)
}

// TestObserveSingleWriterQueueWait verifies queue-wait observations are
// recorded under the correct kind/priority label combination, and do not leak
// into unrelated label pairs.
func TestObserveSingleWriterQueueWait(t *testing.T) {
	SingleWriterQueueWaitDuration.Reset()

	ObserveSingleWriterQueueWait("containerprofiles", PriorityHigh, 5*time.Millisecond)
	ObserveSingleWriterQueueWait("containerprofiles", PriorityHigh, 15*time.Millisecond)
	ObserveSingleWriterQueueWait("containerprofiles", PriorityLow, 1*time.Second)
	ObserveSingleWriterQueueWait("sbomsyfts", PriorityHigh, 1*time.Millisecond)

	highCount, err := testutil.GetHistogramMetricCount(SingleWriterQueueWaitDuration.WithLabelValues("containerprofiles", PriorityHigh))
	require.NoError(t, err)
	assert.Equal(t, uint64(2), highCount)

	lowCount, err := testutil.GetHistogramMetricCount(SingleWriterQueueWaitDuration.WithLabelValues("containerprofiles", PriorityLow))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), lowCount)

	otherKindCount, err := testutil.GetHistogramMetricCount(SingleWriterQueueWaitDuration.WithLabelValues("sbomsyfts", PriorityHigh))
	require.NoError(t, err)
	assert.Equal(t, uint64(1), otherKindCount)

	unseenCount, err := testutil.GetHistogramMetricCount(SingleWriterQueueWaitDuration.WithLabelValues("sbomsyfts", PriorityLow))
	require.NoError(t, err)
	assert.Equal(t, uint64(0), unseenCount)
}

// TestIncSingleWriterCommit verifies commit-outcome counts are recorded under
// the correct kind/priority/outcome label combination.
func TestIncSingleWriterCommit(t *testing.T) {
	SingleWriterCommitTotal.Reset()

	IncSingleWriterCommit("containerprofiles", PriorityHigh, CommitOutcomeCommitted)
	IncSingleWriterCommit("containerprofiles", PriorityHigh, CommitOutcomeCommitted)
	IncSingleWriterCommit("containerprofiles", PriorityHigh, CommitOutcomeConflict)
	IncSingleWriterCommit("containerprofiles", PriorityLow, CommitOutcomeError)

	committed, err := testutil.GetCounterMetricValue(SingleWriterCommitTotal.WithLabelValues("containerprofiles", PriorityHigh, CommitOutcomeCommitted))
	require.NoError(t, err)
	assert.Equal(t, float64(2), committed)

	conflict, err := testutil.GetCounterMetricValue(SingleWriterCommitTotal.WithLabelValues("containerprofiles", PriorityHigh, CommitOutcomeConflict))
	require.NoError(t, err)
	assert.Equal(t, float64(1), conflict)

	lowError, err := testutil.GetCounterMetricValue(SingleWriterCommitTotal.WithLabelValues("containerprofiles", PriorityLow, CommitOutcomeError))
	require.NoError(t, err)
	assert.Equal(t, float64(1), lowError)

	// A label combination that was never observed reports zero, not an error.
	unseen, err := testutil.GetCounterMetricValue(SingleWriterCommitTotal.WithLabelValues("containerprofiles", PriorityLow, CommitOutcomeCommitted))
	require.NoError(t, err)
	assert.Equal(t, float64(0), unseen)
}

// TestIncSingleWriterConflictRetry verifies conflict-retry counts are
// recorded per resource kind and do not leak into unrelated kinds.
func TestIncSingleWriterConflictRetry(t *testing.T) {
	SingleWriterConflictRetryTotal.Reset()

	IncSingleWriterConflictRetry("containerprofiles")
	IncSingleWriterConflictRetry("containerprofiles")
	IncSingleWriterConflictRetry("sbomsyfts")

	containerProfilesCount, err := testutil.GetCounterMetricValue(SingleWriterConflictRetryTotal.WithLabelValues("containerprofiles"))
	require.NoError(t, err)
	assert.Equal(t, float64(2), containerProfilesCount)

	sbomsyftsCount, err := testutil.GetCounterMetricValue(SingleWriterConflictRetryTotal.WithLabelValues("sbomsyfts"))
	require.NoError(t, err)
	assert.Equal(t, float64(1), sbomsyftsCount)
}

// TestSetSingleWriterQueueDepth verifies queue-depth gauges are set
// independently per priority lane, including overwriting a previous value.
func TestSetSingleWriterQueueDepth(t *testing.T) {
	SingleWriterQueueDepth.Reset()

	SetSingleWriterQueueDepth(PriorityHigh, 3)
	SetSingleWriterQueueDepth(PriorityLow, 1)

	highDepth, err := testutil.GetGaugeMetricValue(SingleWriterQueueDepth.WithLabelValues(PriorityHigh))
	require.NoError(t, err)
	assert.Equal(t, float64(3), highDepth)

	lowDepth, err := testutil.GetGaugeMetricValue(SingleWriterQueueDepth.WithLabelValues(PriorityLow))
	require.NoError(t, err)
	assert.Equal(t, float64(1), lowDepth)

	// Setting again overwrites rather than accumulates, unlike a counter.
	SetSingleWriterQueueDepth(PriorityHigh, 0)
	highDepthAfterDrain, err := testutil.GetGaugeMetricValue(SingleWriterQueueDepth.WithLabelValues(PriorityHigh))
	require.NoError(t, err)
	assert.Equal(t, float64(0), highDepthAfterDrain)
}
