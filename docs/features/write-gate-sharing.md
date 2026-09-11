# Write-gate sharing: one SQLite writer for every kind

**Status:** implemented behind `containerProfileSqliteBackend` (R0 + R1 of
`.omc/plans/write-gate-sharing.md`); the flag flip (R2) and the shard removal
(R3) are separate, later steps.

## The bug class

SQLite has one write lock per database. Under the ContainerProfile SQLite
backend every ObjectStore write goes through a *write gate*: a FIFO ticket
queue in Go owning one dedicated connection, `BEGIN IMMEDIATE … COMMIT`. Every
write that does **not** go through the gate acquires the same lock through
SQLite's busy handler, which polls every 1…100 ms, is not `ctx`-bounded, and
loses systematically against a gate that commits continuously. Such a writer
stalls for the whole busy timeout (60 s in production) and the gate's own
histograms never see it — the loser is on a pool connection the gate does not
instrument.

Two instances were found and point-fixed before this change (the collapse
settings self-stall, #401; the SBOM self-repair stall, `df50b1e3`). Both were
the same `DELETE FROM metadata` reached two ways. Nine more sites remained
(`write-gate-sharing.md` §1, W1–W9b): the shard commit, `saveObject`,
`deleteLocked`, `get()`'s four self-repair deletes, the three gob-migration
rewrites and the cleanup tick's row delete and sidecar migration.

## What changed

- **One gate per process.** `main.go` builds the gate beside the pool when the
  flag is on (`file.NewWriteGate`) and hands it to the ObjectStore, the legacy
  `StorageImpl` (`SetWriteGate`) and the cleanup handler. The apiserver's
  pre-shutdown hook closes the store, then the gate, before `Pool.Close`.
  With the flag off no gate exists and every site runs today's code, byte for
  byte (Tier A goldens unchanged).
- **One helper at nine sites.** `gatedWrite` (`storage.go`) is the only place
  the flag-off/flag-on branch is chosen: bare statements or today's savepoint
  on the caller's connection without a gate; `gate.run` with one.
  - W1 `singleWriter.commitGated`: the shard holds **no pool connection**
    while queued; the CAS read joins the gated transaction.
  - W2 `saveObject(ctx, conn, …, priority, path)`: the row + rename.
  - W3 `deleteLockedGated`: `DELETE … RETURNING` captured raw, decoded after
    release; `Delete` takes `Lock(key)` only.
  - W4–W7a `repairDelete`; W6b/W7b/W8 through W2 (`migrate` label).
  - W9a/W9b `ResourcesCleanupHandler.write` on the tick's `ctx`.
- **The gate is not re-entrant, and says so.** `fn` receives a ctx marked as
  gate-held; a nested acquire through it is refused at O(1)
  (`storage_write_gate_reentrant_total`; a panic under the test binary). A
  watchdog logs a hold that outlives `gateWatchdogThreshold` with every
  goroutine's stack (`storage_write_gate_hold_age_seconds`).
- **One gate per pool, enforced.** `newWriteGate` refuses a pool that already
  has a live gate (`writegate_registry.go`).
- **Refused configuration.** `containerProfileSqliteBackend` without
  `singleWriterEnabled` is fatal at startup and `ErrGateRequiresSingleWriter`
  at runtime: with the single writer off every REST write would queue on the
  gate holding a pool connection.

### The connection rule and where it is relaxed

Hot paths hold no pool connection while queued (the shard commit, `Delete`).
Cold paths — a repair from `Get`, a migration rewrite, the cleanup walk — keep
the connection they already hold; the bound is measured, not assumed
(`TestTG5_…`, `storage_pool_wait_duration_seconds{outcome="timeout"}` = 0).

## The invariant, not the enumeration

The proof is not that W1–W9b are fixed; it is that the next site fails CI:

- **AC-G1** (`main_test.go`, `writegate_registry.go`): every pool connection
  carries an authorizer that reports each INSERT/UPDATE/DELETE prepared on it.
  In production it counts `storage_sqlite_ungated_write_total{op,table}` when
  the pool has a gate that never owned the connection — the R2 canary, alert
  at > 0. Under the test binary every such statement is recorded with its
  stack and judged at each gated environment's cleanup (and once more at
  exit): a write on a connection no gate of the pool ever owned fails the
  test naming the site. Fixtures seed state through a non-pool handle
  (`openFixtureConn`); the recorder is never windowed off (a cached statement
  re-executes without the authorizer).
- **AC-G2** (`writegate_acg2_test.go`, `writegate_acg2_on_test.go`): read
  entry point × key state, per topology. Flag-off pins the legacy residual
  (repair cells stall for the busy timeout — a golden, not a promise);
  flag-on bounds every repair cell to one gate hold plus the queue.
- **T-G1** (`writegate_tg1_test.go`): one env per site, W1–W9b.

## Metrics

| Series | Meaning |
|---|---|
| `storage_sqlite_write_hold_seconds{path,kind}` | gate hold per transaction; legacy paths `legacy_commit`, `legacy_delete`, `repair`, `migrate`, `cleanup`, `cleanup_migrate` |
| `storage_sqlite_write_hold_step_seconds{path,step}` | the legacy commit's payload rename |
| `storage_sqlite_ungated_write_total{op,table}` | must stay 0 (AC-G1) |
| `storage_sqlite_busy_wait_seconds` | must stay ~0 with the flag on (AC-G3) |
| `storage_write_gate_reentrant_total{path}` | must stay 0 |
| `storage_write_gate_hold_age_seconds` | alert at > 5 s |

## Measuring

Tier B carries legacy traffic (`containerprofile_load_test.go`, harness
version 3): two writers cycling sbomsyft objects at `LOAD_LEGACY_KB` and small
vulnerability manifests through create → update → delete, and one cleanup tick
reclaiming `LOAD_CLEANUP_ROWS` rows, concurrent with the CP scenario.

```
PERF_AB_BACKEND=objectstore PERF_AB_OUT=round.json go test -run TestPerfABRound ./pkg/registry/file/
```

reports `hold-p99-ms/<path>`, `busy-wait-max-ms`, `ungated-writes`,
`pool-wait-timeouts` and the legacy classes' latencies next to the CP ones;
`hack/perf-ab.sh` compares two arms.
