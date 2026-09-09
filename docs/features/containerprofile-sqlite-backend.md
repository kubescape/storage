# ContainerProfile SQLite-native backend

## Summary

`config.Config.ContainerProfileSqliteBackend` (default `false`) selects a second storage backend
for the `containerprofiles` resource: `file.ObjectStore`
(`pkg/registry/file/sqliteobject_*.go`), which keeps the object's payload **inside the SQLite
database** next to its metadata row and writes metadata row, payload and `time_series` row in
**one transaction**. The legacy `StorageImpl` (metadata row in SQLite + gob payload file, two
commit points) stays the default and serves the other 13 kinds unchanged.

This is the production form of `.omc/plans/full-acid-storage-architecture.md` (Revision 2): the
prototype's store, gate and checkpointer, plus the pieces the prototype left out — the startup
data migration (§8), the cleanup arm on rows (K-4), `GeneratedNetworkPolicyStorage` on the CP
store (§5.6 row 9) and the shared write gate for the 13 legacy kinds
(`docs/features/write-gate-sharing.md`). Still to come before the flip: the reverse-export tool
and the legacy-file deletion step (§8.4), and the soak.

## Why it matters

Every member of the row/payload divergence family the storage investigation catalogued (E3, B16,
B19, B3, the PC-DIV shapes, `get()`'s self-repair deletes, the orphan temp-file reaper, the `Stat`
pre-check) exists because the metadata row and the payload file commit separately. With both in
one SQLite transaction none of those states is representable, and the compare-and-swap becomes one
`UPDATE … WHERE rv=:rv AND uid=:uid`.

## What changes when the flag is on

| Piece | Behaviour |
|---|---|
| Schema | Migrations 3–5 (always applied, additive): `metadata.rv INTEGER`, `metadata.uid TEXT` (both nullable; legacy kinds leave them NULL), `payloads(kind, namespace, name, encoding, body BLOB)` and `migration_state(name, state, counts, updated_at)` (the data migration's done-flag). |
| Payload | The object converted to `v1beta1` and JSON-marshalled (`payloads.encoding = json/v1beta1`); the original `TypeMeta` is preserved. |
| Create | Prepare (PreSave, checksum, encode) outside any transaction, then under the gate: in-transaction TS admission (`json_extract` on the base row; Completed/Full or TooLarge base → refused, transaction rolled back), `INSERT … ON CONFLICT DO NOTHING` (→ `KeyExists`), payload insert, `time_series` insert. |
| Update | `UPDATE metadata … WHERE rv=:rv AND uid=:uid`; `changes()==0` → conflict → backoff, re-read, retry (the single writer's loop). `changes()!=1` on the payloads `UPDATE` → `ROLLBACK` + `InternalError` (K-6). |
| Delete | metadata `RETURNING` + payloads + `time_series` in one transaction; absent key → `KeyNotFound`. |
| Get / List | One `SELECT … JOIN payloads` (autocommit, WAL snapshot, no per-key lock). Metadata-only GET/LIST run the legacy statements, so continue tokens stay rowids. |
| Consolidation | `WithConnection` hands the pass a read handle; `BeginTransaction` opens a staged write set; base save (CAS), `time_series` rewrite and the processed-TS deletes (each with its own `rv`/`uid` predicate, R4) commit together under the gate; any failed CAS rolls the whole tick back and the pass retries once. |
| Write gate | Caller-side two-lane FIFO ticket semaphore with `highBurstLimit` fairness, ctx-cancellable, owning one dedicated pool connection; prepare → ticket → `BEGIN IMMEDIATE` → `COMMIT` → release → dispatch. Built in `main.go` beside the pool and shared by the ObjectStore, the legacy `StorageImpl`, the cleanup handler and the data migration; `Close()` returns the connection before `Pool.Close` (K-5). |
| Checkpointing | `PRAGMA wal_autocheckpoint=0` on **every** pool connection (`file.PoolOptions.DisableAutoCheckpoint`, K-3); a supervised background goroutine runs `PRAGMA wal_checkpoint(PASSIVE)` when the `-wal` file exceeds a threshold after a gated commit, and on a timer. |
| Ownership guard | The legacy default `StorageImpl` gets `SetForeignKinds(file.IsContainerProfileKind)`: every full-object operation on a containerprofile key (`get`'s payload branch, fullSpec list, `getListWithSpec`, `appendGobObjectFromFile`, `delete`, `CreateWithConn`, `GuaranteedUpdateWithConn`, the single-writer create/update) returns an `InternalError` **before** touching a row, a file or a self-repair delete. Metadata-only reads are served. |
| `GeneratedNetworkPolicyStorage` | Its full-spec ContainerProfile list reads through the `containerprofiles` resource's own `storage.Interface` (the ObjectStore under the flag; the processor-wired legacy instance otherwise), never the default instance whose `get()` deletes the shared row of a key without a file. Its `knownservers` read stays on the default instance. |
| Cleanup | `ContainerProfileKind` is never in the generic relevancy walk's handler map (`initResourceToKindHandler`): the ContainerProfile arm — `deleteByTemplateHashOrWlid` plus, with relevancy on, the two missing-annotation handlers (`ResourcesCleanupHandler.ContainerProfileHandlers`) — runs from `ContainerProfileProcessor.cleanup()` only. Under the flag the handler carries the ObjectStore (`SetContainerProfileStore`, wired in `apiserver.go`), enumerates the namespace's **rows** and reclaims through `ObjectStore.Delete` (one gated transaction over the three tables, `Deleted` dispatched after). No CP file is read, written or removed. Flag-off keeps the file walk. |

Flag off is byte-identical to before: no pragma, no guard, no gate, no migration, legacy store
everywhere.

## Data migration (`file.MigrateContainerProfiles`)

Runs synchronously in `main.go` at every start with the flag on, after the pool and the gate are
built and **before** the cleanup goroutine and the API server (design §8.2, R2). Every batch is
one gated `BEGIN IMMEDIATE … COMMIT` (50 objects by default) containing only SQL on bytes prepared
before the ticket: the gob decode of a legacy file (the external `/usr/bin/migration` tool as the
fallback on a gob type mismatch — the last time it runs for this kind), and the JSON encode, run
on a pool connection released before the ticket. A PASSIVE checkpoint follows the last batch.
Legacy `.g` files are **left in place** (§8.4: they are the rollback).

The reconcile of steps 2 and 2b runs on **every** start (R3); the done-flag in `migration_state`
gates only the file sweeps (steps 3–4).

| Step | Predicate | Shape (`storage_cp_migration_total{shape,source}`) | Action |
|---|---|---|---|
| 2 | `metadata` row of kind `containerprofile` with `rv IS NULL` **or** no `payloads` row | `migrated` | file decoded, row and file agree on `resourceVersion`: metadata JSON rewritten with `rv`/`uid`, payload inserted |
| | | `diverged` | row and file disagree on `resourceVersion` (a PC-DIV shape, met once): the object gets `max(rowRV, payloadRV)+1`; PreSave's non-TS revert applied (a Completed row beats a Learning file) |
| | | `row_without_file` | no `.g` file: the row is deleted (today's `get()` self-repair, done once) |
| | | `legacy_rewrite{source=file}` | `rv IS NULL` **with** a `payloads` row — a legacy writer replaced the row after a rollback (K-1). The legacy writer wrote its file before its row, so the **file** is its content and the `payloads` body is stale: the body is rebuilt from the file; `rv` is the row JSON's `resourceVersion` (no `+1`; when a crash left the file one ahead, the larger persisted version wins and the JSON is rewritten to match) |
| | | `legacy_rewrite{source=payloads}` | same, no file (a row-only legacy write): the `payloads` body is kept, re-stamped at `rv` |
| | | `undecodable` | neither gob nor the tool decoded the file: skipped and counted, **never deleted**, the file left for the export tool (PM-2) |
| 2b | `payloads` row of kind `containerprofile` with no `metadata` row | `orphan_payload` | a legacy `deleteLocked`/cleanup delete after a rollback removes row and file, never the `payloads` row (K-2); without this every `Create` of the key fails on the UNIQUE constraint. Deleted. |
| 3 (done-flag) | `.g` file under `/data/<group>/containerprofile/` with no row | `file_without_row` | imported at the file's `resourceVersion` and UID |
| 4 (done-flag) | `*.g.t*` staging files | `temp_file` | removed (never committed) |

Every non-`migrated` outcome is logged per key at Warning: this is where the field frequency of
the divergence shapes is measured. The done-flag is written only when the sweep met no undecodable
file, so such a file stays reported at every start until an operator acts.

Idempotent (a repaired row no longer matches the predicate; the second start reconciles nothing)
and resumable (a crash — an error, a panic, or the process dying inside a batch — rolls that batch
back; the next start completes from the same predicate). A migration error is fatal at startup:
the backend never serves a half-reconciled store, and turning the flag off is the rollback.

`config.Config.ContainerProfileMigrationDryRun` runs the reconcile in count-only mode (no gate,
nothing written, the done-flag untouched) and logs the counts a real run would produce — the census
§8.3 requires before the flag is turned on anywhere. It is refused together with the backend flag.

**Rollback order (§8.4):** reverse-export first, downgrade second. The old binary's `get()` deletes
the metadata row of any CP key whose file is missing, and its `INSERT OR REPLACE` nulls `rv`/`uid`;
the every-start reconcile repairs the latter on re-enable (`legacy_rewrite`, `orphan_payload`).
The reverse-export tool is not on this branch yet.

**Deployment precondition (R12):** one storage pod at a time (the chart's `replicas: 1` +
`strategy: Recreate`); nothing in the database fences an older binary.

## Metrics

`storage_sqlite_write_hold_seconds{path}` (the migration's batches are `path="cp_migration"`),
`storage_write_gate_wait_seconds{priority}`, `storage_sqlite_busy_wait_seconds`,
`storage_cp_cas_conflict_total{op}`, `storage_cp_ownership_refusal_total{op}`,
`storage_cp_migration_total{shape,source}`, `storage_sqlite_wal_pages`,
`storage_sqlite_freelist_count`, `storage_sqlite_checkpoint_total{outcome}`,
`storage_sqlite_ungated_write_total{op,table}` (the AC-G1 production canary; alert at > 0). Gate
commits are also counted under the existing `storage_single_writer_commit_total`.

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

## Tests

`sqliteobject_migration_test.go` drives a real legacy `StorageImpl` (no guard, no gate, a second
pool on the same database file) as the "old binary" and the gated pool + ObjectStore as the "new
binary": the plain migration (content, RV and UID preserved, files kept, CAS live afterwards,
second start a no-op); every reconcile shape with its own fixture; the R3/K-1 rollback cycle (the
stale-body and permanent-conflict symptoms before the reconcile, the repair from the file after
it, `rv == json resourceVersion`, the next CAS succeeding) and its row-only variant; the K-2
orphan (the UNIQUE failure on `Create` before, success after); resumability under an injected
error, a panic, and a killed child process (`os.Exit` inside the second batch, real files, the
parent resumes); the dry run's counts predicting the real run and writing nothing; and that a
batch waits its turn behind another gate holder. `sqliteobject_cleanup_test.go` and
`sqliteobject_gnp_test.go` pin the cleanup and GNP re-pointing; all of them fail on the previous
wiring. The X-A frozen gate (Lane 0) is not on this branch's base; INV-3 is asserted as parity
between the backends.
