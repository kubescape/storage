# Storage lock/pool contention metrics + configurable SQLite pool tuning

## Summary

Phase 0 of the storage-locking investigation (baseline instrumentation + a cheap,
reversible config experiment, ahead of any change to lock/connection acquisition
ordering in `pkg/registry/file/storage.go`):

1. **New Prometheus histogram metrics** for lock-hold and SQLite connection-pool-wait
   durations, labeled by resource `kind` and `outcome` (`acquired`/`timeout`). These were
   previously only visible as Debug-level `lockDuration` log lines, which are invisible at
   the production log level.
2. **Three new config knobs**, `sqlitePoolSize`, `sqliteBusyTimeout`, and `poolTimeout`,
   that make the SQLite connection pool size, per-connection busy-timeout, and the
   pool-acquisition wait deadline operator-tunable instead of hardcoded. Their defaults
   reproduce today's hardcoded values exactly (10 connections, 60s busy-timeout, 5s
   pool-acquisition timeout) — this change is a no-op until an operator sets them
   explicitly.

This does **not** change any lock/connection acquisition ordering, retry, or timeout
logic — see `docs/features/containerprofile-locking-hardening.md` for the (separate,
paused) work on that.

## Metrics

Exposed on the storage apiserver's existing `/metrics` endpoint (the generic apiserver's
built-in `EnableMetrics` route, backed by `k8s.io/component-base/metrics` /
`legacyregistry`, the same registry `k8s.io/apiserver` itself publishes to — no new HTTP
endpoint was added).

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `storage_lock_wait_duration_seconds` | Histogram | `kind`, `outcome` | Time spent waiting to acquire the per-key in-process lock (`pkg/utils.MapMutex`) before a Create/Delete/Get/GuaranteedUpdate proceeds. |
| `storage_pool_wait_duration_seconds` | Histogram | `kind`, `outcome` | Time spent waiting for a SQLite connection from the pool (`sqlitemigration.Pool.Take`, via `poolContext()`). |

- `kind` is the pluralized lowercase resource kind derived from the storage key (see
  `resourceFromKey` in `pkg/registry/file/storage.go`), e.g. `containerprofiles`,
  `sbomsyfts`.
- `outcome` is `acquired` (the wait succeeded) or `timeout` (the wait hit its
  `lockTimeout`/`poolTimeout` backstop and the caller received a `ServerTimeout`).

Implementation lives in `pkg/metrics/metrics.go` (`ObserveLockWait`, `ObservePoolWait`);
call sites are in `pkg/registry/file/storage.go` at each `s.locks.Lock`/`RLock` and
`s.pool.Take` acquisition. The existing Debug-level `lockDuration > 1s` log lines are left
in place — they remain useful for correlating a specific slow request with its key, which
the aggregate histograms can't do.

### Consolidation counters (completed-immutability and divergence)

The consolidation pass (`ContainerProfileProcessor.ConsolidateTimeSeries`) enforces
"once a profile is Completed/Full nothing updates it" on its own write path and heals the
one crash shape that would otherwise leave a completed profile unannounced. Each guard
has a counter; every one is expected to be zero or near-zero in steady state.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `storage_consolidation_frozen_reclaimed_total` | Counter | `what` (`row`/`object`) | `time_series` rows and TS objects reclaimed unmerged by the frozen gate because the base profile was already Completed/Full when the pass read it. Low and non-zero on multi-replica workloads (each late series is reclaimed once). Rising for one key on many consecutive ticks while `divergence_total{shape="payload_ahead"}` stays at zero means a writer other than consolidation keeps producing rows for a completed profile (an old node-agent ignoring `ObjectCompletedError`, for instance). |
| `storage_consolidation_frozen_refusals_total` | Counter | — | Consolidation saves refused because the persisted base was Completed/Full at write time (under the per-key lock) although it was not when the pass read it. Expected zero; non-zero names a concurrent completing writer (the Error line at `GuaranteedUpdate - tryUpdate func failed` carries the key). |
| `storage_consolidation_divergence_total` | Counter | `shape` (`payload_ahead`/`metadata_ahead`) | Payload/metadata divergences observed on a base profile. `payload_ahead` (payload Completed/Full, metadata row not — a process crash or a failed `COMMIT` between the payload rename and the row's commit) counts heals performed; non-zero after no pod restart means a `COMMIT` failed (look for `SQLITE_FULL`/`SQLITE_IOERR`). `metadata_ahead` (the inverse — a lost payload rename after a power loss) is observed and warned, not healed. |
| `storage_consolidation_heal_failed_total` | Counter | `reason` (`lock_timeout`/`begin`/`read`/`save`/`commit`) | Failed divergence heals by the step that failed. A failing heal errors the tick before the frozen gate runs, so `frozen_reclaimed_total` does not move; rising for one key on 3+ consecutive ticks is a wedged heal: `lock_timeout` means a same-key writer holds the per-key lock across ticks, `begin` means the database write lock is held past the busy timeout, `save` means the payload directory or the row cannot be written, `commit` means the heal's own `COMMIT` failed after its payload rename (I/O-class: `SQLITE_FULL`/`SQLITE_IOERR`) -- the same shape the heal repairs, one version further ahead; the next tick retries. |

### Single-writer panic containment

A panic on a shard goroutine (`pkg/registry/file/singlewriter.go`) no longer exits the process:
a panic in a `runOnShard` closure is returned to that caller as an error (`callGuarded`), the
shard's pool connection is checked for an open transaction or stepped statement before it is
returned (`putChecked`), and a panic that escapes `commit()` is recovered in `process()` so the
shard takes its next job. All three series are expected to be **zero**; any movement names a
bug to chase in the `single-writer:` Error line, which carries the key and the stack.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `storage_single_writer_commit_total{outcome="panic"}` | Counter | `kind`, `priority` | A commit panicked past every guard (for instance `sqlitex.Save`'s release panicking on a failed `ROLLBACK TO`) and the shard goroutine's recover converted it to an error for the caller. A panic inside a `runOnShard` closure is counted under `outcome="error"` instead: it is recovered at the closure boundary and fails only its own job. |
| `storage_single_writer_dirty_connection_total` | Counter | — | A shard commit was about to return a pool connection with an open transaction/savepoint or a stepped, unreset statement (a panic skipped `commit()`'s non-deferred `release`). The connection was rolled back and reset before reuse. |
| `storage_single_writer_dropped_connection_total` | Counter | — | A dirty connection could not be rolled back and was dropped rather than returned; the pool is permanently one connection smaller. Repeated drops exhaust the pool (`storage_pool_wait_duration_seconds{outcome="timeout"}` rises) and need a pod restart. |

## Config

Three new fields on `config.Config` (`pkg/config/config.go`), read the same way as every
other tunable there (via `viper`, JSON `config.json` under the configured config
directory):

| Config key | Field | Default | Description |
| --- | --- | --- | --- |
| `sqlitePoolSize` | `SqlitePoolSize` | `10` | SQLite connection pool capacity (`file.DefaultPoolSize`). |
| `sqliteBusyTimeout` | `SqliteBusyTimeout` | `60s` | Busy-timeout applied to every pooled SQLite connection (`file.DefaultBusyTimeout`). |
| `poolTimeout` | `PoolTimeout` | `5s` | How long a caller blocks in `pool.Take()` waiting for a free connection from the pool, via `poolContext()` (`file.DefaultPoolTimeout`), before failing fast with a `ServerTimeout`+Retry-After. |

`sqliteBusyTimeout` and `poolTimeout` are easy to conflate but govern different things:
`sqliteBusyTimeout` is SQLite's own internal busy-handler on a single already-acquired
connection, while `poolTimeout` bounds waiting for a connection to become available from
the pool in the first place.

All three defaults exactly match the values that were previously hardcoded in
`pkg/registry/file/sqlite.go` (`DefaultPoolSize = 10` and
`conn.SetBusyTimeout(60 * time.Second)`) and `pkg/registry/file/storage.go`
(`poolTimeout = 5 * time.Second`), so leaving them unset changes nothing.
`file.NewPool` now takes the busy-timeout as an explicit parameter
(`NewPool(path string, size int, busyTimeout time.Duration)`); a non-positive value for
either parameter falls back to its `Default*` constant, same as before for `size`.
`poolTimeout` remains a package-level var in `pkg/registry/file/storage.go` (so existing
tests can still shrink it directly), but is now set from config via the exported
`file.SetPoolTimeout(timeout time.Duration)`, which likewise falls back to
`DefaultPoolTimeout` for a non-positive value.

`main.go` wires `cfg.SqlitePoolSize` / `cfg.SqliteBusyTimeout` into the `file.NewPool` call
used to construct the production pool, and `cfg.PoolTimeout` into `file.SetPoolTimeout`
right after.

## Scope / limitations

- This is intentionally **not** a claim about which value is better — it only makes the
  values changeable without a rebuild. Trying e.g. `sqlitePoolSize: 30` or a lower
  `sqliteBusyTimeout` against the new metrics above (Phase 0c of the investigation) is a
  deployment-time config change made separately, not something defaulted here.
- No lock/connection acquisition ordering, retry, or timeout logic was touched.
- Metric cardinality is bounded by the small, fixed set of resource kinds this storage
  backend serves (see `pkg/config/config.go`'s `kindQueues` for the enumerated list) times
  two outcomes — no unbounded label values.

## Verifying

- Unit tests:
  - `pkg/metrics/metrics_test.go` (`TestObserveLockWait`, `TestObservePoolWait`) assert
    per-label-combination histogram counts/sums for both the acquired and timed-out cases.
  - `pkg/config/config_test.go` (`TestSqlitePoolConfig`) asserts the new config fields
    default to `10`/`60s` when unset and are read correctly when set.
  - `pkg/config/config_test.go` (`TestPoolTimeoutConfig`) asserts `poolTimeout` defaults
    to `5s` when unset and is read correctly when set.
  - `pkg/registry/file/storage_test.go`'s existing
    `TestStorageImpl_LockContentionReturnsServerTimeout` /
    `TestStorageImpl_PoolContentionReturnsServerTimeout` continue to pass unchanged and
    exercise the same code paths now emitting metrics.
- In production: query `storage_lock_wait_duration_seconds` /
  `storage_pool_wait_duration_seconds` on the storage apiserver's `/metrics` endpoint,
  broken down by `kind` and `outcome`.
