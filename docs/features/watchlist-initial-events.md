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

The buffered events are not trusted for the state itself: both backends publish an
object's events after releasing their lock on it, so two updates can be published in the
wrong order, and the replay must not finish a client's initial sync on an older version
than the store holds. The buffered events only say which objects moved. Once the list is
in, every object a buffered event names is re-read, after the list and so after any write
the list could have straddled, and the client gets the transition from the snapshot to
what the store holds now: nothing when the object is unchanged, a `MODIFIED` when the same
incarnation (the uid, since versions restart at every creation) has a newer version, a
`DELETED` when it is gone, an `ADDED` when it was not listed, and a `DELETED` followed by an
`ADDED` when the name now belongs to another incarnation. A re-read that fails ends the
watch with an error event instead of a bookmark, so the client relists.

The re-reads take the per-object lock, and a writer publishes an object's event while it
still holds that lock (`StorageImpl.Delete` does, for one), so the watcher keeps receiving
while its reads run: the reads happen on their own goroutine and the main loop goes on
taking live events. What arrives during a round is settled against the reads before the
bookmark: an event of the incarnation the client holds ships when its version is newer
and is dropped otherwise, a delete of that incarnation ships, a later event of a deleted
incarnation (a delete is final, a uid never returns) is dropped, and a name the client
does not hold ships as an `ADDED`. An event of another incarnation while one is held
cannot be ordered against the reads, since versions restart at every creation, so the
next round re-reads that name. Rounds are bounded (`maxReconcileRounds`); a name
recreated on every round for that long ends the watch with an error event and the client
relists. A stopped watch cancels the reads, waits for them to return and emits nothing
more. Writers never wait on the reads; while the replay is being shipped they wait only
if the client's outbound buffer is full, as on any watch. A notification that is
published after the bookmark but belongs to an older commit reaches the client live, as
it would on any watch; that weakness of the live stream predates the replay.

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
  last, writes between list pages applied before the bookmark, a failed re-read ending the
  watch with an error, the transition table against a stub store (updates and deletes
  before and after the snapshot, recreates on either side of it, notifications published
  out of commit order, writes whose publication is still pending), live events taken and
  settled while a re-read is paused, a recreate published out of order during a re-read
  taking a second round, the settle table (newer, older, deleted, dead incarnation,
  unknown name, other incarnation, unparsable version), the round bound ending in an
  error, a stop during a re-read closing the watch without events, and the real-store
  regression where a `Delete` commits while the re-read of the same object is in flight
  and the replay still ends with the bookmark. Also the object key for namespaced,
  all-namespace, cluster-scoped and named watches, the bookmark version, the
  minimum-version refusal, named replays on both backends, the computed-resource rejection.
- `pkg/registry/file/watch_initial_events_objectstore_test.go`: the same against the real
  ContainerProfile SQLite backend, including the reordered update and delete-recreate
  sequences.
- `pkg/registry/genericrest/store_watchlist_test.go`: a client-go reflector in watch-list
  mode syncs from the `Store` and receives later writes on the same stream without ever
  calling `List`.
- `pkg/cmd/server/start_test.go`: the gate stays enabled and the handler-level validation
  accepts `sendInitialEvents` watch requests.
