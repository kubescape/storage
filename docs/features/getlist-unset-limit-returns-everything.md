# GetList no longer truncates an unpaginated List to 500 items

## Summary

`StorageImpl.prepareGetList` silently substituted a default of 500 for
`Predicate.Limit` whenever a caller left it unset (`Limit == 0`), then had
`GetList`/`GetListWithConn` cap the returned list at that substituted value and
attach a `continue` token once more than 500 matching items existed.

Per Kubernetes List API conventions, `Limit == 0` means the caller did not
request pagination at all, and the server must return the complete result in
one response. This code instead treated an unpaginated request exactly like a
paginated one with `Limit=500`, without the caller ever asking for that.

## Why it matters

Confirmed against a real deployment (armo-dev): a plain `kubectl get --raw`
List call for `containerprofiles`, with no `limit` query parameter, returned
exactly 500 items and a `continue` token, out of 1120 objects that actually
existed. Any client that issues a single, unpaginated List call and doesn't
separately check the response for a `continue` token -- which is a reasonable
thing for a client to skip, since it never asked to paginate in the first
place -- silently renders an incomplete result as if it were the whole thing.

This reproduced with k9s's generic/dynamic resource lister
(`internal/dao/generic.go`'s `dial.List(ctx, opts)`), which sets no `Limit` and
never inspects `metadata.continue`/`metadata.remainingItemCount` on the
response. `kubectl get` did not show the same truncation only because it
explicitly wraps its list calls in `client-go`'s `tools/pager.ListPager`,
which sets its own `Limit` and follows `continue` tokens across multiple
round-trips regardless of what the original caller asked for -- opt-in
behavior specific to that client, not something every List caller gets.

## How it works

- `prepareGetList` now treats `Predicate.Limit == 0` (exactly zero) as "no
  limit requested" and leaves it that way, instead of substituting
  `defaultListBatchSize` (500). A separate `batchSize` return value captures
  the internal per-round-trip SQLite fetch size only -- it defaults to
  `defaultListBatchSize`, or to the caller's own `Limit` when that's smaller,
  but it never caps the total result on its own.
- `GetList`/`GetListWithConn`'s pagination loop condition changed from
  `for int64(v.Len()) < limit` to `for limit == 0 || int64(v.Len()) < limit`,
  so an unlimited request keeps fetching successive internal batches (each
  still acquiring/releasing its own pool connection in `GetList`, per
  `getlist-page-connection-hold.md`) until the underlying data is exhausted,
  rather than stopping once the old substituted limit was reached.
- The new `nextPageSize(limit, batchSize, fetched)` helper computes each
  internal page's size: always `batchSize` when unlimited, otherwise however
  much is still needed to reach `limit`, clamped to `batchSize` so that even a
  caller-specified large `Limit` (e.g. 5000) still fetches -- and releases its
  pool connection -- in bounded chunks rather than one giant round trip.
- A genuinely unlimited List still ends with no `continue` token (there is
  nothing left to continue), so well-behaved paginating clients are
  unaffected and see the same termination signal as before.
- A **negative** `Limit` is deliberately *not* folded into "unlimited" --
  it's invalid/degenerate input, and the loop guard (`limit == 0`, not
  `limit <= 0`) preserves the pre-fix behavior of returning an empty list for
  it, rather than reinterpreting a negative value as a request for
  everything. This distinction was caught in code review of the initial
  version of this fix, which had used `limit <= 0` throughout.

## Cross-file regression this fix required: `DeleteCollection`'s cancellation checkpoint

Code review of this fix (before merge) caught a real, separate regression it
would otherwise have introduced: `genericrest.Store.DeleteCollection` (the
Phase 4 bulk-delete implementation) was silently relying on `GetList`'s old
500-item default as its own cancellation checkpoint. Its only `ctx.Done()`
check runs once per outer loop iteration -- i.e. once per internal List page,
not once per deleted item. Before this fix, an unpaginated "delete all" call
(`DeleteCollection` with no explicit `Limit`) would always page through the
collection roughly 500 items at a time as a side effect of `GetList`'s old
default, giving that check a real chance to fire on a large collection. Once
`GetList` was fixed to return everything in one call when no `Limit` is
requested, that implicit paging -- and with it, the only opportunity for
`DeleteCollection`'s cancellation check to run more than once -- disappeared:
a "delete all" call on a large collection would list and delete the entire
thing in one uninterruptible pass.

Fixed in the same change: `DeleteCollection` now sets its own
`DefaultDeleteCollectionPageSize` (500) on `listOptions.Limit` whenever the
caller didn't specify one, independent of whatever `GetList`'s own default
happens to be. This restores the original pagination cadence without
`DeleteCollection` depending on an implementation detail of a function it
doesn't own.

## Verification

New tests in `pkg/registry/file/storage_test.go`:
- `TestNextPageSize` -- table-driven coverage of the page-size helper's edge
  cases (unlimited, limit smaller/larger than the batch size, limit already
  satisfied).
- `TestStorageImpl_GetList_UnsetLimitReturnsAllItems` -- creates
  `defaultListBatchSize + 20` objects and calls `GetList` with no `Limit` set,
  asserting all of them come back with no `continue` token. Confirmed to fail
  against the pre-fix behavior (returns exactly 500 items with a non-empty
  `continue`) by temporarily reintroducing the old `Limit == 0 -> 500`
  substitution and rerunning, then confirmed to pass again with the fix
  restored.
- `TestStorageImpl_GetList_NegativeLimitReturnsEmpty` -- pins down the
  negative-limit distinction above: a negative `Limit` still returns an empty
  list, not every item.

New test in `pkg/registry/genericrest/store_test.go`:
- `TestStore_DeleteCollection_StopsOnCancellationBetweenPages` -- creates
  `DefaultDeleteCollectionPageSize + 20` objects, wraps the store's
  `storage.Interface` to cancel the context only once the first page's real
  deletes have all completed successfully, and asserts `DeleteCollection`
  stops with a context-cancellation error having deleted only the first page.
  Confirmed to fail against the pre-fix `DeleteCollection` (no explicit page
  size) by temporarily disabling the `DefaultDeleteCollectionPageSize`
  assignment and rerunning, then confirmed to pass again with the fix
  restored.

Full `pkg/registry/...` suite green under `-race`, run 3x with zero
flakiness (aside from the known pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`/
`TestFilesystemStorageWatchPublishing` race in the Watch dispatcher,
independently reproduced on unmodified `main` too). `go vet` clean (aside
from the known pre-existing, unrelated `callstack_test.go` warnings).

## Known residual scope

This fix addresses the truncation itself. Two related gaps in the same code
path are pre-existing (predate this fix, and predate the whole
storage-locking-rewrite investigation) and are **not** addressed here:

- The list's `resourceVersion` and `remainingItemCount` are never populated
  (`setListContinue` only sets `Continue`) -- a well-behaved paginating client
  still can't start a consistent watch from an unpaginated list's
  resourceVersion, or report progress against `remainingItemCount`.
  `StorageImpl.GetCurrentResourceVersion` is currently a stub (`return 0,
  nil`), so there is no cheap, already-correct source for the former; fixing
  it properly is a separate, larger piece of work.
- There is no explicit ceiling on how large an unpaginated List's result can
  grow. Unlike real etcd3-backed Kubernetes clusters (where an unpaginated
  List is normal, bounded mainly by API Priority and Fairness and response
  size limits), this backend's per-object cost (especially in the
  `ResourceVersionFullSpec` branch, which does a `s.get()` -- and, depending
  on the object, a Gob-file read -- per item) is not free. The per-page
  connection release from `getlist-page-connection-hold.md` already prevents
  a large unpaginated List from monopolizing a single pool connection for its
  whole duration, which was the primary safety concern; a loud, explicit
  size ceiling (as opposed to a silent truncation) remains a possible future
  addition if a specific workload demonstrates the need.
