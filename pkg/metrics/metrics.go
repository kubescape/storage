// Package metrics defines the storage server's Prometheus metrics and
// registers them with the k8s.io/component-base legacyregistry, the same
// registry the generic apiserver's built-in "/metrics" endpoint already
// serves (see k8s.io/apiserver/pkg/server, EnableMetrics).
//
// This currently covers Phase 0 of the storage-locking investigation
// (docs/features/storage-lock-pool-metrics.md): lock-hold and
// connection-pool-wait durations, so that contention which used to be
// visible only via Debug-level log lines becomes a real, always-on,
// percentile-queryable metric.
package metrics

import (
	"time"

	"k8s.io/component-base/metrics"
	"k8s.io/component-base/metrics/legacyregistry"
)

// Outcome label values for LockWaitDuration / PoolWaitDuration: whether the
// acquisition succeeded or hit its timeout.
const (
	OutcomeAcquired = "acquired"
	OutcomeTimeout  = "timeout"
)

// waitBuckets covers sub-millisecond acquisitions up through the ~5s
// lockTimeout/poolTimeout backstops (pkg/registry/file/storage.go) and a bit
// beyond, so a timed-out acquisition still lands in a meaningful bucket.
var waitBuckets = []float64{
	0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10,
}

var (
	// LockWaitDuration observes how long a caller waited to acquire the
	// per-key in-process lock (pkg/utils.MapMutex), labeled by resource
	// "kind" (see file.resourceFromKey) and "outcome" (acquired/timeout).
	LockWaitDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "lock_wait_duration_seconds",
			Help:           "Time spent waiting to acquire the per-key in-process lock, by resource kind and outcome.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"kind", "outcome"},
	)

	// PoolWaitDuration observes how long a caller waited to acquire a SQLite
	// connection from the pool (sqlitemigration.Pool.Take), labeled by
	// resource "kind" and "outcome" (acquired/timeout).
	PoolWaitDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "pool_wait_duration_seconds",
			Help:           "Time spent waiting to acquire a SQLite connection from the pool, by resource kind and outcome.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"kind", "outcome"},
	)
)

func init() {
	legacyregistry.MustRegister(LockWaitDuration)
	legacyregistry.MustRegister(PoolWaitDuration)
}

// ObserveLockWait records a lock-hold-wait observation for the given
// resource kind and outcome (OutcomeAcquired / OutcomeTimeout).
func ObserveLockWait(kind, outcome string, d time.Duration) {
	LockWaitDuration.WithLabelValues(kind, outcome).Observe(d.Seconds())
}

// ObservePoolWait records a connection-pool-wait observation for the given
// resource kind and outcome (OutcomeAcquired / OutcomeTimeout).
func ObservePoolWait(kind, outcome string, d time.Duration) {
	PoolWaitDuration.WithLabelValues(kind, outcome).Observe(d.Seconds())
}
