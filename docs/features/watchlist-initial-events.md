# Watch list: initial events and the end-of-list bookmark

Issue: [#320](https://github.com/kubescape/storage/issues/320), follow-up to [#318](https://github.com/kubescape/storage/issues/318) and [#330](https://github.com/kubescape/storage/pull/330).

## What changed

A watch with `sendInitialEvents=true` now starts with the current objects under the key as
`ADDED` events, followed by a `BOOKMARK` annotated `k8s.io/initial-events-end: "true"` whose
`resourceVersion` is the one a list of the same key returns. Live events follow on the same
stream. This is the protocol client-go reflectors use with the `WatchListClient` feature,
and what Rancher's agents expected in #318. The `WatchList=false` feature-gate override in
`pkg/cmd/server/start.go` is gone, so the apiserver handler accepts these requests again.

## How the replay avoids missing or duplicating writes

The store has no global revision to anchor a list and a watch on, so `StorageImpl.Watch`
registers the watcher with the dispatcher first and lists afterwards. While the list runs
the watcher buffers live events instead of shipping them. Once the list is in, it ships
the listed objects, then the buffered events reconciled against the snapshot, and only
then the bookmark, so the state a client holds when it finishes its initial sync is one
that existed even when the list's pages straddled writes.

The reconciliation keys on the object's incarnation, its uid and creation time, because
versions restart at every creation. For each object, the snapshot read happened at some
point of its buffered history: the last `ADDED` or `MODIFIED` of the listed incarnation at
the listed `resourceVersion` marks that point when the object was listed, the last
`DELETED` marks it when it was not, and everything up to that point is dropped as already
represented. Without such a mark, the history of an earlier incarnation of a listed name
is dropped as superseded, and so is an `ADDED` or `MODIFIED` of the listed incarnation at
or below the listed version; everything else ships in order, so a delete and recreate
after the read reaches the client as a delete and an add. Writers never wait on the
replay: the watcher keeps draining its inbound channel throughout.

The bookmark's `resourceVersion` is the one a list of the same key returns, raised to the
version of any update shipped before it. When the client asks for a numeric minimum
(`resourceVersionMatch=NotOlderThan`) the snapshot cannot prove, the watch is refused
with the standard too-large-resource-version error, which client-go answers by retrying
without a minimum.

The replay respects the watch's label and field selectors and the `fullSpec` resource
version sentinel, so a full-spec watch list carries full objects like a full-spec list,
and a named watch (a `metadata.name` selector, which `Store.Watch` turns into a
non-recursive key) reads metadata only unless `fullSpec` was asked for.
`ObjectStore.Watch` (the ContainerProfile SQLite backend) does the same.

## Computed resources

`ConfigurationScanSummary`, `VulnerabilitySummary` and `GeneratedNetworkPolicy` are computed
on read and have nothing to replay from storage. A watch on them with `sendInitialEvents`
returns `405 Method Not Supported`; client-go treats that as non-retriable and falls back to
list plus watch, where the watch is the idle one from #330.

## Tests

- `pkg/registry/file/watch_initial_events_test.go`: replay order and bookmark, selector and
  full-spec handling, the buffering of writes that land during the list with the bookmark
  last, writes between list pages applied before the bookmark, the reconciliation table
  (updates, deletes and recreates before and after the snapshot, across incarnations),
  the bookmark version, the minimum-version refusal, named replays on both backends, the
  computed-resource rejection.
- `pkg/registry/genericrest/store_watchlist_test.go`: a client-go reflector in watch-list
  mode syncs from the `Store` and receives later writes on the same stream without ever
  calling `List`.
- `pkg/cmd/server/start_test.go`: the gate stays enabled and the handler-level validation
  accepts `sendInitialEvents` watch requests.
