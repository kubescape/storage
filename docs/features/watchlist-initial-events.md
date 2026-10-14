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
the listed objects, the bookmark, and then the buffered events, dropping an `ADDED` or
`MODIFIED` whose namespace, name and `resourceVersion` the list already contained. A
`DELETED` buffered during the list is always shipped. Writers never wait on the replay:
the watcher keeps draining its inbound channel throughout.

The replay respects the watch's label and field selectors and the `fullSpec` resource
version sentinel, so a full-spec watch list carries full objects like a full-spec list.
`ObjectStore.Watch` (the ContainerProfile SQLite backend) does the same.

## Computed resources

`ConfigurationScanSummary`, `VulnerabilitySummary` and `GeneratedNetworkPolicy` are computed
on read and have nothing to replay from storage. A watch on them with `sendInitialEvents`
returns `405 Method Not Supported`; client-go treats that as non-retriable and falls back to
list plus watch, where the watch is the idle one from #330.

## Tests

- `pkg/registry/file/watch_initial_events_test.go`: replay order and bookmark, selector and
  full-spec handling, the buffering and dedup of writes that land during the list, the
  computed-resource rejection.
- `pkg/registry/genericrest/store_watchlist_test.go`: a client-go reflector in watch-list
  mode syncs from the `Store` and receives later writes on the same stream without ever
  calling `List`.
- `pkg/cmd/server/start_test.go`: the gate stays enabled and the handler-level validation
  accepts `sendInitialEvents` watch requests.
