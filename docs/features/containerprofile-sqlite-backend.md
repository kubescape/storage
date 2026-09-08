# ContainerProfile SQLite-native backend (prototype)

## Summary

`config.Config.ContainerProfileSqliteBackend` (default `false`) selects a second storage backend
for the `containerprofiles` resource: `file.ObjectStore`
(`pkg/registry/file/sqliteobject_*.go`), which keeps the object's payload **inside the SQLite
database** next to its metadata row and writes metadata row, payload and `time_series` row in
**one transaction**. The legacy `StorageImpl` (metadata row in SQLite + gob payload file, two
commit points) stays the default and serves the other 13 kinds unchanged.

This is the time-boxed prototype of `.omc/plans/full-acid-storage-architecture.md` (§7.6). It is
**not production-ready**: there is no data migration for existing profiles, `cleanup.go` and
`GeneratedNetworkPolicyStorage` are not re-pointed, and the 13 legacy kinds do not share the
write gate. Its purpose is the go/no-go evidence the design asks for — differential parity with the
legacy store, INV-1..5 under test, and the Step 1-L Tier B A/B.

## Why it matters

Every member of the row/payload divergence family the storage investigation catalogued (E3, B16,
B19, B3, the PC-DIV shapes, `get()`'s self-repair deletes, the orphan temp-file reaper, the `Stat`
pre-check) exists because the metadata row and the payload file commit separately. With both in
one SQLite transaction none of those states is representable, and the compare-and-swap becomes one
`UPDATE … WHERE rv=:rv AND uid=:uid`.

## What changes when the flag is on

| Piece | Behaviour |
|---|---|
| Schema | Migrations 3–4 (always applied, additive): `metadata.rv INTEGER`, `metadata.uid TEXT` (both nullable; legacy kinds leave them NULL) and `payloads(kind, namespace, name, encoding, body BLOB)`. |
| Payload | The object converted to `v1beta1` and JSON-marshalled (`payloads.encoding = json/v1beta1`); the original `TypeMeta` is preserved. |
| Create | Prepare (PreSave, checksum, encode) outside any transaction, then under the gate: in-transaction TS admission (`json_extract` on the base row; Completed/Full or TooLarge base → refused, transaction rolled back), `INSERT … ON CONFLICT DO NOTHING` (→ `KeyExists`), payload insert, `time_series` insert. |
| Update | `UPDATE metadata … WHERE rv=:rv AND uid=:uid`; `changes()==0` → conflict → backoff, re-read, retry (the single writer's loop). `changes()!=1` on the payloads `UPDATE` → `ROLLBACK` + `InternalError` (K-6). |
| Delete | metadata `RETURNING` + payloads + `time_series` in one transaction; absent key → `KeyNotFound`. |
| Get / List | One `SELECT … JOIN payloads` (autocommit, WAL snapshot, no per-key lock). Metadata-only GET/LIST run the legacy statements, so continue tokens stay rowids. |
| Consolidation | `WithConnection` hands the pass a read handle; `BeginTransaction` opens a staged write set; base save (CAS), `time_series` rewrite and the processed-TS deletes (each with its own `rv`/`uid` predicate, R4) commit together under the gate; any failed CAS rolls the whole tick back and the pass retries once. |
| Write gate | Caller-side two-lane FIFO ticket semaphore with `highBurstLimit` fairness, ctx-cancellable, owning one dedicated pool connection; prepare → ticket → `BEGIN IMMEDIATE` → `COMMIT` → release → dispatch. `Close()` returns the connection before `Pool.Close` (K-5). |
| Checkpointing | `PRAGMA wal_autocheckpoint=0` on **every** pool connection (`file.PoolOptions.DisableAutoCheckpoint`, K-3); a supervised background goroutine runs `PRAGMA wal_checkpoint(PASSIVE)` when the `-wal` file exceeds a threshold after a gated commit, and on a timer. |
| Ownership guard | The legacy default `StorageImpl` gets `SetForeignKinds(file.IsContainerProfileKind)`: every full-object operation on a containerprofile key (`get`'s payload branch, fullSpec list, `getListWithSpec`, `appendGobObjectFromFile`, `delete`, `CreateWithConn`, `GuaranteedUpdateWithConn`, the single-writer create/update) returns an `InternalError` **before** touching a row, a file or a self-repair delete. Metadata-only reads are served. |

Flag off is byte-identical to before: no pragma, no guard, legacy store everywhere.

## Metrics

`storage_sqlite_write_hold_seconds{path}`, `storage_write_gate_wait_seconds{priority}`,
`storage_sqlite_busy_wait_seconds`, `storage_cp_cas_conflict_total{op}`,
`storage_cp_ownership_refusal_total{op}`, `storage_sqlite_wal_pages`,
`storage_sqlite_freelist_count`, `storage_sqlite_checkpoint_total{outcome}`. Gate commits are
also counted under the existing `storage_single_writer_commit_total{kind="containerprofiles"}`.

## Known, intended divergences from the legacy store

The differential suites (`pkg/registry/file/sqliteobject_differential_test.go`,
`pkg/registry/softwarecomposition/containerprofile/backend_differential_test.go`) assert these as
*what* differs, and that nothing else does:

1. **AfterCreate crash atomicity** — a TS create whose `time_series` write fails leaves an object
   without a series row on the legacy store; nothing on the new one.
2. **TS admission after base completion** — a base that becomes Completed/Full between PreSave's
   read and the commit is admitted by the legacy store and refused by the new one.
3. **Rowid-stable pagination** — the legacy `INSERT OR REPLACE` re-inserts an updated row, so a
   paginating LIST sees it again; the new `UPDATE` keeps the rowid (continue tokens of later rows
   differ by one per such re-insert).
4. **`creationTimestamp` precision** — gob keeps nanoseconds, JSON (RFC 3339) keeps seconds.
5. **R4 per-TS CAS** — a TS object updated during a tick is silently deleted by the legacy store;
   the new store conflicts once, retries and merges the update.
6. **nil vs empty collections** (found by the suite, not in the design's list) — `v1beta1` spec
   collections without `omitempty` (`execs`, `opens`, `capabilities`, `endpoints`, …) decode as
   empty slices from JSON and as nil from gob, so REST renders `[]` where the legacy store renders
   `null`.

## Not in the prototype

Data migration of existing rows/files, `cleanup.go`'s CP arm (it still walks files; with the flag
on it finds none), `GeneratedNetworkPolicyStorage`'s full-spec list (it reads through the guarded
default instance and now fails loudly rather than deleting owned rows), the shared gate for the
13 legacy kinds, rollback/downgrade tooling, `main.go` sequencing. The X-A frozen gate
(Lane 0) is not on this branch's base; INV-3 is asserted as parity between the backends.
