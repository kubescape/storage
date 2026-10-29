# Syft package metadata type registry

> Status: **implemented**. `pkg/apis/softwarecomposition/packagemetadata` and
> `.../sourcemetadata`, consumed by the `UnmarshalJSON` paths in both copies of
> `syfttypes.go`. Synced to syft v1.55.0.

## Summary

An SBOM document carries each package's ecosystem-specific details in a
`metadata` object tagged with a `metadataType` string — `"apk-db-entry"`,
`"go-module-buildinfo-entry"`, and so on. To decode that object into the right
Go struct, something has to map the string to a type.

Syft does this in `internal/packagemetadata/names.go`. Being under `internal/`,
it cannot be imported, so this repo keeps a hand-maintained copy in
`pkg/apis/softwarecomposition/packagemetadata/packagemetadata.go`. The helper
functions are byte-identical to syft's; only the `jsonTypes` list has to be
kept in step. `sourcemetadata/sourcemetadata.go` does the same, much smaller,
job for the SBOM's *source* descriptor (`"image"`, `"directory"`, `"file"`, …).

Both API versions — `pkg/apis/softwarecomposition` and
`pkg/apis/softwarecomposition/v1beta1` — import these two packages rather than
each carrying their own copy, so there is exactly one list to maintain.

## What happens when the list is stale

Nothing crashes. `ReflectTypeFromJSONName` returns `nil` for a name it does not
know, `UnmarshalJSON` returns `errUnknownMetadataType`, and the caller swallows
that error and keeps the metadata as raw JSON.

So a missing entry is **lossy but safe**: the package is still stored and still
scannable, but its typed metadata is not materialised, and anything reading the
structured fields of that metadata sees nothing. The symptom is quiet — no log
line, no failed write — which is why the list drifts without anyone noticing.

The practical consequence of drift is per-ecosystem. A stale list in a cluster
running a newer syft means the ecosystems added since the last sync (uv and PDM
lockfiles, pnpm and Bun lockfiles, Deno, CPAN, Homebrew, Conda, Terraform
provider locks, GGUF and SafeTensors model files, …) store their metadata
untyped.

## Keeping it in sync

On every `anchore/syft` bump:

1. Diff `internal/packagemetadata/names.go` in the new syft against the
   `jsonTypes` block here. The helper signatures match, so the block can be
   copied across verbatim — that is the safest way to do it, because it
   preserves the legacy-name aliases and the `jsonNamesWithoutLookup` entries
   exactly as syft wrote them.
2. Do the same for `internal/sourcemetadata/names.go`.
3. `go mod tidy`, `go build ./...`, `go test ./pkg/apis/...`.

The legacy aliases matter: they exist so that documents written by older syft
versions still decode. Never drop one, even if the current name is different.

Nothing here is a Kubernetes API struct, so a sync does not require running
`hack/update-codegen.sh`.

## syft v1.55.0 sync

The bump from v1.42.3 to v1.55.0 moves the syftjson schema from 16.0.39 to
16.1.11 — a minor bump inside schema major 16, so no wire break.

Twenty-eight metadata types were added and none removed:

`BitnamiSBOMEntry`, `CpanDistribution`, `CpanUnpackedRelease`, `DartPubspec`,
`DenoLockEntry`, `DenoRemoteLockEntry`, `DpkgArchiveEntry`,
`GitHubActionsUseStatement`, `GolangSourceEntry`, `JavaVMInstallation`,
`PnpmLockEntry`, `BunLockEntry`, `PEBinary`, `PhpPearEntry`,
`PythonPdmLockEntry`, `PythonUvLockEntry`, `SwiplPackEntry`, `OpamPackage`,
`SnapEntry`, `HomebrewFormula`, `AppleAppBundleEntry`, `LuaRocksPackage`,
`TerraformLockProviderEntry`, `DotnetPackagesLockEntry`, `CondaMetaPackage`,
`GGUFFileHeader`, `SafeTensorsModelInfo`, `VcpkgManifest`.

`sourcemetadata` gained `source.SnapMetadata` (`"snap"`) and
`source.OCIModelMetadata` (`"oci-model"`).
