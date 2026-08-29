# Dry-run panic and finalizer-delete data-loss fixes

## Summary

Two independent, previously-undocumented bugs affecting every resource served by this
apiserver, both now fixed:

1. **Dry-run requests panicked.** `genericregistry.DryRunnableStorage.Codec` was `nil` for
   every resource. Any request with `dryRun=All`/`dryRun=Server` (e.g.
   `kubectl apply --dry-run=server`, `kubectl diff`, any server-side-apply-aware client)
   panicked inside the apiserver's dry-run encode/decode path, surfaced to the client as an
   opaque `ServiceUnavailable`.
2. **Finalizer-based deletes silently failed to persist `deletionTimestamp`.** A client that
   set a finalizer on an object and then deleted it received a response claiming
   `deletionTimestamp` was set, but the change was never actually written to disk — clearing
   the finalizer afterward would not complete the deletion, because the stored object never
   had `deletionTimestamp` set in the first place.

## Why it matters

Bug 1 breaks any tooling or client that relies on server-side dry-run validation before a real
write (increasingly common with server-side apply). Bug 2 is a data-integrity issue: any
resource using Kubernetes finalizers (a standard mechanism for cleanup coordination, used by
various controllers and by Kubernetes' own garbage collection) could get stuck in a broken
"terminating but never actually terminating" state, since the API's own response claimed a
state that the underlying storage never reached.

## How it works

### Dry-run fix

`k8s.io/apiserver`'s `Store.CompleteWithOptions` only backfills `DryRunnableStorage.Codec` from
`RESTOptions.StorageConfig.Codec` when `Storage.Storage == nil` (see vendored
`registry/generic/registry/store.go`). Every resource's `NewREST` in this repo pre-sets
`Storage.Storage` to the non-nil custom `StorageImpl` before calling `CompleteWithOptions`, so
that guard is always false and a literal `Codec: nil` was never overwritten.

Fixed with a single shared helper, `registry.NewCodec(scheme)` (`pkg/registry/registry.go`),
using `serializer.NewCodecFactory(scheme).LegacyCodec(v1beta1.SchemeGroupVersion)`. All 13
`pkg/registry/softwarecomposition/*/etcd.go` constructors now pass this instead of `nil` — a
one-line change per file, no per-resource duplication. Verified by
`pkg/registry/dryrun_test.go`, which exercises real dry-run Create/Update against
representative cluster-scoped and namespaced resources and asserts no panic and no persistence.

### Finalizer-delete fix

`StorageImpl.GuaranteedUpdateWithConn`'s (`pkg/registry/file/storage.go`) no-op-update
detection compares a "before" snapshot (`orig := origState.obj.DeepCopyObject()`) against the
`tryUpdate` result (`ret`) via `reflect.DeepEqual`. The snapshot used to be taken *after*
`tryUpdate` had already run. `genericregistry.Store`'s own finalizer-delete `tryUpdate` mutates
its `existing` argument in place (via `markAsDeleting`) and returns that same reference — so
the "before" snapshot ended up reflecting the *already-mutated* state, `reflect.DeepEqual`
was always true, and the write was silently classified as a no-op and never persisted.

Fixed by taking the snapshot before `tryUpdate` runs, on every retry-loop iteration, instead of
after. This required moving the `connKey` context injection (used by processors like
`ContainerProfileProcessor.PreSave`) to the top of the function, since the snapshot's own
`PreSave` call now runs earlier and needs it too — safe, since the connection is already a
parameter available at function entry for this `WithConn` variant. No lock/connection
acquisition order or other control flow was changed. Verified by
`TestStorageImpl_GuaranteedUpdate_MutateInPlacePersists`
(`pkg/registry/file/storage_test.go`), which reproduces the exact mutate-in-place pattern and
confirms the change is genuinely persisted, and by the existing
`pkg/registry/softwarecomposition/knownservers` differential test suite.
