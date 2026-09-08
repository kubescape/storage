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

// Priority label values for the single-writer priority queue metrics:
// which lane a commitJob traveled through.
const (
	PriorityHigh = "high"
	PriorityLow  = "low"
)

// Outcome label values for SingleWriterCommitTotal: how a commit attempt
// concluded.
const (
	CommitOutcomeCommitted = "committed"
	CommitOutcomeConflict  = "conflict"
	CommitOutcomeError     = "error"
	CommitOutcomePanic     = "panic"
)

// Outcome label values for SqliteCheckpointTotal.
const (
	CheckpointOutcomeOK    = "ok"
	CheckpointOutcomeBusy  = "busy"
	CheckpointOutcomeError = "error"
	CheckpointOutcomePanic = "panic"
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

	// SingleWriterQueueWaitDuration observes how long a commitJob sat in the
	// single writer's high/low priority channel before the writer goroutine
	// (singleWriter.run) picked it up and started processing it. This is the
	// new contention point the single-writer design introduces by
	// centralizing every write through one goroutine, labeled by resource
	// "kind" and queue "priority" (high/low).
	SingleWriterQueueWaitDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "single_writer_queue_wait_duration_seconds",
			Help:           "Time a commit job spent waiting in the single writer's priority queue before being picked up, by resource kind and priority.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"kind", "priority"},
	)

	// SingleWriterCommitTotal counts single-writer commit attempts, labeled by
	// resource "kind", queue "priority" (high/low), and "outcome"
	// (committed/conflict/error). This shows how often optimistic commits
	// succeed vs. hit a resourceVersion conflict vs. fail for another reason,
	// and whether high- vs low-priority jobs are serviced as expected.
	SingleWriterCommitTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "single_writer_commit_total",
			Help:           "Count of single-writer commit attempts, by resource kind, priority, and outcome (committed/conflict/error).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"kind", "priority", "outcome"},
	)

	// SingleWriterConflictRetryTotal counts how many times
	// guaranteedUpdateSingleWriter's retry loop redoes the prepare phase
	// because a commit was rejected with errWriteConflict, labeled by
	// resource "kind". This directly measures retry-storm risk under real
	// contention.
	SingleWriterConflictRetryTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "single_writer_conflict_retry_total",
			Help:           "Count of GuaranteedUpdate prepare-phase retries caused by a resourceVersion conflict at commit time, by resource kind.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"kind"},
	)

	// SingleWriterQueueDepth gauges the current number of commitJobs waiting
	// in each single-writer priority lane, updated on enqueue and dequeue.
	// This shows whether a backlog is building up under real load, labeled
	// by queue "priority" (high/low).
	SingleWriterQueueDepth = metrics.NewGaugeVec(
		&metrics.GaugeOpts{
			Subsystem:      "storage",
			Name:           "single_writer_queue_depth",
			Help:           "Current number of commit jobs waiting in the single writer's priority queue, by priority.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"priority"},
	)

	// SqliteWriteHoldDuration observes how long the ObjectStore's write gate
	// was held for one transaction (BEGIN IMMEDIATE through COMMIT/ROLLBACK),
	// by write "path" (create/update/delete/consolidate). The design's PM-3
	// detector: with the checkpoint off the commit path this is the fsync and
	// the page writes, nothing else.
	SqliteWriteHoldDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "sqlite_write_hold_seconds",
			Help:           "Time the ContainerProfile write gate was held for one transaction, by write path.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"path"},
	)

	// WriteGateWaitDuration observes how long a caller queued for the
	// ObjectStore's write gate ticket, by "priority" (high/low).
	WriteGateWaitDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "write_gate_wait_seconds",
			Help:           "Time a writer queued for the ContainerProfile write gate before its ticket was granted, by priority.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"priority"},
	)

	// SqliteBusyWaitDuration observes how long BEGIN IMMEDIATE on the gate's
	// dedicated connection spent in SQLite's busy handler because an ungated
	// writer (a legacy kind's commit, cleanup.go) held the database lock.
	SqliteBusyWaitDuration = metrics.NewHistogram(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "sqlite_busy_wait_seconds",
			Help:           "Time BEGIN IMMEDIATE on the ContainerProfile write gate's connection waited for SQLite's database lock.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
	)

	// CPCASConflictTotal counts compare-and-swap conflicts on the ObjectStore
	// (an UPDATE/DELETE whose rv/uid predicate matched no row), by "op".
	CPCASConflictTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "cp_cas_conflict_total",
			Help:           "Count of ContainerProfile compare-and-swap conflicts, by operation.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"op"},
	)

	// CPOwnershipRefusalTotal counts operations on a ContainerProfile key the
	// legacy StorageImpl refused because the kind is owned by the ObjectStore
	// (the kind-ownership guard). Any non-zero value is a mis-wiring.
	CPOwnershipRefusalTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "cp_ownership_refusal_total",
			Help:           "Count of legacy StorageImpl operations refused on a kind owned by the ContainerProfile SQLite backend, by operation.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"op"},
	)

	// SqliteWalPages gauges the WAL size in pages as last observed by the
	// background checkpointer.
	SqliteWalPages = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "storage",
			Name:           "sqlite_wal_pages",
			Help:           "WAL size in pages as last observed by the background PASSIVE checkpointer.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// SqliteFreelistCount gauges PRAGMA freelist_count as last observed by the
	// background checkpointer (TS profiles are create-then-delete objects; their
	// pages cycle through the freelist).
	SqliteFreelistCount = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "storage",
			Name:           "sqlite_freelist_count",
			Help:           "PRAGMA freelist_count as last observed by the background checkpointer.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// SqliteCheckpointTotal counts background checkpoint runs by "outcome"
	// (ok/busy/error/panic).
	SqliteCheckpointTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "sqlite_checkpoint_total",
			Help:           "Count of background PASSIVE checkpoint runs, by outcome.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"outcome"},
	)
)

func init() {
	legacyregistry.MustRegister(LockWaitDuration)
	legacyregistry.MustRegister(PoolWaitDuration)
	legacyregistry.MustRegister(SingleWriterQueueWaitDuration)
	legacyregistry.MustRegister(SingleWriterCommitTotal)
	legacyregistry.MustRegister(SingleWriterConflictRetryTotal)
	legacyregistry.MustRegister(SingleWriterQueueDepth)
	legacyregistry.MustRegister(SqliteWriteHoldDuration)
	legacyregistry.MustRegister(WriteGateWaitDuration)
	legacyregistry.MustRegister(SqliteBusyWaitDuration)
	legacyregistry.MustRegister(CPCASConflictTotal)
	legacyregistry.MustRegister(CPOwnershipRefusalTotal)
	legacyregistry.MustRegister(SqliteWalPages)
	legacyregistry.MustRegister(SqliteFreelistCount)
	legacyregistry.MustRegister(SqliteCheckpointTotal)
}

// ObserveSqliteWriteHold records one gated transaction's hold time by path.
func ObserveSqliteWriteHold(path string, d time.Duration) {
	SqliteWriteHoldDuration.WithLabelValues(path).Observe(d.Seconds())
}

// ObserveWriteGateWait records one caller's queue time for a gate ticket.
func ObserveWriteGateWait(priority string, d time.Duration) {
	WriteGateWaitDuration.WithLabelValues(priority).Observe(d.Seconds())
}

// ObserveSqliteBusyWait records how long BEGIN IMMEDIATE waited for the lock.
func ObserveSqliteBusyWait(d time.Duration) {
	SqliteBusyWaitDuration.Observe(d.Seconds())
}

// IncCPCASConflict records one compare-and-swap conflict for op.
func IncCPCASConflict(op string) {
	CPCASConflictTotal.WithLabelValues(op).Inc()
}

// IncCPOwnershipRefusal records one refused legacy operation for op.
func IncCPOwnershipRefusal(op string) {
	CPOwnershipRefusalTotal.WithLabelValues(op).Inc()
}

// SetSqliteWalPages sets the last observed WAL size in pages.
func SetSqliteWalPages(pages int64) {
	SqliteWalPages.Set(float64(pages))
}

// SetSqliteFreelistCount sets the last observed freelist_count.
func SetSqliteFreelistCount(pages int64) {
	SqliteFreelistCount.Set(float64(pages))
}

// IncSqliteCheckpoint records one checkpointer run with the given outcome.
func IncSqliteCheckpoint(outcome string) {
	SqliteCheckpointTotal.WithLabelValues(outcome).Inc()
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

// ObserveSingleWriterQueueWait records how long a commitJob waited in the
// single writer's priority queue for the given resource kind and priority
// (PriorityHigh / PriorityLow) before being picked up by the writer goroutine.
func ObserveSingleWriterQueueWait(kind, priority string, d time.Duration) {
	SingleWriterQueueWaitDuration.WithLabelValues(kind, priority).Observe(d.Seconds())
}

// IncSingleWriterCommit records one single-writer commit attempt for the
// given resource kind, priority (PriorityHigh / PriorityLow), and outcome
// (CommitOutcomeCommitted / CommitOutcomeConflict / CommitOutcomeError).
func IncSingleWriterCommit(kind, priority, outcome string) {
	SingleWriterCommitTotal.WithLabelValues(kind, priority, outcome).Inc()
}

// IncSingleWriterConflictRetry records one GuaranteedUpdate prepare-phase
// retry caused by a resourceVersion conflict at commit time, for the given
// resource kind.
func IncSingleWriterConflictRetry(kind string) {
	SingleWriterConflictRetryTotal.WithLabelValues(kind).Inc()
}

// SetSingleWriterQueueDepth sets the current number of commit jobs waiting in
// the given priority lane (PriorityHigh / PriorityLow).
func SetSingleWriterQueueDepth(priority string, depth int) {
	SingleWriterQueueDepth.WithLabelValues(priority).Set(float64(depth))
}
