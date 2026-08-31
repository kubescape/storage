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
