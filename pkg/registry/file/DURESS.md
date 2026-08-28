# SQLite write-path duress contract

This document is the systematic failure-mode truth table for the file/SQLite
storage backend, the architecture decision derived from it, and the map from
every cell to the test that pins it. `duress_test.go` executes the table.

## Definitions

Operations (columns):

- **C** — `Create` (API): per-key lock → PreSave → payload stage → SQL commit
  step (metadata insert + payload rename) → time-series row (container-profile
  parts only) → watch event.
- **U** — `GuaranteedUpdate` (API): per-key lock → read current → tryUpdate →
  same commit step.
- **D** — `Delete` (API): per-key lock → metadata delete + time-series delete +
  payload remove.
- **G** — `Get` (API, payload read; metadata variant reads SQLite only).
- **L** — `GetList` (API, metadata-only from SQLite).
- **K** — consolidation / maintenance (`ConsolidateTimeSeries` workers: list
  time-series → merge part payloads → replace time-series rows → save
  consolidated profile), cleanup handlers included.

Contract vocabulary (cells):

- **impossible** — the failure mode cannot occur by construction; a test proves
  the construction (not merely the absence).
- **fail-fast-clean** — the request errors within its bounded budget; the
  previous object state is fully intact (payload AND metadata agree); a retry
  of the same request can succeed.
- **retry-in-place** — absorbed inside the operation without surfacing.
- **converge** — a later pass/read repairs or completes the state; nothing is
  wedged forever.
- **never-starve** — reads keep succeeding while writers contend.

## Truth table

Failure mode × operation. Every cell names the contract and (fixed by / pinned
by). "pre-fix" describes the behavior this change removed.

| # | Failure mode | C | U | D | G | L | K |
|---|---|---|---|---|---|---|---|
| 1 | SQLITE_BUSY / SQLITE_LOCKED on a write statement | **impossible** intra-process: all SQLite write sections serialize on the in-process write gate; this process is the only writer of the DB file. Bounded busy retry retained as a backstop for extra-process writers only. Pre-fix: N pool connections raced the single SQLite write lock; the loser surfaced `database is locked` to the client (or, with the 60s busy timeout, parked its pool connection and starved reads). Pinned by `TestDuress_MixedWorkload` (zero BUSY errors across the full run) | ← same | ← same | n/a (WAL: readers never take the write lock) | n/a | ← same |
| 2 | Statement interrupted mid-write (`step: interrupted`, `clear bindings: interrupted`) by the connection's interrupt channel | **root cause removed**: `Pool.Take(ctx)` binds the connection's interrupt to the *acquisition* context, which carried a 5s timeout — every operation outliving 5s after acquisition had its own statement killed. The interrupt is now rebound to the request context after acquisition, so only a genuine client cancellation interrupts. A cancelled request rolls back via the savepoint: fail-fast-clean. Inside the gated commit section the interrupt is masked entirely: the savepoint spans an irreversible filesystem rename (or removal, for delete), and an interrupt landing on the savepoint RELEASE would roll the SQL back while the file operation persists — torn payload/metadata. The commit section is short and bounded, so cancellation waits it out; a context already dead on entry fails fast before the savepoint opens. Pinned by `TestDuress_InterruptLifetime` (op running longer than the acquisition budget completes) and `TestDuress_CancellationStorm` (client-cancelled writes leave no torn state) | ← same | ← same | interrupt on read: fail-fast-clean, nothing to roll back | ← same | consolidation takes connections with the background context; per-key transaction rolls back whole |
| 3 | Pool acquisition timeout | fail-fast-clean `ServerTimeout` + Retry-After (pre-existing behavior, kept). Writers hold the gate only for short SQL sections, so the pool is not drained by parked writers (pre-fix: 60s busy timeout parked up to N-1 writers on the SQLite lock with their connections held). Pinned by `TestDuress_MixedWorkload` (reads never starve while write storm runs) | ← | ← | ← | ← | worker skips the key this pass; next pass retries: converge |
| 4 | Per-key lock timeout | fail-fast-clean `ServerTimeout` (kept). Gate is never held while acquiring a per-key lock (lock ordering: per-key lock → gate; gate holders take no locks) — pinned by `TestDuress_ConsolidationVsAPI` completing without deadlock under adversarial interleaving | ← | ← | ← (read lock) | n/a | consolidation takes the consolidated key's write lock before the gate, same ordering |
| 5 | Payload rename fails after metadata commit | fail-fast-clean: metadata savepoint rolls back, staged file removed, previous object intact (#366 behavior, kept). Pinned by existing `TestSaveObject_RenameFailureRollsBackMetadata` | ← | n/a | n/a | n/a | ← (same saveObject) |
| 6 | Payload stage (fs write/encode) fails before commit | fail-fast-clean: nothing committed, no SQL touched. Pinned by `TestDuress_FaultInjection` | ← | n/a | n/a | n/a | ← |
| 7 | Metadata write fails mid-commit (non-BUSY: disk error, constraint) | fail-fast-clean: savepoint rollback + staged file removed (#366, kept; `TestSaveObject_MetadataFailureLeavesPayloadUntouched`) | ← | savepoint added: Delete is now atomic (see 9) | n/a | n/a | per-key transaction rollback |
| 8 | Time-series row write fails after the part's payload+metadata commit | **impossible**: the time-series row now commits inside the same savepoint as metadata+rename (pre-commit hook). Pre-fix: part committed, TS insert failed after → client got an error, retry hit `KeyExists`, and the part was permanently invisible to consolidation (no TS row) and to expiry (which scans the TS table): a silent permanent data hole. Pinned by `TestDuress_PartAtomicity` | n/a (updates don't create TS rows) | n/a | n/a | n/a | n/a |
| 9 | Torn delete (metadata deleted, payload remove fails — or the reverse) | n/a | n/a | **impossible**: delete now runs metadata delete + TS delete + payload remove inside one gated savepoint; payload-remove failure (other than not-exists) rolls the SQL back. Pre-fix: metadata-delete errors were logged and ignored while the payload was removed regardless → LIST showed ghosts / GET-vs-LIST disagreed. Pinned by `TestDuress_AtomicDelete` | GET on a half-deleted key self-heals (kept) | LIST agrees with GET after any outcome — invariant checked after every duress scenario | part deletes go through the same atomic path |
| 10 | Process crash between payload stage and commit (orphan `*.g.t`) | staged files are swept at storage construction; a crash leaves the previous object fully intact (stage is invisible until rename). Pinned by `TestDuress_OrphanStagedFiles` | ← | n/a | orphan `.t` files are never served (suffix excluded from payload reads) | ← | ← |
| 11 | Concurrent writers, same key | serialized by the per-key lock (kept); loser sees fresh state (U re-reads under lock). Pinned by `TestDuress_SameKeyHammer` (final state = exactly one of the contenders, GET/LIST agree) | ← | ← | readers see pre- or post-state, never partial | ← | consolidation serializes with API writers on the same key via the per-key lock (new: it previously raced them between its read and its transaction) |
| 12 | Concurrent writers, different keys | interleave freely; only the short gated SQL section serializes. Pinned by `TestDuress_MixedWorkload` throughput assertion (no livelock, all writes land) | ← | ← | n/a | n/a | ← |
| 13 | Reader during long writer transaction | WAL snapshot: reads proceed without blocking and see a consistent snapshot. Readers take no gate. Pinned by `TestDuress_MixedWorkload` (reads succeed and stay fast during write storm + consolidation) | — | — | never-starve | never-starve | long consolidation transaction does not block readers |
| 14 | Long consolidation transaction vs API writes | API writers wait on the gate at most their bounded budget; consolidation's gate hold is per-key (one profile's merge+write), not per-sweep. Pre-fix: consolidation's transaction held the SQLite write lock across file merges while API writers burned their busy timeout against it. Pinned by `TestDuress_ConsolidationVsAPI` | ← | ← | unaffected (13) | ← | per-key transactions; a failed key rolls back alone and converges next pass |
| 15 | Retry storm after lock release (thundering herd) | herd cannot form: waiters queue on the gate (FIFO-ish mutex) instead of colliding on the SQLite lock and re-colliding in backoff lockstep. Backstop busy retry has per-attempt gate reacquisition, no sleep while holding the gate. Pinned by `TestDuress_MixedWorkload` (no BUSY under maximum contention) | ← | ← | n/a | n/a | ← |

## Architecture decision

Candidates evaluated:

- **(a) per-connection busy handling (timeouts/retries)** — rejected as the
  primary mechanism. Empirically (component-test matrix): a 60s busy timeout
  converted write contention into pool starvation — writers parked on the
  single SQLite write lock while holding pool connections and unrelated READS
  died on pool acquisition. Short timeouts + retries merely resurface the race
  with jitter; the herd re-collides.
- **(b) in-process write gate — CHOSEN.** This process is the only writer of
  the database file. Serializing all SQLite write sections on one bounded,
  context-aware mutex makes SQLITE_BUSY structurally impossible between our own
  writers, which is all writers. SQLite serializes writers anyway — the gate
  moves the queue from the SQLite lock (where waiting costs a pool connection
  and surfaces BUSY) to a Go mutex (where waiting is cheap, bounded, and
  fail-fast). Lock ordering is fixed as per-key lock → gate; gate holders
  acquire no locks (consolidation's inner reads and profile save run in
  caller-holds-lock mode), which makes the ordering acyclic.
- **(c) splitting the consolidation transaction** — partially adopted: the
  gate hold is per consolidated key (the existing per-key transaction), so an
  API writer waits at most one profile's consolidation, not one sweep.
  Restructuring file merges out of the transaction was evaluated and deferred:
  under the gate the merge cost no longer blocks other writers' correctness,
  only their latency, and the per-key hold is bounded and measured in tests.
- **(d) retry loops** — kept only as a backstop for hypothetical extra-process
  writers, bounded, without holding the gate during backoff.

Additional root cause removed while building the table: the pool-acquisition
context (5s) doubled as the connection's interrupt lifetime, so any operation
slower than 5s end-to-end was killed by its own storage layer
(`sqlite: step: interrupted` / `clear bindings: interrupted` in production
logs). Acquisition deadline and execution lifetime are now separate; the
interrupt follows the request context.

## Invariants asserted after every duress scenario

1. **No torn state**: for every key, GET (payload) and LIST (metadata) agree on
   existence, and RV/labels match between them.
2. **No wedge**: every key is either fully present or fully absent, and a
   subsequent write to it succeeds.
3. **Contracted errors only**: clients only ever observe KeyExists, NotFound,
   contracted fail-fast timeouts, or success — never raw `database is locked`,
   `interrupted`, or torn intermediate states.
4. **Reads never starve**: read success rate and latency are asserted during
   the write storm, not after it.
5. **Convergence**: after the storm plus one consolidation pass, no staged
   files remain, no TS rows are orphaned from their parts, and no part is
   invisible to consolidation.
