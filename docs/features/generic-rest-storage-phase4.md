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

This is part of the storage-locking-rewrite plan's Phase 4 (see
`.omc/plans/storage-locking-rewrite.md`): incrementally proving that `genericregistry.Store` can
be replaced by a much smaller, purpose-built `rest.Storage`, one resource at a time.

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

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated `watch.go` flaky
test); the `knownservers` and `openvulnerabilityexchange` differential suites re-run 3x under
`-race` with zero flakiness. `knownservers`' refactor from a 777-line hand-written
implementation to a thin wrapper over `genericrest.Store` is a zero-regression change — all 15
pre-existing differential tests pass unchanged.
