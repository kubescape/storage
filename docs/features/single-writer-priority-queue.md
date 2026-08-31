# Single dedicated writer + 2-lane priority queue (prototype, gated off)

## Summary

An alternative write path for `Create`/`GuaranteedUpdate`/`SaveContainerProfile`: instead of each
caller taking its own per-key lock and SQLite connection, all writes are funneled through one
dedicated writer goroutine per `StorageImpl`, dispatched via a 2-lane priority queue (REST writes
at `priorityHigh`, the consolidation processor's `SaveContainerProfile` at `priorityLow`, so REST
traffic always jumps ahead of background consolidation). Gated behind
`config.Config.SingleWriterEnabled` (default `false`): when unset, every write path behaves
exactly as it did before this existed.

This is Phase 0 (lock/pool-wait observability + configurable SQLite pool tuning) plus the
single-writer prototype built on top of it, from the storage-locking-rewrite investigation (see
`.omc/plans/storage-locking-rewrite.md`).

## Why it matters

The single-writer design is the answer to two of the three root causes identified in the
investigation: RC2 (payload I/O inside the per-key lock's critical section) is resolved as a side
effect — a prepare/commit split means no per-key lock is held on this path at all — and RC1
(connection acquired before the lock, letting a stalled lock wait pin an idle pool connection) is
structurally moot here, since only one goroutine ever calls `commit()`, so a lock stall can pin at
most one connection rather than one per concurrent caller.

Phase 0 (`pkg/metrics`, `SqlitePoolSize`/`SqliteBusyTimeout`/`PoolTimeout` config knobs) exists
independently as the observability and cheap-tuning-experiment groundwork the whole investigation
was built on -- it's a real, standalone lock/pool-contention diagnostic tool regardless of whether
the single-writer path is ever enabled.

## How it works

- `pkg/registry/file/singlewriter.go`: the dedicated writer goroutine, the 2-lane priority queue,
  and `createSingleWriter`/`guaranteedUpdateSingleWriter` (a prepare/commit split: the prepare
  phase runs on the caller's goroutine using its own connection, the commit phase runs serialized
  on the single writer with a resourceVersion compare-and-commit).
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
- No throughput ceiling has been measured under deliberate load.
- Still gated off by default; this PR does not change the default behavior of any resource.
