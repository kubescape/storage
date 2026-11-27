# Qualified-resource prefix migration (#376)

Storage now uses plural persistent resource names, matching its unchanged API
routes. Discovery advertises the corresponding singular names:

| Previous kind | Canonical kind |
| --- | --- |
| containerprofile | containerprofiles |
| sbomsyft | sbomsyfts |
| sbomsyftfiltered | sbomsyftfiltereds |
| seccompprofile | seccompprofiles |

Every startup runs `file.MigrateQualifiedResourcePrefixes` before the optional
ContainerProfile SQLite-native migration, cleanup, or API serving. This is
independent of backend and custom-REST flags. Both REST implementations use the
new names. No CRD schema or serialized object is changed.

## Upgrade and recovery

Use a single replica and a Recreate upgrade: **all other readers and writers must
be stopped**, including tools and older images. Back up the entire PVC and a
consistent SQLite database (including its WAL); retain the old image. Rehearse
against a sanitized copy of a real PVC before rollout. Generated test fixtures
are not evidence of that operational rehearsal.

An optional offline preflight with the new image is:

```sh
resourceprefixmigration -direction=forward -root=/data -db=/data/metadata.sq3 -dry-run
```

The same command without `-dry-run` migrates offline; otherwise startup performs
the migration automatically. A missing database is refused by the command.
Dry-run copies the stopped database and present WAL/SHM/journal sidecars to a
private temporary directory and opens only that copy. Source entries and bytes
remain unchanged (access times are not covered). Apply preserves journal mode
and schema version; it creates only the prefix recovery table if absent. Both
modes require the real `<root>/metadata.sq3` pairing and reject symlinked paths.
Versions 1–7 of the ordered schema are supported; malformed or unknown schemas
are refused. Do not use this command while writers are running.

The server's `containerProfileMigrationDryRun` has a narrower contract: normal
pool initialization can apply schema migrations. It performs no prefix migration
and refuses to serve when a prefix move is required. Use the offline command
for strict source-preserving inspection.

Global preflight inspects all four directories and the `metadata`, `payloads`,
and `time_series` tables before moving any resource. Populated legacy and
canonical forms, including opposing directions across tables or files, abort
startup. The diagnostic identifies the resource paths and conflicting state.
Do not delete data to bypass a conflict: inspect the backup and resolve which
store is authoritative offline. There is no automatic merge or overwrite.

For each resource, a durable `migration_state` intent precedes an atomic
same-filesystem directory rename. One immediate SQLite transaction updates all
applicable `kind` columns and the completion marker. WAL requires synchronous
FULL or EXTRA; DELETE journal requires EXTRA; other modes are refused. The
offline command sets EXTRA on its own connection, preserving journal mode.
Startup checks the actual gate/borrowed connection without changing application
settings. Validation precedes even empty-directory removal; the renamed directory's
parent is synced. The entire directory moves, including `.g`, `.m`, staging
files, and nested Kubernetes/ECS/host paths. Empty destinations may be removed;
populated destinations and symlinks are refused. Object bytes, metadata JSON,
UIDs, resource versions, and rowids are not rewritten.

If interrupted, keep storage stopped and rerun **the same direction**. Recognized
pending states resume, including when earlier resources have finished and a
later resource has only renamed its directory. Each versioned intent records
source row counts and directory presence; contradictory states fail closed.
Startup refuses pending rollback with the marker and direction in its diagnostic;
finish the offline rollback before choosing which binary to start. SQLite commit
errors are not interpreted as proof that nothing committed: reruns inspect the
authoritative marker and layout. A completed run
is harmless to repeat. If all objects and directories have been deleted since
the previous migration, reversing direction still atomically updates the completion
marker so subsequent old-binary writes can be migrated on the next upgrade.
Do not serve data between per-resource moves.

## Offline downgrade

1. Stop/scale storage to zero and take a fresh consistent PVC/database backup.
2. If downgrading from the SQLite-native ContainerProfile backend to a file-only
   binary, run the **new image's** `cpexport -root=/data -dry-run` and then
   `cpexport -root=/data`. Do this **before** reversing prefixes: the new export
   tool reads canonical plural kinds. **Stop if either export command fails or
   reports undecodable objects.** Export also removes stale files of deleted
   native objects; skipping it can resurrect deleted objects. Its own dry-run
   can initialize schema and is not the strict inspection mode described above. See [backend rollback](containerprofile-sqlite-backend.md).
3. With the new image's tool, run:

   ```sh
   resourceprefixmigration -direction=rollback -root=/data -db=/data/metadata.sq3 -dry-run
   resourceprefixmigration -direction=rollback -root=/data -db=/data/metadata.sq3
   ```

4. Start the older image only after success. Starting the new image instead
   automatically migrates forward again. A same-image backend-flag change does
   not need prefix rollback.

Running either tool alongside a live server is unsupported; no cross-process
lock fences an older binary. Backup restoration remains the recovery path if
the PVC or database is damaged.

After deployment, check the four `singularName` fields in the group's discovery
document and singular-name GETs with kubectl, as well as ordinary plural GET/LIST.
Local tests cover generated legacy Gob and SQLite-native snapshots, both REST
implementations, interruption recovery, and forward/rollback/forward operation.
Real-cluster discovery checks and PVC rehearsal remain deployment gates.

## Local compatibility evidence and release gates

The pinned old-reader probe is compiled from pre-fix revision
`7c93d2142e50c438bafeb5e57e26ff50c8701081`, with the file backend. The probe source
is `pkg/registry/file/testdata/qualified_resource_old_reader/main.go`. Build it
inside a separate source archive of that revision with the matching dependencies,
then run (the current vendor tree matches that revision's module files):

```sh
baseline_dir=$(mktemp -d)
git archive 7c93d2142e50c438bafeb5e57e26ff50c8701081 | tar -x -C "$baseline_dir"
cp -a vendor "$baseline_dir/vendor"
mkdir -p "$baseline_dir/cmd/prefix-old-reader"
cp pkg/registry/file/testdata/qualified_resource_old_reader/main.go "$baseline_dir/cmd/prefix-old-reader/main.go"
(cd "$baseline_dir" && go build -mod=vendor -o "$baseline_dir/old-reader" ./cmd/prefix-old-reader)
PREFIX_OLD_READER="$baseline_dir/old-reader" go test -mod=vendor ./pkg/registry/file -run '^TestQualifiedResourcePinnedOldReaderDowngrade$' -count=1
```

This tests old creates, forward prefix/backend migration, native update/delete,
export, prefix rollback, and old-process reads. It checks Gob bytes directly so
the newer payload fallback cannot hide an export defect. It is a source-pinned
storage probe, not an old-image Kubernetes deployment.

Before release, rehearse the same sequence on a sanitized production PVC
containing all four resources, verify raw discovery and singular/plural kubectl
GETs against the deployed server, and build both images. Subprocess crash tests
exercise SQLite recovery but are not a hardware power-loss simulation. Do not
close the issue solely on generated fixtures or an inconclusive performance run.
