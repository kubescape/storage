# Phase 4, remaining eight resources: generic rest.Storage

## Summary

The eight remaining real per-object resources now each have an optional, gated, hand-written
`rest.Storage` implementation built on `pkg/registry/genericrest.Store`
(see `docs/features/generic-rest-storage-phase4.md`), completing Phase 4's per-resource migration
alongside the three already done (`knownservers`, `openvulnerabilityexchange`, `containerprofile`):

- `collapseconfigurations` (cluster-scoped, real Strategy validation)
- `sbomsyftfiltereds`, `sbomsyfts`, `seccompprofiles` (namespaced, no-op Strategy, singular/plural
  qualified-resource inversion)
- `vulnerabilitymanifests`, `vulnerabilitymanifestsummaries`, `workloadconfigurationscans`,
  `workloadconfigurationscansummaries` (namespaced, no-op Strategy, not inverted)

Each is gated behind its own `config.Config.Custom<Resource>RestEnabled` flag (default `false`;
live validation against armo-dev-stage on 2026-08-31 found zero behavioral divergence across all
11 Phase 4 resources, but that covers one cluster, not every deployment of this binary, so the
default stays conservative). The old `genericregistry.Store`-based implementation remains the
default and the differential-testing reference for every one of them; set a flag to `true` (or
set `CUSTOM_REST_ENABLED`, see `docs/features/custom-rest-enabled-env-var.md`) to opt into the
new implementation. The three "virtual CRD" resources
(`configurationscansummaries`, `generatednetworkpolicies`, `vulnerabilitysummaries`) are
deliberately out of scope: they're generated-on-the-fly, `immutableStorage`-backed resources
using a different, non-persistent registry pattern that doesn't fit this migration.

## Why it matters / how it works

None of these eight resources needed any change to `genericrest.Store` itself -- the pattern
proven on the first three resources (function-value/interface configuration, no Go generics,
Strategy interface satisfied structurally) applies directly. Seven have entirely no-op Strategy
validation (matching `knownservers`/`openvulnerabilityexchange`); `collapseconfigurations` has
real validation (`validateCollapseConfigurationSpec` -- per-prefix entry requirements, duplicate-
prefix rejection, threshold bounds), exercised identically by both implementations since
`genericrest.Store.Create`/`Update` invoke `rest.BeforeCreate`/`rest.BeforeUpdate` against
`r.Strategy` at the same point `genericregistry.Store` does.

`sbomsyftfiltereds`, `sbomsyfts`, and `seccompprofiles` carry the same singular/plural
qualified-resource inversion documented for `containerprofile` (see
`docs/features/generic-rest-storage-containerprofile.md` and
[issue #376](https://github.com/kubescape/storage/issues/376)) -- their `DefaultQualifiedResource`/
`SingularQualifiedResource` values are copied verbatim from each resource's `etcd.go`, not
"corrected", since `DefaultQualifiedResource` determines the on-disk key prefix.

All eight use the plain default `storageImpl` in `apiserver.go`'s wiring (none have a non-default
`storage.Interface` like `containerprofile`'s consolidation-processor-wired instance).

## Verification

Full test suite passes under `-race` (excluding the pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`). Each of the eight resources has a
from-scratch differential suite (16 tests for the seven no-op-Strategy resources; 18 for
`collapseconfigurations`, including its three validation-specific cases) covering standard
CRUD/dry-run/finalizer-delete/pagination/watch/protobuf behavior plus namespace-isolation and
key-derivation-matches (proving both implementations address identical on-disk keys, including
across the three inverted-prefix resources). All 8 packages re-run 3x under `-race` with zero
flakiness (423 subtest runs, 0 failures).
