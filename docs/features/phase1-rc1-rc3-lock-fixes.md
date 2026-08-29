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

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`); `TestStorageImpl_LockContentionReturnsServerTimeout`
and `TestStorageImpl_PoolContentionReturnsServerTimeout` pass unmodified. New dedicated tests —
`TestStorageImpl_Get_StalledLockWaitDoesNotPinPoolConnection`,
`TestStorageImpl_MigrateObjectUnlocked_ConcurrentWriteWins`, and
`TestStorageImpl_MigrateObjectUnlocked_ConcurrentDeleteNotResurrected` — were each confirmed to
fail against the pre-fix code and pass with the fix.

## Known residual scope, not covered by this change

`GetListWithConn`'s per-page pagination nuance (only its `ResourceVersionFullSpec` branch takes
a per-key lock per object) is a narrower, lower-impact restatement of RC1 from the same plan
section, deliberately deferred rather than bundled into this change — tracked in the plan
document for a future pass.
