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
- `pkg/metrics/metrics.go`: Phase 0's `storage_lock_wait_duration_seconds`/
  `storage_pool_wait_duration_seconds` histograms (labeled by resource kind and outcome), plus the
  single-writer-specific `storage_single_writer_queue_wait_duration_seconds`,
  `storage_single_writer_commit_total`, `storage_single_writer_conflict_retry_total`, and
  `storage_single_writer_queue_depth`, all served on the existing apiserver `/metrics` endpoint.
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
- Priority arbitration under real contention (a REST write racing a consolidation write) is now
  additionally only arbitrated within a shard; see "How it works" for why that is judged acceptable,
  but it has not been observed under real load either.
