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
(`docs/features/write-gate-sharing.md`) and the `cpexport` rollback tool. Still to come before the
flip: the legacy-file deletion step (§8.4) and the soak.

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

Flag off keeps the legacy store everywhere, without the backend's pragma, ownership guard,
write gate or startup migration. The rollback-safety checks described below still apply.

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
| 2b | `payloads` row of kind `containerprofile` with no `metadata` row | `orphan_payload` | a `repairDelete` self-repair on one of the 3 undecodable-file `get()` sites (a corrupt or unmigratable `.g` file) still leaves its `payloads` row behind — the read-time fallback below can't safely serve a stale body there, only the every-start reconcile can. The missing-file branch also leaves payloads behind for excluded time-series rows; only an `rv IS NULL`, non-time-series ContainerProfile receives payload cleanup there. See "Rollback safety: read-time fallback" below. An orphan that reaches ObjectStore insertion would conflict with the payloads UNIQUE constraint; startup reconciliation removes it first. Deleted. |
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

## Rollback safety: read-time fallback (`.omc/plans/rollback-safety-guard.md`)

Simply turning `ContainerProfileSqliteBackend` back off — no binary change, a config edit — is a
*different* hazard from the downgrade below, and closer at hand: the flag-off `StorageImpl` runs
in the *same*, current binary, and its `get()`'s missing-file self-repair used to delete the
metadata row of any key the ObjectStore had created or updated since the flip (no `.g` file was
ever written for it) — the object destroyed, not merely invisible, with zero confirmation and no
`cpexport` step forcing itself on the operator first.

The same-binary guard changes the missing-file path as follows:

1. **`get()` serves the object from its `payloads` body** whenever all four database conditions
   hold: the metadata row exists, `rv IS NOT NULL` (the row is ObjectStore- or migration-owned),
   `is_time_series = 0`, and a `payloads` row exists. Every legacy write nulls `rv`/`uid` via
   `INSERT OR REPLACE`, so the fallback excludes rows a legacy writer has touched since the flip,
   including a `.g` file whose rename was lost to a crash. `ResourceVersion`/`UID` are stamped
   from the metadata row's columns, using the same conversions as `cpexport`.
2. **Inspection failures preserve data.** A database query or payload decode failure returns a
   non-NotFound error and retains both rows. A positively ineligible result can proceed to
   metadata self-repair; it is distinct from an inspection failure.
3. **Payload cleanup is narrowly scoped.** The shared `repairDelete` still deletes metadata only.
   Only the missing-file branch also deletes a ContainerProfile's payload when inspection
   establishes an `rv IS NULL`, non-time-series row. Corrupt-file reads, migration-tool failures
   and time-series rows keep their previous metadata-only repair behavior.
4. **`Create` protects fallback-visible objects**, including with `singleWriterEnabled=false`:
   its database check prevents a missing legacy file from allowing an existing object to be
   overwritten. The single-writer commit path retains its own metadata existence recheck.
5. **`Delete` propagates payload cleanup failures.** `deleteLocked` deletes payloads before
   metadata and returns a `DeletePayloads` error to the caller instead of reporting success.

The three undecodable-`.g`-file repair sites remain outside this fallback: a legacy write may
have superseded the SQLite body, so a corrupt or unmigratable file still triggers the existing
metadata-only self-repair. Existing but stale `.g` files are also out of scope: file reads still
win over SQLite payloads. The fallback does not make those files current.

**Advisory-only, not a substitute for `cpexport`**: at every flag-off startup, a bounded census
(`LogFallbackEligibleContainerProfilesCensus`, its own `context.WithTimeout`, never `Fatal`) logs
a Warning with the count and example keys satisfying the four database conditions. These are
**database candidates**, not a count of objects actually served by the fallback: the census
neither checks for a missing `.g` file nor decodes the payload. Zero is logged at Info. A query
error is logged distinctly from a genuine zero count and never blocks startup.

**Flag-off cleanup limitation**: cleanup walks `.g` files, so it never visits live fallback-only
records that have metadata and a payload but no file. Those objects remain readable through the
fallback, but are not reclaimed by that cleanup walk. Orphaned payloads (without metadata) are
inert — never served or resurrected — and also remain until the flag is re-enabled and migration
reconcile sweeps them (§8.2 step 2b, `orphan_payload`, above).

## Rollback: `cpexport`, then downgrade (§8.4)

The read-time fallback above only helps the same-binary flag-off case. An **older storage
binary** — a real image downgrade — opens the migrated database without error, but it reads only
the metadata row and the `.g` file: every key the ObjectStore created since the flip has **no
file**, every key it updated has a **stale** one, and the old binary's `get()` **deletes the
metadata row** of a key whose file is missing (its self-repair) — the object is destroyed, not
merely invisible. Its `INSERT OR REPLACE` also nulls `rv`/`uid`; the every-start reconcile repairs
that on re-enable (`legacy_rewrite`), but nothing repairs a deleted row.
`TestExport_DangerWithoutExport_OldBinaryDestroysNewStoreRows` pins all three symptoms.

So the rollback order is fixed: **export first, downgrade second.** `/usr/bin/cpexport`
(`cmd/cpexport`; `file.ExportContainerProfiles`) writes every migrated row's payload back as the
legacy gob file at its key, staged and renamed exactly as the legacy writer does, at the row's
`resourceVersion` and UID. Rows with `rv IS NULL` (never migrated, or already rewritten by a legacy
writer) are skipped — their file is already the legacy writer's. The database is not touched: the
old binary ignores `rv`, `uid` and `payloads`; a row it never rewrites stays consistent for the
re-enable, and a row it rewrites or deletes is exactly the `legacy_rewrite` / `orphan_payload`
shape the reconcile repairs. Run it **with the server stopped** (scale to 0, run against the PVC),
never beside a serving process; `-dry-run` counts without writing.

```
cpexport [-root /data] [-db /data/metadata.sq3] [-dry-run]
```

The round trip is behaviourally identical, not byte-identical
(`TestExport_RoundTripIsBehaviourallyIdentical`): gob encodes maps (labels, annotations) in Go's
randomised map order, so two encodes of one object differ byte-wise, and the JSON codec keeps
`creationTimestamp` to the second and empty collections as empty (divergences 4 and 6 above).
Everything the old binary reads back — every field, the `resourceVersion`, the UID — is asserted
equal to what it served before the migration, and the full cycle export → downgrade → old-binary
writes and deletes → re-enable is asserted lossless (`TestExport_ThenDowngradeThenReEnable`).

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
