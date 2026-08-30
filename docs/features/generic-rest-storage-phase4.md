# Generic hand-written rest.Storage (Phase 4, generic-rest extraction)

## Summary

Two resources now have an optional, gated, hand-written `rest.Storage` implementation as an
alternative to `k8s.io/apiserver`'s `genericregistry.Store`:

- `knownservers` (`pkg/registry/softwarecomposition/knownservers/custom_rest.go`), gated by
  `config.Config.CustomKnownServersRestEnabled`.
- `openvulnerabilityexchangecontainers`
  (`pkg/registry/softwarecomposition/openvulnerabilityexchange/custom_rest.go`), gated by
  `config.Config.CustomOpenVulnerabilityExchangeRestEnabled`.

Both flags default to `false`. When unset, `pkg/apiserver/apiserver.go` registers these
resources via their original `genericregistry.Store`-based `NewREST`, exactly as before these
flags existed — the old implementation is kept alive as the reference implementation for
differential testing regardless of the flag's value.

This is Phase 4 of an ongoing storage-locking/scalability effort: incrementally proving that
`genericregistry.Store` can be replaced by a much smaller, purpose-built `rest.Storage`, one
resource at a time.

## Why it matters

`genericregistry.Store` is designed around `storage.Interface`'s etcd-shaped contract
(watch-cache, resource-version compare-and-swap semantics, generic dry-run via `runtime.Codec`
round-tripping). This repo's `StorageImpl` is a SQLite+file-backed implementation of that same
interface, which works but carries assumptions (locking, connection pooling) that don't map
cleanly onto etcd's model. A hand-written `rest.Storage` talks to `StorageImpl` directly and can
be reasoned about and evolved independently of `genericregistry.Store`'s internals.

`knownservers` was the first resource migrated, chosen for being low-complexity (trivial type,
no-op strategy, cluster-scoped). `openvulnerabilityexchangecontainers` is the second, and the
first namespaced one — proving out namespace-aware key derivation.

## How it works

- **`pkg/registry/genericrest/store.go`** (new package): a reusable `Store` type providing
  Get/List/Watch/Create/Update/Delete/DeleteCollection/dry-run/finalizer-graceful-deletion
  logic, configured via function-value and interface-typed fields (`NewFunc`, `NewListFunc`,
  `PredicateFunc`, `Strategy`, `DefaultQualifiedResource`/`SingularQualifiedResource`, optional
  `ResetFieldsStrategy`/`TableConvertor`) — mirroring exactly how `genericregistry.Store` itself
  is configured, not via Go generic type parameters. Any resource strategy that already
  satisfies both `rest.RESTCreateStrategy` and `rest.RESTUpdateStrategy` (every strategy in this
  repo does) satisfies the package's `Strategy` interface without modification.
- `KeyFunc`/`KeyRootFunc` pick between `genericregistry.NamespaceKeyFunc`/`NamespaceKeyRootFunc`
  and `genericregistry.NoNamespaceKeyFunc` based on `Strategy.NamespaceScoped()`, the same way
  `genericregistry.Store.CompleteWithOptions` does, so both the OLD and NEW implementations
  address identical on-disk keys for the same resource.
- Dry-run uses a plain `DeepCopyObject` preview rather than `genericregistry.DryRunnableStorage`'s
  `runtime.Codec` round-trip, avoiding any dependency on a codec being wired up correctly.
- `pkg/registry/softwarecomposition/knownservers/custom_rest.go` and
  `.../openvulnerabilityexchange/custom_rest.go` are thin (~40-line) wrappers: `type CustomREST =
  genericrest.Store`, with a `NewCustomREST` that supplies only what's genuinely
  resource-specific (`New`/`NewList`, the label/field selector predicate, qualified resource
  names, and the strategy value).
- Each resource's `custom_rest_test.go` is a differential test suite running identical operation
  sequences against both the OLD (`genericregistry.Store`-based) and NEW (`genericrest.Store`-
  based) implementations, asserting identical behavior. `openvulnerabilityexchange`'s suite adds
  namespace-specific cases (`TestDifferential_NamespaceIsolation`,
  `TestDifferential_KeyDerivationMatches`) and validation-specific cases exercising the
  `spec.Author` required check in `openvulnerabilityexchange/strategy.go`.

## Fixes applied after review

Code review turned up two real behavioral divergences from the `genericregistry.Store`
reference this package is meant to be a faithful, gated alternative to:

- **`DeleteCollection` silently truncated at `StorageImpl.GetList`'s default page size (500
  objects)**: it issued a single `List` call and never followed the returned continue token, unlike
  vendor's `DeleteCollection` (vendor store.go:1298-1362), which pages through until exhausted
  unless the caller explicitly set their own `Limit`. `DeleteCollection` now loops on the continue
  token the same way, deleting the whole collection regardless of size; an explicit caller-supplied
  `Limit` is still honored as a request for just that one page, matching vendor. Covered by
  `TestDifferential_DeleteCollection_PaginatesBeyondDefaultPageSize` (510 objects across both
  implementations) in `openvulnerabilityexchange/custom_rest_test.go` — confirmed to fail against
  the pre-fix code (truncates at 500) before the fix and pass after.
- **Dry-run `Delete` never invoked `deleteValidation`**, in both the hard-delete and the
  graceful/finalizer-preview branches. Vendor's `DryRunnableStorage.Delete` (vendor
  registry/dryrun.go:49-58) always re-validates against the current object under dry-run before
  skipping the actual write; this package's dry-run branches returned early without ever calling
  it, so a validating webhook that would deny the delete was silently bypassed under
  `--dry-run=server`. Both branches now call `deleteValidation` against the pre-mutation object
  and propagate its error, mirroring vendor. Covered by
  `TestDifferential_DryRunDeleteRejectedByValidation` and
  `TestDifferential_DryRunDeleteWithFinalizerRejectedByValidation` (one per branch, using a
  rejecting `deleteValidation` -- `rest.ValidateAllObjectFunc`, used by every other test in this
  suite, can never fail and so cannot exercise this path) -- both confirmed to fail against the
  pre-fix code and pass after.

## Further fixes from the same review (non-blocking, addressed anyway)

A second pass through the review's non-blocking findings:

- **`List`/`Watch` weren't narrowing scope to a single namespace** the way vendor's
  `ListPredicate`/`WatchPredicate` do (vendor store.go:405-412, 1440-1447) when a cluster-scoped
  request's field selector matches exactly one valid namespace. Not a correctness issue --
  `listMetadata`'s own namespace filtering already returned the right results either way -- but a
  perf/fidelity gap against the reference. Added the shared `narrowToSingleNamespace` helper, used
  by both.
- **`deepCopyInto` panicked instead of returning an error** if `src` and `dst`'s concrete types
  ever diverged (a `reflect.Value.Set` type mismatch panics). Now checks `AssignableTo` first and
  returns an error, matching the rest of the package's error-handling style for an apiserver
  storage hot path.
- **Dry-run `Create`'s "already exists" branch skipped `rest.CheckGeneratedNameError`**, unlike the
  real (non-dry-run) path just below it. An exhausted `generateName` retry loop under dry-run would
  have surfaced as a plain `AlreadyExists` instead of a retryable `GenerateNameConflict`. Now routed
  through the same helper.
- **`NewStore` validated `DefaultQualifiedResource` but not `SingularQualifiedResource`.** A missing
  one would have failed silently at `GetSingularName()` (returning `""`) rather than at
  construction time. Now validated the same way.
- **The displaced OLD `rest.Storage` was never `Destroy()`d** when a `Custom*RestEnabled` flag
  swaps it out of `apiGroupInfo.VersionedResourcesStorageMap`. Traced through to confirm this is a
  no-op today (the shared `storageImpl` is injected directly into the OLD
  `genericregistry.Store`, so `CompleteWithOptions` never sets a `DestroyFunc`) but added the call
  anyway so this stays correct if that wiring ever changes.
- **Package-level test coverage for `genericrest.Store` itself.** All prior coverage was indirect,
  via each resource package's differential suite; there was no test proving the "no mutable state
  after construction" claim under real concurrent access. Added
  `pkg/registry/genericrest/store_test.go`'s `TestStore_ConcurrentAccess`, exercising concurrent
  Create/Get/Update/Delete (including deliberate same-key contention forcing
  `GuaranteedUpdate`'s conflict-retry path) against one shared `Store` instance, run under `-race`.
- Several source comments cited `.omc/plans/storage-locking-rewrite.md`, a local planning doc that
  is gitignored and was never tracked in this repo -- a dead link for any other contributor.
  Repointed to this doc instead, in every file this PR touches.

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated `watch.go` flaky
test); the `knownservers`, `openvulnerabilityexchange`, and `genericrest` suites re-run 3x under
`-race` with zero flakiness. `knownservers`' refactor from a 777-line hand-written
implementation to a thin wrapper over `genericrest.Store` is a zero-regression change — all 13
pre-existing differential tests in that package pass unchanged (`openvulnerabilityexchange`'s
suite has 22, after the 3 tests added in the first review-fix pass above).
