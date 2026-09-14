# Storage measurement harness (work budgets and paired A/B)

> **Backend A/B.** Tier B can compare the two ContainerProfile backends on the
> same commit: `PERF_AB_BACKEND=objectstore` makes a round drive the SQLite-native
> `ObjectStore` (`config.ContainerProfileSqliteBackend`, see
> `containerprofile-sqlite-backend.md`) instead of the legacy `StorageImpl`. The
> backend is provenance in the round JSON (`backend`), not part of the effective
> config, so `BASE=HEAD PERF_AB_BASE_ENV="PERF_AB_BACKEND=legacy"
> PERF_AB_HEAD_ENV="PERF_AB_BACKEND=objectstore" hack/perf-ab.sh` is a valid A/B.
> The round also has a `list` client class (`list-p99-ms`, headline; the design's
> PM-1 detector) and records `write-bytes` from `/proc/self/io` (info).

## Summary

Two instruments for the `pkg/registry/file` hot paths, so that a change to
the storage layer is reviewed with a number and not only with a correctness
test:

| Tier | Question | Mechanism | Runs |
|---|---|---|---|
| **A — work budgets** (`TestWorkBudget`) | Did this change add I/O, locks, statements or events to a hot-path call? | Count operations per scenario; compare **exactly** against a checked-in golden | Always, inside `go test ./...`, ~1 s |
| **B — paired A/B** (`make perf-ab`) | Under contention, is HEAD slower or lower-throughput than its merge-base by more than X%? | Two test binaries (merge-base, HEAD) interleaved on the same machine in one window; a relative verdict with its own noise estimate | Before SHIP on any hot-path PR; CI on demand and nightly |

Neither tier checks in an absolute latency. Tier A measures *change*, Tier B
measures *whether the change matters under contention*.

## Tier A — work budgets

`pkg/registry/file/workbudget_test.go` runs eight single-goroutine scenarios
on a fresh storage each (pool 10, one consolidation worker, in-memory payload
files, fixtures cloned from `testdata/p1.json` and stamped at run time):

| # | Scenario | Hot path |
|---|---|---|
| S1 | Learning tick: Learning base, three new reports with data | `ConsolidateTimeSeries` → `consolidateKeyTimeSeries` → merge → save → per-row delete |
| S2 | Empty tick: nothing pending | the listing only |
| S3 | Frozen tick: Completed/Full base, two late reports with data | the frozen arm |
| S4 | Divergent tick: payload Completed/Full at RV n+1, metadata row Learning at RV n, plus S3's late reports | the heal arm, then S3 |
| S5 | REST `Get` of a base key | `acquireLockedConn` read path |
| S6 | REST `Create` of a TS profile | `PreSave` + single-writer commit + `AfterCreate` |
| S7 | REST `GuaranteedUpdate` whose `tryUpdate` is a no-op | `guaranteedUpdateSingleWriter` short-circuit |
| S8 | One processed TS profile deleted as the consolidation pass deletes it | `deleteProcessedTimeSeries` |

Five counters, each a before/after delta around the scenario only:

1. **SQL statements by Go call site** — every `sqlitex.Execute` in
   `sqlite.go` (13 sites, one per function) plus the transaction openers
   (`Transaction`, `Save:saveObject`, `Save:commit`) report through
   `observeStmt(site)` in `workbudget.go`. The observer is an
   `atomic.Pointer[func(string)]`: nil in production (one load and a nil
   check, no allocation), installable without a data race while a leaked
   goroutine from another test is still executing statements.
2. **Per-key lock acquisitions by mode** — `utils.SetLockObserver` in
   `pkg/utils/mutex.go`, the same nil-in-production shape, reports every
   `Lock`/`RLock` with its outcome. The golden's `lock` is
   `{rlock, lock, timeout}`.
3. **Pool takes** — the sample-count delta of the existing
   `storage_pool_wait_duration_seconds` histogram. This PR made the two
   previously unobserved take sites report to it:
   `ContainerProfileStorageImpl.WithConnection` (the listing and every
   consolidation worker) and `Create`'s `AfterCreate` connection.
   `cleanup.go`'s `CleanupTask` take is still unobserved and outside every
   scenario.
4. **Payload-file operations** — a counting `afero.Fs` (`open`, `rename`,
   `remove`). One payload read is one open; one save is one open plus one
   rename.
5. **Watch events** — a watcher on `/`, drained with a deadline after the
   scenario.

Scenarios run under a package mutex, never in parallel, and each ends with a
**settle check**: the observers stay installed for 50 ms after the scenario
returns, then are cleared and read once more under their mutex; any late
observation (statement, lock, file op, or a moved pool histogram) fails the
test as `contaminated: <site>` instead of a flaky golden mismatch. Every
scenario pins `singleWriterEnabled == true` at its start. Hook closures never
call `testing.T`.

The golden is `pkg/registry/file/testdata/workbudget.golden.json`; comparison
is exact equality with the diff printed per field. Regenerate with

```
go test ./pkg/registry/file -run TestWorkBudget -update
```

**A golden change is a review item.** The commit that changes it states each
delta and why; the reviewer, not the author, checks the attribution. Four
invariants hold regardless of the golden:

1. a no-op writes nothing (S2, S7: no rename, no `WriteJSON`, no events);
2. a frozen tick writes nothing to the base (S3: no rename, no `WriteJSON`,
   no `Modified`) — enabled by `frozenTickInvariant` once the frozen gate
   (kubescape/storage#399) is in the tree;
3. one REST read is one read lock and one connection (S5);
4. the single-writer `ROLLBACK` recovery never fires on a passing path.

## Tier B — paired A/B

`hack/perf-ab.sh` (`make perf-ab`) compares HEAD against `BASE` (default
`git merge-base origin/main HEAD`) with `PAIRS` (default 10) interleaved
rounds. Per round, `TestPerfABRound` in `containerprofile_load_test.go`
runs a **fixed amount of work** with **closed-loop clients** on the
production shape — pool 10, 8 shards, `Workers = 2`, `GOMAXPROCS=8`, 6
writers × 2400 Creates, 25 readers × 12000 Gets, 3 updaters × 1200
GuaranteedUpdates, 12 base keys, consolidation ticking every 250 ms plus
three ticks after the clients finish — and writes one JSON: per-class
latency percentiles, wall time and throughput, the six process-registry
series (`storage_lock_wait_duration_seconds`,
`storage_pool_wait_duration_seconds`,
`storage_single_writer_queue_wait_duration_seconds`,
`storage_single_writer_commit_total`,
`storage_single_writer_conflict_retry_total`,
`storage_single_writer_queue_depth` max), and an `effective` block read back
from the constructed objects (probed pool size, `len(shards)`, `Workers`,
`GOMAXPROCS`, `singleWriterEnabled`, op counts, the pinned collapse-settings
TTL). It also prints
`BenchmarkPerfAB/<metric>` lines for `benchstat`.

The driver:

1. refuses to start if the 1-minute load average exceeds `nproc/2`
   (`PERF_AB_ALLOW_NOISY=1` overrides and stamps the verdict `(noisy)`);
2. builds `base.test` from a worktree at `BASE` **with HEAD's harness files
   overlaid** (the load test and the thresholds), so a base that predates the
   harness runs the same instrument; a base that cannot host it exits 2 with
   the build error, not a bare compile failure;
3. pins both binaries to the first `nproc/2` distinct cores with `taskset`
   and `GOMAXPROCS=8`;
4. runs three base-only probe rounds and aborts (exit 3) if any headline
   metric's CV exceeds 50 % — an early abort, not the control;
5. interleaves `PAIRS` rounds of base, head on fresh temp databases,
   sampling the load average per round into `schedule.txt`; a pair whose
   sample exceeds `nproc/2` is marked, and more than `⌈PAIRS/3⌉` marked
   pairs is INCONCLUSIVE regardless of the statistics;
6. computes the verdict with `hack/perfab` from the pre-registered thresholds
   in `pkg/registry/file/testdata/perfab.thresholds.json`.

Per headline metric (REST Get p99, Create p99, GuaranteedUpdate p95, tick
p50/p99, ops/s) the primary statistic is a **paired t-test on the per-round
log-ratios** `d_i = ln(head_i/base_i)`; Mann–Whitney U on the two samples is
the second opinion. REGRESSION needs the mean ratio past the threshold
(+20 % on latencies, +30 % on tick p99, −10 % on throughput) **and either**
test at p < 0.05; a row where the two tests disagree is flagged `SPLIT`.
Timeout counts regress when HEAD > BASE significantly; the conflict rate
when it rises by more than 5 points; `errOther`, `>5 s` and commit panics are
**hard** rows — any on HEAD when BASE has none, no statistics. GuaranteedUpdate
is gated on p95: under the pinned shape about 1 % of updates hit
`acquireLockedConn`'s 250 ms connection-attempt cliff, so its p99 straddles the
cliff and is bimodal round to round; it is reported (`info`) but never gates,
and the cliff itself is the `pool-wait-timeouts` row. `tick-total-s` (the
sum of all consolidation passes in the round, for a fixed number of rows) is
reported alongside tick p50/p99 because with fixed-interval ticking a slower
pass accumulates more rows for the next one, so per-pass percentiles partly
measure rows-per-pass; the total is the per-row cost. Noise is
measured post hoc from the pairs: the paired CV is the SD of `d_i` (over 25 %
on a headline metric is INCONCLUSIVE), and `MDE = (t_{N-1,0.975} +
t_{N-1,0.8}) · s_d / √N`; a metric whose MDE exceeds its threshold is
UNDERPOWERED with the `N'` needed printed.

Exit codes and verdict lines (quote the line verbatim in the PR):

| Exit | Line |
|---|---|
| 0 | `PASS` |
| 1 | `REGRESSION <metric> <ratio> t=<p> mw=<p>` |
| 2 | `CONFIG MISMATCH <field>` — the arms did not run the same workload (or base cannot host the harness) |
| 3 | `INCONCLUSIVE paired-CV=<x>%` / `INCONCLUSIVE load-marked=<k>/<N>` / `INCONCLUSIVE probe …` |
| 4 | `UNDERPOWERED N'=<n>` |

Precedence when several apply: config mismatch, load-marked, hard rows,
paired CV, statistical regression, underpowered, pass.

Artifacts (rounds, logs, `schedule.txt`, `verdict-table.txt`, `verdict.txt`)
land in `.omc/artifacts/perf-ab/<timestamp>/` unless `PERF_AB_OUT_DIR` is
set. `.github/workflows/perf-ab.yaml` runs the same driver on
`workflow_dispatch`, nightly against the previous day's `main`, and on PRs
labelled `perf`; runners are noisy, so INCONCLUSIVE is expected often there
and is reported in the check summary, never hidden — the local run on a quiet
machine is the authoritative one. Only a REGRESSION fails the check.

Two harness isolation choices are pinned per round and echoed in `effective`:
the SQLite busy timeout is 5 s (production: 60 s), and `collapseSettingsTTL`
is one hour (production: 10 s). The latter because a consolidation save that
has already written refreshes the CollapseConfiguration cache on a second
connection, and with no CR present `get()`'s `DeleteMetadata` on that
connection waits on the write lock the same goroutine holds — a self-deadlock
resolved only by the busy timeout, during which every shard commit waits too.
Whether a round crosses a TTL boundary is wall-clock phase, not the change
under test; the stall itself shows up in the `over-one-sec` row of an
unpinned run.

`TestContainerProfileLoad` (`LOAD_TEST=1`) remains as a time-boxed
diagnostic with env tunables; its absolute numbers are not evidence.

## The rule this adds

For any change under
`pkg/registry/file/{storage,singlewriter,containerprofile_*,sqlite}.go`:

```
go build ./...
go test ./...                                    # includes TestWorkBudget
go test -race -count=20 ./pkg/registry/file/ -run 'Consolidate|SingleWriter|Delete'
make perf-ab BASE=$(git merge-base origin/main HEAD)   # quote the verdict line
```

A golden change is stated per row in the commit message; the verdict line is
quoted in the PR description; SHIP is not written against INCONCLUSIVE or
UNDERPOWERED without a second run on a quieter machine or at `N'`.
