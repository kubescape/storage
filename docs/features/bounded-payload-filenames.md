# Bounded temporary payload filenames

## Problem and current storage ownership

[Issue #398](https://github.com/kubescape/storage/issues/398) reports a
WorkloadConfigurationScanSummary whose permanent payload name fits the filesystem
but whose timestamped staging name fails with `file name too long`. The fix in
[PR #400](https://github.com/kubescape/storage/pull/400) is still needed after the
ContainerProfile storage rework.

The original researched upstream baseline was
[`7c1e33455c18de7462abdc44f4114cb33d33e6d8`](https://github.com/kubescape/storage/commit/7c1e33455c18de7462abdc44f4114cb33d33e6d8).
This update is rebased onto
[`b69c39067a3514972352d860230ef2df6db1b9c0`](https://github.com/kubescape/storage/commit/b69c39067a3514972352d860230ef2df6db1b9c0).
At the original baseline, `containerProfileSqliteBackend` defaults to false and selects
SQLite-native payload storage only for ContainerProfiles. Other resources,
including workload summaries, retain `StorageImpl` and filesystem payloads. When
the backend is enabled, the legacy instances share its write gate without changing
their payload storage. See the [backend documentation](containerprofile-sqlite-backend.md),
[configuration default](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/config/config.go#L203-L205)
and [backend wiring](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/apiserver/apiserver.go#L157-L195).

## Filename behavior

`makeTempPayloadPath` hashes every staging basename, using the hexadecimal
SHA-256 digest of the permanent basename (including `.g`), followed by `.g`
and the original staging suffix. For the supported `.t` and timestamp/sequence
suffixes, this keeps the staging basename within 255 bytes. The file stays in the permanent payload's parent
directory, preserving the existing same-filesystem rename operation. Permanent
payload filenames and database keys remain unchanged.

The helper serves three staging paths:

- Legacy `saveObject`, with suffix `.t`.
- `singleWriter.nextTempPath`, retaining its timestamp and atomic sequence suffix.
- ContainerProfile rollback export's `writeLegacyPayloadFile`, with suffix `.t`.

The first two paths existed before the rework; the third was added for rollback
export. The researched baseline appended suffixes directly in all three:
[legacy save](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/registry/file/storage.go#L540-L555),
[single writer](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/registry/file/singlewriter.go#L335-L343),
and [exporter](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/registry/file/sqliteobject_export.go#L300-L308).

Retaining `.g` makes shortened staging names match the migration's existing
`.g.t` / `.g.t.` classifier. Migration continues to exclude permanent `.g` files
before checking staging names; no broader filename matching is introduced.
See [migration cleanup](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/registry/file/sqliteobject_migration.go#L692-L710)
and [classifier](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/registry/file/sqliteobject_migration.go#L751-L755).

This change addresses temporary suffix overflow. It does not support permanent
payload basenames or directory paths beyond the filesystem's own limits.

## Collision review and correction

The [requested-changes review](https://github.com/kubescape/storage/pull/400#discussion_r4143828677)
identified a collision in the conditional-hashing helper at reviewed head
[`c6352383`](https://github.com/kubescape/storage/blob/c6352383770a6cbcff5711d3be5ec099df93f37a/pkg/registry/file/storage.go#L466-L479).
Let `L` be 253 `a` characters and `H` be `hex(SHA256(L + ".g"))`, equal to
`43fc0112db9666cee67bfe959e41939958a4ec9ae427b5b9402d83da186ae889`.
Both the overflowing `L.g` and ordinary `H.g` staged at `H.g.t`; this requires
no SHA-256 collision. Distinct keys acquire separate locks, and payload staging
uses `O_TRUNC` before the metadata transaction. An overlapping short-name write
could overwrite the long-name payload before its rename. See the
[legacy staging sequence](https://github.com/kubescape/storage/blob/c6352383770a6cbcff5711d3be5ec099df93f37a/pkg/registry/file/storage.go#L568-L622)
and [per-key locking](https://github.com/kubescape/storage/blob/c6352383770a6cbcff5711d3be5ec099df93f37a/pkg/registry/file/storage.go#L680-L691).

Hashing all staging basenames removes this ordinary-versus-hashed overlap.
A leading-dot or underscore prefix is not a guaranteed reserved namespace:
[workload-summary validation](https://github.com/kubescape/storage/blob/c6352383770a6cbcff5711d3be5ec099df93f37a/pkg/registry/softwarecomposition/workloadconfigurationscansummary/strategy.go#L62-L64)
adds no name restriction, while
[Kubernetes metadata validation](https://github.com/kubernetes/apiserver/blob/v0.35.0/pkg/registry/rest/create.go#L126-L130)
uses [path-segment rules](https://github.com/kubernetes/apimachinery/blob/v0.35.0/pkg/api/validation/path/name.go#L30-L60)
that allow those prefixes. The change makes no claim about eliminating
cryptographic hash collisions or improving the existing crash-atomicity behavior.

The new `TestStorage_LegacyStagingCollision` uses public `Create` calls on a real
filesystem with the legacy writer and no shared gate. A test-only file wrapper
closes L's staged file, waits until H's file is also staged and closed, then lets
L commit before H. Successful opens alone count toward this sequence, accounting
for direct-I/O fallback. Channel barriers have cancellation and a bounded timeout;
no production hook or timing sleep is used. Both writes must succeed and retain
their own names and distinct payloads through GET, full-spec LIST and a fresh
storage instance, with only permanent payload files remaining.

Before changing the helper, this coordinated regression failed with H's rename
reporting `no such file or directory`, reproducing the shared-staging-path defect.
The helper regression also covers the exact L/H pair with both supported suffix
forms. Export cleanup now inspects the directory rather than checking the obsolete
unhashed `finalPath + ".t"` path.

## Regression coverage

The focused tests cover exact filename boundaries, byte length, deterministic
hashing, distinct object names and concurrent single-writer staging uniqueness.
Real-filesystem workload-summary tests use the reported name and a 253-byte name,
with both writer modes and with the shared gate absent or present. The existing
shared-gate rejection of single-writer-disabled requests is asserted rather than
bypassed; CRUD coverage runs for all supported combinations. The tests verify
create, get/list, update, metadata/rename failure rollback, fresh-instance reads,
delete and staging cleanup. Gate-present cases arm the package's existing ungated
write checker and apply the ContainerProfile ownership guard.

Real-filesystem migration tests verify that dry-run preserves shortened staging
files and apply removes them while keeping the permanent payload and object.
Real-filesystem export tests create a 253-byte ContainerProfile in the native store,
export it, verify no staging residue and read the exported object through the
legacy store with its resource version and UID preserved.

Run the new regression tests with:

```sh
go test -mod=readonly -race ./pkg/registry/file \
  -run '^(TestMakeTempPayloadPath|TestSingleWriter_LongTempPathUnique|TestStorageLongNames|TestStorage_LegacyStagingCollision|TestMigration_LongNameStagingFiles|TestExport_LongNameRealFilesystem)$' \
  -count=1 -timeout=5m
```

Run the affected existing tests with:

```sh
go test -mod=readonly -race ./pkg/registry/file \
  -run '^(TestSaveObject_.*|TestSingleWriter_.*|TestTG1_.*|TestMigration_.*|TestExport_.*)$' \
  -count=1 -timeout=10m
```

Validation for this collision correction uses Go 1.26.8 on upstream main
`b69c39067a3514972352d860230ef2df6db1b9c0` plus the rebased PR and local changes.
The focused regressions passed with the race detector in 1.607s. The affected
existing tests, including migration/export scale coverage, passed in 209.481s.
Changed-Go formatting checks and `git diff --check` passed. The full suite was
not run.
