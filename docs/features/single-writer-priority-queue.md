# Sharded dedicated writers + 2-lane priority queue

## Summary

An alternative write path for `Create`/`GuaranteedUpdate`/`SaveContainerProfile`: instead of each
caller taking its own per-key lock and SQLite connection, writes are funneled through dedicated
writer goroutines per `StorageImpl`, each fed by its own 2-lane priority queue (REST writes at
`priorityHigh`, the consolidation processor's `SaveContainerProfile` at `priorityLow`, so REST
traffic jumps ahead of background consolidation). Gated behind
`config.Config.SingleWriterEnabled` (default `true`): when disabled, every write path behaves
exactly as it did before this existed.

Commit jobs are routed to one of `file.DefaultSingleWriterShards` (8, fixed — not independently
configurable, see `config.Config.SingleWriterEnabled`) goroutines by hashing the job's key, so
every commit for a given key runs on the same goroutine in submission order, while commits for
different keys run in parallel.

This is Phase 0 (lock/pool-wait observability + configurable SQLite pool tuning) plus the
single-writer prototype built on top of it, from the storage-locking-rewrite investigation (see
`.omc/plans/storage-locking-rewrite.md`).

## Why it matters

The single-writer design is the answer to two of the three root causes identified in the
investigation: RC2 (payload I/O inside the per-key lock's critical section) is resolved as a side
effect — a prepare/commit split means no per-key lock is held on this path at all — and RC1
(connection acquired before the lock, letting a stalled lock wait pin an idle pool connection) is
structurally bounded here, since only the shard goroutines call `commit()`, so a lock stall can pin
at most `DefaultSingleWriterShards` connections rather than one per concurrent caller. (This is why
the shard count is fixed at or below `SqlitePoolSize`.)

Phase 0 (`pkg/metrics`, `SqlitePoolSize`/`SqliteBusyTimeout`/`PoolTimeout` config knobs) exists
independently as the observability and cheap-tuning-experiment groundwork the whole investigation
was built on -- it's a real, standalone lock/pool-contention diagnostic tool regardless of whether
the single-writer path is ever enabled.

## How it works

- `pkg/registry/file/singlewriter.go`: the writer shards (`shardIndexFor` routes a key to one, via
  FNV-1a), their 2-lane priority queues, and
  `createSingleWriter`/`guaranteedUpdateSingleWriter` (a prepare/commit split: the prepare phase
  runs on the caller's goroutine using its own connection, the commit phase runs serialized per key
  on that key's shard with a resourceVersion compare-and-commit).
- Sharding preserves every correctness property the original one-goroutine-for-all-keys design
  relied on, because all of them (no-lost-updates, torn-read prevention, delete-race conflict
  detection) only ever required *same key → same committer, in order*, which key-hash routing gives
  exactly. Writes to different keys touch disjoint metadata rows and disjoint payload files.
  What it does weaken, deliberately: `highBurstLimit`'s high-vs-low arbitration is now per-shard,
  so a high-priority write only overtakes low-priority writes on its own shard. Jobs on different
  shards never competed for a goroutine's time in the first place, so there is nothing there to
  arbitrate; only same-shard cross-priority contention is affected.
- `StorageImpl.Create`/`GuaranteedUpdate` check `singleWriterEnabled` and route to the
  single-writer path at `priorityHigh`; `ContainerProfileStorageImpl.SaveContainerProfile` does
  the same at `priorityLow`.
- `singleWriter.runOnShard(ctx, key, priority, fn)`: a general escape hatch for background work
  that mutates a key's row but doesn't fit the Create/GuaranteedUpdate compare-and-commit shape.
  `fn` runs from inside that key's own shard goroutine, holding the same pool connection and
  per-key lock a commit would, instead of the caller taking its own raw pool connection (which
  has no way to yield to, or be yielded by, a live shard commit — see the gap this closes below).
  `fn` must be a leaf **and** non-blocking. Leaf: it must not itself submit another job to the same
  shard (directly, or via Create/GuaranteedUpdate/SaveContainerProfile for a same-shard key) — that
  would deadlock, since the shard's one goroutine is `fn`'s caller. Non-blocking: it must not block
  on anything outside `conn` — in particular **no `watchDispatcher.*` call inside `fn`**: a watch
  client that has stopped reading blocks `send` until its own ctx ends, which inside `fn` would
  freeze the shard (and its pool connection and per-key lock) for as long as that remote client
  chooses. Dispatch watch events on the caller after `runOnShard` returns, the way
  `createSingleWriter`/`guaranteedUpdateSingleWriter` already do. Used by
  `ContainerProfileProcessor.deleteContainerProfileArbitrated`, which routes
  `StorageImpl.deleteLocked` (the SQLite+filesystem half of `delete`, without the dispatch) and
  emits the `Deleted` event itself afterwards. This is also why consolidation's per-key unit is
  *not* wrapped in one `runOnShard` call end-to-end: it calls `SaveContainerProfile` for the same
  key partway through, which would deadlock exactly as described.
- Panic containment, three layers (Lane 0 / L0-B). A shard goroutine is the only writer for
  every key hashed to it, so a panic that killed it would wedge those keys forever — and with no
  `recover()` anywhere in the package it actually exited the process. (1) `callGuarded` wraps the
  `runOnShard` closure: its panic becomes that job's error. (2) `putChecked` replaces `commit()`'s
  `defer s.pool.Put(conn)`: `release(&err)` is deliberately called, not deferred, so a panic in
  `writeMetadata`/`renamePayload` skipped it and returned a connection with an open `SAVEPOINT`
  (which `Pool.Put` does not detect — it checks stepped statements only); `putChecked` tests
  `AutocommitEnabled` and `CheckReset`, rolls back / resets a dirty connection before reuse, drops
  one it cannot clean, and removes the temp payload on every non-committed exit. It runs inside
  `commit()`'s own frame because Go unwinds `commit()`'s defers before `process()` sees the panic.
  (3) `process()` recovers whatever escapes `commit()` (e.g. `sqlitex.Save`'s release panicking on
  a failed `ROLLBACK TO`), returns `errCommitPanic` to the caller, counts `outcome="panic"`, logs
  the stack, and takes the next job. Counters in `docs/features/storage-lock-pool-metrics.md`;
  tests in `singlewriter_lane0_test.go` (each crashes the test binary on pre-L0-B code, so
  fail-on-today evidence is recorded one test per process).
- `pkg/metrics/metrics.go`: Phase 0's `storage_lock_wait_duration_seconds`/
  `storage_pool_wait_duration_seconds` histograms (labeled by resource kind and outcome), plus the
  single-writer-specific `storage_single_writer_queue_wait_duration_seconds`,
  `storage_single_writer_commit_total` (outcomes `committed`/`conflict`/`error`/`panic`),
  `storage_single_writer_conflict_retry_total`, `storage_single_writer_queue_depth`,
  `storage_single_writer_dirty_connection_total`, and
  `storage_single_writer_dropped_connection_total`, all served on the existing apiserver
  `/metrics` endpoint.
- `pkg/registry/file/sqlite.go`'s `NewPool` gained `size`/`busyTimeout` parameters (both fall back
  to the previously-hardcoded defaults when non-positive), wired to the new
  `SqlitePoolSize`/`SqliteBusyTimeout` config knobs; `PoolTimeout` similarly became
  operator-tunable via `SetPoolTimeout` instead of a hardcoded constant.

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`); `pkg/registry/file` (including
`singlewriter_test.go`) and `pkg/metrics` re-run 5x under `-race` with zero flakiness.

**Live-validated on `armo-dev-stage`** (`kubescape` namespace): deployed as
`quay.io/matthiasb_1/storage:spike-singlewriter` with `singleWriterEnabled: true`. A direct manual
test (`kubectl apply`/`patch`/`delete` on a throwaway `ContainerProfile`) proved the path works
end-to-end: Create → committed, high priority, ~20µs queue wait; Update → committed, zero
conflicts; Delete → clean. Real (non-synthetic) production traffic was separately confirmed
exercising this path over an extended monitoring window for several resource kinds
(`sbomsyftfiltereds`, `sbomsyfts`, `vulnerabilitymanifests`, `vulnerabilitymanifestsummaries`) via
`storage_single_writer_commit_total`, climbing steadily with zero conflicts or errors observed.

## Known gaps / not yet done

- Not yet validated under real concurrent same-key contention or real priority arbitration
  (REST write racing a consolidation write for the same key) -- only observed passively under
  whatever contention pattern naturally occurred on `armo-dev-stage`.
- The original one-goroutine-for-all-keys design *did* have a throughput ceiling, and it was hit in
  practice: with the path enabled by default, kubescape/node-agent's CI (many `ContainerProfile`
  writes for many pods concurrently) went from 0-1 flaky test failures to 18-19 of ~30 jobs failing,
  every one on `context deadline exceeded` against this apiserver. Sharding commits by key hash is
  the fix, and it was validated against that real workload, not just this repo's own unit tests:
  with the default 8 shards, node-agent's CI went from 18-19/30 failing (every one a sustained
  20-minute hang) to 15/30 (fast, self-healing failures on unrelated assertions; a representative
  test run went from 323 failed create attempts to 6 out of 61). The 1-way ceiling is confirmed
  gone. What is *not* confirmed: raising shards further does not help — a 16-shard/pool-size-24
  variant of the same validation scored 21/32, no better than 8 shards and arguably worse — so the
  residual gap between 15/30 and node-agent's historical 0-1/30 baseline is not simply "not enough
  shards" and needs its own investigation (node-agent-side CI resource limits and pod churn were
  observed as candidates, independent of this fix).
  **Update:** that follow-up investigation ruled out both node-agent-side candidates (storage pod
  CPU limit bumped 4x: no change; live CI pod restart counts: zero across every failing job
  checked) and instead found a gap in *this* fix's own coverage — see `runOnShard` below.
- Priority arbitration under real contention (a REST write racing a consolidation write) is now
  additionally only arbitrated within a shard; see "How it works" for why that is judged acceptable,
  but it has not been observed under real load either.
- **Found and fixed:** `ConsolidateTimeSeries`'s `deleteProcessedTimeSeries` step deleted each
  processed TS profile via `DeleteContainerProfile`, which — unlike `SaveContainerProfile` — took a
  *raw* pool connection outside this whole shard system. That connection could collide directly
  with a shard's SQLite write lock, and a genuine collision blocks the loser for up to
  `DefaultBusyTimeout` (60s) rather than the microsecond in-process channel wait every shard-routed
  write gets. Reproduced locally (`containerprofile_load_test.go`, `LOAD_CONSOLIDATORS=1`): a pure
  20-way concurrent write burst alone was flawless (9543/9543 ops, p99=115ms), but adding one
  concurrent consolidation pass collapsed throughput by >250x (36 ops/8s, 14 failed, multi-second
  `database is locked` stalls) — the same fast, heterogeneous failure shape as the residual
  node-agent CI flakiness, not the original catastrophic hang. Routing that one delete call through
  the new `runOnShard` (below) restored throughput to ~5000+ ops/8s with near-zero failures in the
  same repro, p50 in microseconds and p99 ~5ms (down from 9.9s). Not yet validated against real
  node-agent CI (only this repo's local load test) as of this writing.
