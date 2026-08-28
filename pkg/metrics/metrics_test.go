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
