# Phase 4, third resource: containerprofiles generic rest.Storage

## Summary

`containerprofiles` now has an optional, gated, hand-written `rest.Storage` implementation
built on `pkg/registry/genericrest.Store` (see `docs/features/generic-rest-storage-phase4.md`),
as a parallel alternative to the `genericregistry.Store`-based implementation in `etcd.go`. This
is Phase 4's third migrated resource and by far its most complex: a consolidation processor
(time-series writes, size limits, completed-profile immutability), real Strategy validation
(NetworkNeighbor wildcard grammar), and a singular/plural qualified-resource inversion that
determines the on-disk key prefix.

Gated behind `config.Config.CustomContainerProfileRestEnabled` (default `true` as of 2026-08-31,
after live validation against armo-dev-stage); the old `genericregistry.Store`-based
implementation remains available as a fallback (set the flag to `false`) and as the
differential-testing reference. Live validation also surfaced a pre-existing PATCH/UID bug
([issue #385](https://github.com/kubescape/storage/issues/385)) confirmed identical on both
implementations -- not a regression introduced by this migration, and not a reason to keep the
old implementation as the default.

## Why it matters / how it works

**None of `containerprofiles`' real complexity required any change to `genericrest.Store`
itself.** The consolidation processor's hooks (`PreSave`/`AfterCreate`, which redirect
time-series reports from node-agent into raw SQL writes via `WriteTimeSeriesEntry`, and enforce
size/completion limits) live inside `StorageImpl.Create`/`GuaranteedUpdate` themselves, not in
either REST layer -- any `storage.Interface` caller inherits them identically. Likewise,
`ContainerProfileStrategy`'s real `PrepareForUpdate`/`Validate` logic (the completed-profile
immutability guard, `NetworkNeighbor` IP/DNS wildcard validation) already satisfies
`genericrest.Store`'s `Strategy` interface without modification, exactly like the previous two
resources' no-op strategies did.

`pkg/apiserver/apiserver.go`'s gated branch wires `NewCustomREST` with the same non-default
`containerProfileStorageImpl` (the processor-wired `storage.Interface`) that the old `NewREST`
uses today -- not the plain `storageImpl` the first two migrated resources use.

**The one genuine gotcha: the singular/plural qualified-resource inversion.**
`containerprofile/etcd.go` sets `DefaultQualifiedResource: softwarecomposition.Resource("containerprofile")`
(singular) and `SingularQualifiedResource: softwarecomposition.Resource("containerprofiles")`
(plural) -- backwards relative to normal convention. This is not cosmetic:
`DefaultQualifiedResource` feeds directly into the on-disk/SQLite key prefix (via
`optsGetter.GetRESTOptions` -> `SimpleStorageFactory.ResourcePrefix`, `resource.Group + "/" +
resource.Resource`), so every ContainerProfile is actually stored under a **singular** key
prefix. `custom_rest.go`'s `NewCustomREST` copies both values verbatim rather than "correcting"
them -- swapping them would silently change the key prefix and make every existing
ContainerProfile invisible until a data migration renamed on-disk keys. See
[#376](https://github.com/kubescape/storage/issues/376) for tracking a proper fix (a genuine
data migration, out of scope for this REST-layer migration), which also affects `sbomsyfts`,
`sbomsyftfiltereds`, and `seccompprofiles`.

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`); a from-scratch differential suite (21
tests) covers standard CRUD/dry-run/finalizer-delete/pagination/watch/protobuf behavior plus
three cases unique to this resource: the completed-profile immutability guard, NetworkNeighbor
validation rejection, and proof that the consolidation processor's time-series-write hook fires
identically for both implementations (`TestDifferential_ConsolidationProcessorHooksFireIdentically`,
verified via `ListTimeSeriesWithData` on the underlying pool). `TestDifferential_KeyDerivationMatches`
confirms both implementations address identical on-disk keys despite the inversion. Re-run 3x
with zero flakiness.
