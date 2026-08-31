# GetList no longer pins a pool connection across internal page boundaries

## Summary

`StorageImpl.GetList` (the connection-less top-level list wrapper) previously took a
single SQLite pool connection for its entire duration and held it across every
internal SQLite-level page fetched to satisfy the request, even when that took
multiple rounds (e.g. because a label/field selector filters out enough entries that
more raw rows must be pulled from SQLite to fill the requested `Limit`). In the
`ResourceVersionFullSpec` branch, each object in each page is fetched via `s.get()`,
which itself acquires a per-key read lock; a lock stall anywhere in that multi-page
loop kept the pool connection reserved for the rest of the call too, not just for the
round that was actually stalled.

`GetList` now acquires and releases a pool connection separately for each internal
page, so a connection is only pinned for the work (and any lock stall) belonging to
the page it's currently on -- not for pages that already finished, nor implicitly
for the ones still to come.

## Why it matters

This is a restatement of the same root cause (RC1) already fixed for `Get`/`Delete`
in the storage-locking-rewrite investigation (see
`.omc/plans/storage-locking-rewrite.md`): a pool connection held idle while stalled
on an unrelated lock reduces the effective pool size for every other concurrent
caller. `GetList`'s internal pagination loop could compound this across many objects
and many rounds within a single call.

## How it works

- `StorageImpl.prepareGetList` factors out the (connection-independent) setup shared
  by `GetList` and `GetListWithConn`: predicate normalization, pulling the
  destination slice/element type out of `listObj`, the page limit, starting cursor,
  and the `ResourceVersionFullSpec` flag. It returns the normalized predicate
  explicitly, since `opts` is passed by value and mutating a local copy inside a
  helper does not propagate back to the caller.
- `StorageImpl.fetchListPage` factors out one internal page's worth of work (the
  SQLite query plus, for full-spec, the per-object `s.get()` calls and predicate
  matching) so both callers can drive the same logic against a connection they
  manage on their own terms.
- `GetList` now takes a fresh pool connection at the start of each iteration of its
  page loop and puts it back before deciding whether another page is needed --
  instead of one `Take()`/`Put()` pair wrapping the whole loop.
- `GetListWithConn` is unchanged in spirit: it still takes a single connection from
  its caller and uses it for the entire (possibly multi-page) call, since that
  connection isn't the pool's to manage -- the caller already owns it for other
  reasons. It was refactored only to share `prepareGetList`/`fetchListPage` with
  `GetList`, not to change its behavior.

## Verification

New tests in `pkg/registry/file/storage_test.go`:
- `TestStorageImpl_GetList_FullSpec_MultiPage` -- confirms the `ResourceVersionFullSpec`
  branch still returns correct results when a single call needs multiple internal
  pages (regression coverage for the `fetchListPage` extraction).
- `TestStorageImpl_GetList_ReleasesConnectionBetweenPages` -- a size-1 pool, two
  independently-controlled lock stalls (one per internal page), and a second
  goroutine blocked on `pool.Take()` throughout. Because Go hands a channel send
  directly to an already-waiting receiver, the second goroutine is guaranteed to win
  the connection the instant page 1's lock is released and `GetList` calls `Put()`
  -- strictly before `GetList`'s own goroutine can re-`Take()` it for page 2 -- if
  and only if the connection is actually released per page rather than held for the
  whole call. Confirmed to fail (times out) against the pre-fix code by temporarily
  reverting `storage.go` and rerunning, then confirmed to pass again with the fix
  restored.

Full `pkg/registry/file` suite (and its subpackages) green under `-race`
(excluding the pre-existing, unrelated `TestFileSystemStorageWatchReturnsDistinctWatchers`
flake); the two new tests re-run 5x under `-race` with zero flakiness. `go vet`
clean (aside from the known pre-existing, unrelated `callstack_test.go` warnings).
