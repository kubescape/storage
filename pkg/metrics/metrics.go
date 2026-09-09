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
	// CommitOutcomePanic: the commit panicked past every guard and the shard
	// goroutine's recover converted it to an error. Must stay zero.
	CommitOutcomePanic = "panic"
)

// Outcome label values for SqliteCheckpointTotal.
const (
	CheckpointOutcomeOK    = "ok"
	CheckpointOutcomeBusy  = "busy"
	CheckpointOutcomeError = "error"
	CheckpointOutcomePanic = "panic"
)

// Label values for the consolidation counters below.
const (
	// FrozenReclaimedRow / FrozenReclaimedObject: what the frozen gate reclaimed.
	FrozenReclaimedRow    = "row"
	FrozenReclaimedObject = "object"

	// DivergencePayloadAhead: the payload says Completed/Full, the metadata row
	// does not (a process crash or a failed COMMIT between the payload rename
	// and the row's commit) -- healed by re-persisting the payload as-is.
	// DivergenceMetadataAhead: the metadata row says Completed/Full, the payload
	// does not (a lost payload rename after a power loss) -- observed only.
	DivergencePayloadAhead  = "payload_ahead"
	DivergenceMetadataAhead = "metadata_ahead"

	// HealFailed* : which step of the divergence heal failed.
	HealFailedLockTimeout = "lock_timeout"
	HealFailedBegin       = "begin"
	HealFailedRead        = "read"
	HealFailedSave        = "save"
	HealFailedCommit      = "commit"
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
			Help:           "Count of single-writer commit attempts, by resource kind, priority, and outcome (committed/conflict/error/panic).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"kind", "priority", "outcome"},
	)

	// SingleWriterDirtyConnectionTotal counts pool connections a shard commit
	// was about to return with an open transaction/savepoint or a stepped,
	// unreset statement (a panic in the commit path skipped the release).
	// The connection is rolled back and reset before reuse. Must stay zero.
	SingleWriterDirtyConnectionTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "single_writer_dirty_connection_total",
			Help:           "Count of pool connections a single-writer commit found with an open transaction or unreset statement on release; rolled back before reuse.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// SingleWriterDroppedConnectionTotal counts dirty connections that could
	// not be rolled back and were dropped instead of returned, shrinking the
	// pool by one permanently. Must stay zero.
	SingleWriterDroppedConnectionTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "single_writer_dropped_connection_total",
			Help:           "Count of dirty pool connections a single-writer commit could not roll back and dropped; each one shrinks the pool permanently.",
			StabilityLevel: metrics.ALPHA,
		},
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

	// SqliteWriteHoldDuration observes how long the write gate was held for
	// one transaction (BEGIN IMMEDIATE through COMMIT/ROLLBACK), by write
	// "path" (the ObjectStore's create/update/delete/consolidate/time_series
	// and the legacy kinds' legacy_commit/legacy_delete/repair/migrate/
	// cleanup/cleanup_migrate) and "kind". The design's PM-3/PM-G1 detector:
	// with the checkpoint off the commit path this is the fsync, the page
	// writes and, for legacy_commit, one same-directory rename.
	SqliteWriteHoldDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "sqlite_write_hold_seconds",
			Help:           "Time the write gate was held for one transaction, by write path and kind.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"path", "kind"},
	)

	// SqliteWriteHoldStepDuration observes one named step inside a gated
	// hold, by "path" and "step" — today the legacy commit's payload rename,
	// so a PV whose rename is not a directory-entry update is attributable
	// without a profiler (PM-G1).
	SqliteWriteHoldStepDuration = metrics.NewHistogramVec(
		&metrics.HistogramOpts{
			Subsystem:      "storage",
			Name:           "sqlite_write_hold_step_seconds",
			Help:           "Time one named step inside a gated write hold took, by path and step.",
			Buckets:        waitBuckets,
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"path", "step"},
	)

	// WriteGateHoldAge gauges how long the current gate holder has held the
	// gate, sampled by the gate's watchdog; 0 when idle. Alert at > 5 s: a
	// leaked ticket or a wedged holder stops every writer of every kind.
	WriteGateHoldAge = metrics.NewGauge(
		&metrics.GaugeOpts{
			Subsystem:      "storage",
			Name:           "write_gate_hold_age_seconds",
			Help:           "Age of the write gate's current hold as sampled by its watchdog; 0 when idle.",
			StabilityLevel: metrics.ALPHA,
		},
	)

	// WriteGateReentrantTotal counts acquires refused because the caller was
	// already inside a gated transaction (a nested StorageImpl/ObjectStore
	// call from a gated fn), by the nested "path". Must stay zero.
	WriteGateReentrantTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "write_gate_reentrant_total",
			Help:           "Count of write gate acquires refused as re-entrant, by the nested write path. Must stay zero.",
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

	// ConsolidationFrozenReclaimedTotal counts the time_series rows and TS
	// objects consolidation's frozen gate reclaimed unmerged because the base
	// ContainerProfile was already Completed/Full when the pass read it,
	// labeled by "what" (row/object). Expected low and non-zero on
	// multi-replica workloads (each late series is reclaimed once); rising for
	// one key on many consecutive ticks while divergence heals stay at zero
	// means a writer other than consolidation keeps producing rows for a
	// completed profile.
	ConsolidationFrozenReclaimedTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "consolidation_frozen_reclaimed_total",
			Help:           "Count of time_series rows and TS objects reclaimed unmerged by consolidation because the base profile was already Completed/Full, by what (row/object).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"what"},
	)

	// ConsolidationFrozenRefusalsTotal counts consolidation saves refused
	// because the persisted base ContainerProfile was Completed/Full at write
	// time (under the per-key lock) although it was not when the pass read it.
	// Expected zero: a non-zero value means a concurrent writer completed the
	// base between the pass's read and its write.
	ConsolidationFrozenRefusalsTotal = metrics.NewCounter(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "consolidation_frozen_refusals_total",
			Help:           "Count of consolidation saves refused because the persisted base profile became Completed/Full between the pass's read and its write.",
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

	// ConsolidationDivergenceTotal counts payload/metadata divergences
	// consolidation observed on a base ContainerProfile, by "shape"
	// (payload_ahead: healed; metadata_ahead: observed only). Expected zero in
	// steady state; payload_ahead after no pod restart means a COMMIT failed.
	ConsolidationDivergenceTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "consolidation_divergence_total",
			Help:           "Count of payload/metadata divergences observed on a base profile by consolidation, by shape (payload_ahead healed, metadata_ahead observed).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"shape"},
	)

	// SqliteUngatedWriteTotal counts INSERT/UPDATE/DELETE statements prepared
	// on a pool connection the pool's write gate has never owned, by "op" and
	// "table". With the write gate on, the gate is the only writer; any other
	// writer busy-waits against it for the whole busy timeout, invisible to
	// the gate's own histograms. Must stay zero; the R2 canary.
	SqliteUngatedWriteTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "sqlite_ungated_write_total",
			Help:           "Count of write statements prepared on a pool connection the write gate does not own, by op and table. Must stay zero.",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"op", "table"},
	)

	// ConsolidationHealFailedTotal counts failed divergence heals by the step
	// that failed (lock_timeout/begin/read/save/commit). A failing heal errors the
	// tick before the frozen gate runs, so ConsolidationFrozenReclaimedTotal
	// does not move; this series is what makes a wedged heal visible.
	ConsolidationHealFailedTotal = metrics.NewCounterVec(
		&metrics.CounterOpts{
			Subsystem:      "storage",
			Name:           "consolidation_heal_failed_total",
			Help:           "Count of failed payload/metadata divergence heals, by the step that failed (lock_timeout/begin/read/save/commit).",
			StabilityLevel: metrics.ALPHA,
		},
		[]string{"reason"},
	)
)

func init() {
	legacyregistry.MustRegister(LockWaitDuration)
	legacyregistry.MustRegister(PoolWaitDuration)
	legacyregistry.MustRegister(SingleWriterQueueWaitDuration)
	legacyregistry.MustRegister(SingleWriterCommitTotal)
	legacyregistry.MustRegister(SingleWriterConflictRetryTotal)
	legacyregistry.MustRegister(SingleWriterDirtyConnectionTotal)
	legacyregistry.MustRegister(SingleWriterDroppedConnectionTotal)
	legacyregistry.MustRegister(SingleWriterQueueDepth)
	legacyregistry.MustRegister(SqliteWriteHoldDuration)
	legacyregistry.MustRegister(WriteGateWaitDuration)
	legacyregistry.MustRegister(SqliteBusyWaitDuration)
	legacyregistry.MustRegister(CPCASConflictTotal)
	legacyregistry.MustRegister(CPOwnershipRefusalTotal)
	legacyregistry.MustRegister(SqliteWalPages)
	legacyregistry.MustRegister(SqliteFreelistCount)
	legacyregistry.MustRegister(SqliteCheckpointTotal)
	legacyregistry.MustRegister(ConsolidationFrozenReclaimedTotal)
	legacyregistry.MustRegister(ConsolidationFrozenRefusalsTotal)
	legacyregistry.MustRegister(ConsolidationDivergenceTotal)
	legacyregistry.MustRegister(ConsolidationHealFailedTotal)
	legacyregistry.MustRegister(SqliteUngatedWriteTotal)
	legacyregistry.MustRegister(SqliteWriteHoldStepDuration)
	legacyregistry.MustRegister(WriteGateHoldAge)
	legacyregistry.MustRegister(WriteGateReentrantTotal)
}

// IncSqliteUngatedWrite records one write statement prepared on a pool
// connection outside the write gate.
func IncSqliteUngatedWrite(op, table string) {
	SqliteUngatedWriteTotal.WithLabelValues(op, table).Inc()
}

// ObserveSqliteWriteHoldStep records one named step inside a gated hold.
func ObserveSqliteWriteHoldStep(path, step string, d time.Duration) {
	SqliteWriteHoldStepDuration.WithLabelValues(path, step).Observe(d.Seconds())
}

// SetWriteGateHoldAge sets the current hold's age (0 when idle).
func SetWriteGateHoldAge(d time.Duration) {
	WriteGateHoldAge.Set(d.Seconds())
}

// IncWriteGateReentrant records one acquire refused as re-entrant.
func IncWriteGateReentrant(path string) {
	WriteGateReentrantTotal.WithLabelValues(path).Inc()
}

// ObserveSqliteWriteHold records one gated transaction's hold time by path
// and kind.
func ObserveSqliteWriteHold(path, kind string, d time.Duration) {
	SqliteWriteHoldDuration.WithLabelValues(path, kind).Observe(d.Seconds())
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
// (CommitOutcomeCommitted / CommitOutcomeConflict / CommitOutcomeError /
// CommitOutcomePanic).
func IncSingleWriterCommit(kind, priority, outcome string) {
	SingleWriterCommitTotal.WithLabelValues(kind, priority, outcome).Inc()
}

// IncSingleWriterDirtyConnection records one pool connection found dirty on
// release from a single-writer commit.
func IncSingleWriterDirtyConnection() {
	SingleWriterDirtyConnectionTotal.Inc()
}

// IncSingleWriterDroppedConnection records one dirty pool connection that
// could not be rolled back and was dropped instead of returned.
func IncSingleWriterDroppedConnection() {
	SingleWriterDroppedConnectionTotal.Inc()
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

// IncConsolidationFrozenReclaimed adds n to the frozen gate's reclaim count for
// what (FrozenReclaimedRow / FrozenReclaimedObject).
func IncConsolidationFrozenReclaimed(what string, n int) {
	ConsolidationFrozenReclaimedTotal.WithLabelValues(what).Add(float64(n))
}

// IncConsolidationFrozenRefusals records one consolidation save refused on a
// persisted Completed/Full base profile.
func IncConsolidationFrozenRefusals() {
	ConsolidationFrozenRefusalsTotal.Inc()
}

// IncConsolidationDivergence records one observed payload/metadata divergence
// of the given shape (DivergencePayloadAhead / DivergenceMetadataAhead).
func IncConsolidationDivergence(shape string) {
	ConsolidationDivergenceTotal.WithLabelValues(shape).Inc()
}

// IncConsolidationHealFailed records one failed divergence heal for the given
// reason (HealFailedLockTimeout / HealFailedBegin / HealFailedRead / HealFailedSave / HealFailedCommit).
func IncConsolidationHealFailed(reason string) {
	ConsolidationHealFailedTotal.WithLabelValues(reason).Inc()
}
