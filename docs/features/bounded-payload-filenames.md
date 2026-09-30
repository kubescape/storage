# Bounded temporary payload filenames

## Problem and current storage ownership

[Issue #398](https://github.com/kubescape/storage/issues/398) reports a
WorkloadConfigurationScanSummary whose permanent payload name fits the filesystem
but whose timestamped staging name fails with `file name too long`. The fix in
[PR #400](https://github.com/kubescape/storage/pull/400) is still needed after the
ContainerProfile storage rework.

The researched upstream baseline is
[`7c1e33455c18de7462abdc44f4114cb33d33e6d8`](https://github.com/kubescape/storage/commit/7c1e33455c18de7462abdc44f4114cb33d33e6d8).
At that revision, `containerProfileSqliteBackend` defaults to false and selects
SQLite-native payload storage only for ContainerProfiles. Other resources,
including workload summaries, retain `StorageImpl` and filesystem payloads. When
the backend is enabled, the legacy instances share its write gate without changing
their payload storage. See the [backend documentation](containerprofile-sqlite-backend.md),
[configuration default](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/config/config.go#L203-L205)
and [backend wiring](https://github.com/kubescape/storage/blob/7c1e33455c18de7462abdc44f4114cb33d33e6d8/pkg/apiserver/apiserver.go#L157-L195).

## Filename behavior

`makeTempPayloadPath` preserves the existing path when its basename plus the
staging suffix fits within 255 bytes. Otherwise it replaces the temporary basename
with the hexadecimal SHA-256 digest of the permanent basename, followed by `.g`
and the original staging suffix. The file stays in the permanent payload's parent
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
  -run '^(TestMakeTempPayloadPath|TestSingleWriter_LongTempPathUnique|TestStorageLongNames|TestMigration_LongNameStagingFiles|TestExport_LongNameRealFilesystem)$' \
  -count=1 -timeout=5m
```

Run the affected existing tests with:

```sh
go test -mod=readonly -race ./pkg/registry/file \
  -run '^(TestSaveObject_.*|TestSingleWriter_.*|TestTG1_.*|TestMigration_.*|TestExport_.*)$' \
  -count=1 -timeout=10m
```

Both commands passed with the race detector on the researched baseline plus this
change, using Go 1.26.8. The new focused regression run took 1.673s; the affected
existing test run, including migration/export scale coverage, took 233.262s.
Formatting and `git diff --check` passed. The full suite was not run.
