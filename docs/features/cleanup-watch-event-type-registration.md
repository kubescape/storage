# Periodic cleanup dispatches scheme-registered types to watchers

## Summary

`ResourcesCleanupHandler.cleanupNamespace` (`pkg/registry/file/cleanup.go`) reclaims
orphaned, non-user-managed resources on a timer and, when a resource is deleted, notifies
any active watcher via `WatchDispatcher.Deleted`. That call previously always passed a
`*file.PartialObjectMetadata` (`pkg/registry/file/utils.go`) as the deleted object --  an
internal helper struct that only exists to satisfy `runtime.Object` for
`storage.Interface.Delete`'s `metaOut` parameter. It was never registered with the
apiserver's `runtime.Scheme` (`pkg/apiserver/apiserver.go`).

`k8s.io/apiserver`'s `WatchServer.HandleHTTP` encodes every watch event's object through
that same Scheme/Codecs. Any client watching a resource kind the cleanup loop reclaims
(e.g. `workloadconfigurationscans`, `seccompprofiles`) would see the watch stream fail
with `unable to encode watch object *file.PartialObjectMetadata: no kind is registered
for the type file.PartialObjectMetadata` the moment the loop deleted a matching, orphaned
resource with an active watcher (kubescape/storage#405).

## Fix

`cleanupNamespace` now looks up a per-kind constructor in `resourceKindToObjectFunc`
(`pkg/registry/file/cleanup.go`) and has `deleteMetadata` (`pkg/registry/file/utils.go`)
unmarshal the deleted resource's metadata into that scheme-registered type (e.g.
`*softwarecomposition.WorkloadConfigurationScan`) instead of `*file.PartialObjectMetadata`.
`WatchDispatcher.Deleted` is only called when a registered constructor exists for the
kind being cleaned up.

Deprecated kinds handled by `deleteDeprecated` (`applicationprofiles`,
`networkneighborhoods`, `sbomspdxv2p3`, `sbomsummaries`, etc.) no longer have a type in
`pkg/apis/softwarecomposition` or a REST endpoint that could register a watcher, so they
are intentionally omitted from `resourceKindToObjectFunc`; the cleanup loop still deletes
their on-disk payload and SQLite metadata row, it just never reaches the watch
dispatcher for them.

`ContainerProfileKind` ("containerprofile") is registered in `resourceKindToObjectFunc`
too, even though it is not a key of `initResourceToKindHandler`'s base map. Container
profiles are normally cleaned up by `ContainerProfileProcessor.cleanup`
(`containerprofile_processor.go`), but that method builds its own
`resourceToKindHandler` keyed by `ContainerProfileKind` and runs it through the same
`CleanupHandler.CleanupTask` / `cleanupNamespace` / `deleteMetadata` path as every other
cleanup-handled kind, and relevancy-enabled cleanup (`initResourceToKindHandler`) adds
`ContainerProfileKind` to the shared map as well. An earlier version of this fix omitted
`ContainerProfileKind` from `resourceKindToObjectFunc`, which meant a ContainerProfile
delete still succeeded but silently never reached the watch dispatcher instead of
erroring -- caught in review before merge.

## Tests

- `pkg/registry/file/cleanup_watch_dispatch_test.go`:
  `TestCleanupNamespaceDispatchesRegisteredTypeToWatchers` asserts a delete of an orphaned
  `workloadconfigurationscans` resource with an active watcher dispatches a
  `*softwarecomposition.WorkloadConfigurationScan`, not `*file.PartialObjectMetadata`.
  `TestCleanupNamespaceSkipsDispatchForDeprecatedKind` asserts a deprecated kind's delete
  never reaches the watch dispatcher.
- `pkg/registry/file/cleanup_containerprofile_test.go`:
  `TestContainerProfileCleanupDispatchesRegisteredTypeToWatchers` runs
  `ContainerProfileProcessor.cleanup` end to end and asserts a reclaimed container
  profile's delete dispatches a `*softwarecomposition.ContainerProfile` to an active
  watcher.
- `pkg/apiserver/watch_encoding_test.go`:
  `TestWatchEventObjectEncodesThroughApiserverScheme` exercises the actual encode call
  `WatchServer.HandleHTTP` performs (via this package's real `Scheme`/`Codecs`), proving
  `file.PartialObjectMetadata` fails to encode (documenting the historical bug) and that
  the now-dispatched `WorkloadConfigurationScan` type encodes successfully with a valid
  `kind`/`apiVersion` and round-trips its `ObjectMeta`.
