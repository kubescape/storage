# Phase 1: connection-before-lock ordering (RC1) and migration-exec-in-lock (RC3)

## Summary

Two root-cause fixes from the storage-locking investigation (see
`.omc/plans/storage-locking-rewrite.md`), reconciled to their smaller, still-relevant scope
after the single-writer prototype separately resolved these for `Create`/`GuaranteedUpdate`:

1. **RC1 — connection-before-lock ordering, for `Get` and `Delete`.** These entry points used
   to take a SQLite pool connection *before* acquiring the per-key lock, so a stalled lock wait
   held that connection idle for the whole wait — enough concurrent stalls could exhaust the
   pool and time out unrelated requests. They now acquire the lock first, then attempt a
   connection within a short, bounded window; on failure they release the lock and retry, never
   blocking indefinitely on a connection while holding the lock.
2. **RC3 — migration-tool exec no longer holds the write lock for its full (up to 30s) duration**,
   for the two lock states where it's safe to do so (`hasReadLock`/`noLock`, both reached via
   plain `Get`). The `hasWriteLock` state (reached from inside `GuaranteedUpdate`'s
   read-modify-write transaction) is unchanged and still holds the lock throughout the exec,
   since dropping a lock that call doesn't own would break that transaction's exclusivity.

A supporting fix in `pkg/utils/mutex.go`: `MapMutex.Lock`'s fast path now checks
`pendingWriters` (mirroring `RLock`'s existing check), so RC1's retry loop cannot barge ahead of
another already-queued writer indefinitely.

## Why it matters

`Get`/`Delete`'s old connection-then-lock order meant a lock stall pinned an idle connection for
its entire duration; enough concurrent stalls could exhaust the 10-connection pool and produce
`ServerTimeout`s on unrelated keys/resources. `Create`/`GuaranteedUpdate` have the same
underlying issue but are addressed separately by the (not-yet-shipped) single-writer prototype,
which structurally cannot exhaust the pool this way once enabled.

The migration tool's exec running fully inside the write lock meant any object needing
migration blocked all other access to that key for up to 30s. Unlocking around the exec is only
safe with a careful three-way re-verify on re-acquiring the lock, since the object's state can
change while unlocked:
- if the payload now decodes successfully, a concurrent writer already fixed it — use that
  object, don't overwrite it with the (now stale) migration result;
- if the payload file is now gone, a concurrent Delete won — don't resurrect it via the save
  branch;
- otherwise, the file is still genuinely undecodable — commit the migration outcome as before
  (save on success, delete on tool failure).

## How it works

- `pkg/registry/file/storage.go`'s new `acquireLockedConn` helper implements the lock-then-connection
  retry loop for `Get`/`Delete`; `GetWithConn`/`DeleteWithConn` are unchanged, still used by their
  direct callers (e.g. the consolidation path in `containerprofile_storage.go`, which already
  holds its own connection).
- A driver gotcha found while implementing this: `sqlitemigration.Pool.Take(ctx)` binds the
  returned connection's interrupt source to that same `ctx`
  (`sqlite.Conn.SetInterrupt(ctx.Done())`). `acquireLockedConn` rebinds the connection's interrupt
  to its own longer-lived context immediately after a successful `Take`, before releasing the
  short-lived per-attempt context — otherwise the connection would be marked permanently
  interrupted for its entire subsequent use.
- `migrateObjectUnlocked` (new) implements the unlock-exec-relock pattern with the three-way
  re-verify, used only by `get()`'s `hasReadLock`/`noLock` cases. `migrateObject` (the original,
  still used by the `hasWriteLock` case) is unchanged.
- `pkg/utils/mutex.go`'s `MapMutex.Lock` fast path now additionally requires
  `pendingWriters == 0`, matching `RLock`'s existing fast-path check.

## Fixes applied after review

Code review found two issues worth addressing before merge:

- **`migrateObjectUnlocked` could return without the write lock it promises to still hold.** Its
  doc comment guarantees the caller's write lock is held on every return path, but the re-`Lock`
  call after the unlocked migration exec had an error branch that returned early without it.
  Both call sites in `get()` unlock unconditionally right after this function returns, without
  checking for that error — `MapMutex.Unlock` doesn't verify ownership, so returning here without
  the lock would have the caller either nil-deref (if the key's state had since been evicted) or
  release a *different* goroutine's legitimately-held lock for the same key. In practice this
  branch is dead code today (`context.WithoutCancel(ctx)`'s `Err()` is always `nil`, and `ctx` is
  never `nil` here, so the re-`Lock` call cannot currently fail), but it's live code that would
  silently corrupt lock state the moment that assumption changes. Fixed by looping until
  reacquired instead of returning on the first failure, making the doc comment's guarantee
  airtight rather than incidentally true.
- **`Get`/`Delete`'s lock acquisition ran entirely detached from the caller's real request
  context**, only ever using a `poolContext()`-derived, `context.Background()`-rooted context.
  Beyond losing the caller's own cancellation for the lock-wait phase, this also meant the
  connection's final `conn.SetInterrupt(ctx.Done())` rebind (see "How it works" above) was bound
  to that same 5-second-bounded context — not "the caller's own longer-lived ctx" the comment
  claimed — so a `Get`/`Delete` call whose total duration (lock wait + connection take + actual
  work) exceeded `poolTimeout` would have had its connection interrupted mid-operation even while
  still legitimately in use. Fixed by threading the real `ctx` through: `acquireLockedConn` now
  derives its own internal, `poolTimeout`-bounded budget context from it (for the lock-wait
  span, per-phase timeouts, and the overall retry-loop cutoff -- `context.WithTimeout` always
  imposes its own deadline regardless of the parent's, so
  `TestStorageImpl_PoolContentionReturnsServerTimeout`'s `context.Background()` call still times
  out correctly), while using the real `ctx` directly for the trace span's parent and the
  connection's interrupt rebind. Net effect: the lock-wait span now nests under the request trace
  instead of showing up as an orphaned root, a disconnecting client's stalled lock wait can abort
  early instead of always running the full `poolTimeout`, and the connection now genuinely
  survives for as long as the caller holds it.

The second fix's specific "connection survives past `poolTimeout`" property isn't covered by a
dedicated regression test: reliably forcing "acquisition succeeds, then the stale budget expires
mid-use" requires the subsequent real work to straddle a sub-`poolTimeout` window, which isn't
achievable without either a production-code delay hook or a flaky, machine-speed-dependent sleep
-- the same class of problem as `pkg/utils/mutex.go`'s fairness fix. It rests on code inspection
instead; the existing `TestStorageImpl_PoolContentionReturnsServerTimeout`,
`TestStorageImpl_LockContentionReturnsServerTimeout`, and
`TestStorageImpl_Get_StalledLockWaitDoesNotPinPoolConnection` tests all still pass against the
restructured `acquireLockedConn`, confirming no regression to the properties they do cover.

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`); `TestStorageImpl_LockContentionReturnsServerTimeout`
and `TestStorageImpl_PoolContentionReturnsServerTimeout` pass unmodified. New dedicated tests —
`TestStorageImpl_Get_StalledLockWaitDoesNotPinPoolConnection`,
`TestStorageImpl_MigrateObjectUnlocked_ConcurrentWriteWins`, and
`TestStorageImpl_MigrateObjectUnlocked_ConcurrentDeleteNotResurrected` — were each confirmed to
fail against the pre-fix code and pass with the fix. Full package suite re-run 3x under `-race`
with zero flakiness after the review-response fixes above.

## Known residual scope, not covered by this change

`GetListWithConn`'s per-page pagination nuance (only its `ResourceVersionFullSpec` branch takes
a per-key lock per object) was the same narrower, lower-impact restatement of RC1 from the same
plan section -- since fixed separately in
[PR #380](https://github.com/kubescape/storage/pull/380).
