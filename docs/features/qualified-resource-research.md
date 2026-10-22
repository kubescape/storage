# Issue #376: source audit and migration decisions

Research date: 2026-10-21. This records the implementation requirements derived
from the pre-fix checkout and its migration draft, not a claim that deployment or
production-data rehearsal has passed.

## Storage and discovery contract

Kubernetes defines `DefaultQualifiedResource` as plural, obtains REST options
from it, and exposes `SingularQualifiedResource.Resource` through
`GetSingularName()`. The simple storage factory constructs the persistent prefix
as group/resource. Consequently, swapping the four factories changes storage
addresses as well as discovery. Sources: [vendored registry store](../../vendor/k8s.io/apiserver/pkg/registry/generic/registry/store.go)
(`DefaultQualifiedResource`, `CompleteWithOptions`, `GetSingularName`),
[storage factory](../../vendor/k8s.io/apiserver/pkg/server/options/etcd.go)
(`SimpleStorageFactory.ResourcePrefix`), and
[discovery installer](../../vendor/k8s.io/apiserver/pkg/endpoints/installer.go)
(`SingularNameProvider`).

The current database stores the resource component in `kind`; it does not have
the full-path `key` column described in the original issue. `metadata`,
`payloads`, and `time_series` all include that component in their primary keys.
All existing tables must move together without rewriting object bytes or
metadata. Source: [SQLite schema and key helpers](../../pkg/registry/file/sqlite.go)
(`SchemaMigrations`, `K8sKeysToPath`, `ECSKeysToPath`, `HostKeysToPath`).

## Internal consumers that must follow the renamed prefixes

| Consumer | Required behavior and primary source |
| --- | --- |
| Both REST implementations | Assign plural default and singular discovery names in the four resource factories: [containerprofile](../../pkg/registry/softwarecomposition/containerprofile), [sbomsyfts](../../pkg/registry/softwarecomposition/sbomsyfts), [sbomsyftfiltereds](../../pkg/registry/softwarecomposition/sbomsyftfiltereds), [seccompprofiles](../../pkg/registry/softwarecomposition/seccompprofiles). |
| CP persistence and normalization | Canonical persistent CP kind must be plural; retain any intentional legacy input alias without directing writes back to the singular store. Source: [containerprofile_storage.go](../../pkg/registry/file/containerprofile_storage.go), `ContainerProfileKind`, `IsContainerProfileKind`, `NormalizeContainerProfileKind`. |
| CP processing | Both existing-profile lookups and the SBOM lookup in `PreSave` must use canonical keys; otherwise completed-profile protection and SBOM-informed processing can silently miss existing data. Source: [containerprofile_processor.go](../../pkg/registry/file/containerprofile_processor.go), `PreSave`, `cleanup`. |
| Cleanup and host handling | Update handler/object registries and the host-SBOM predicate together, preserving the existing host retention rules. Sources: [cleanup.go](../../pkg/registry/file/cleanup.go), [cleanup_host.go](../../pkg/registry/file/cleanup_host.go). |
| Generated network policies, CP migration/export | These use the CP kind constant and inherit its change; update obsolete singular-prefix comments. Sources: [generatednetworkpolicy.go](../../pkg/registry/file/generatednetworkpolicy.go), [sqliteobject_migration.go](../../pkg/registry/file/sqliteobject_migration.go), [sqliteobject_export.go](../../pkg/registry/file/sqliteobject_export.go). |

Keep the existing CP data migration's `migrationStateName = "containerprofile"`
unchanged: it identifies migration history, not a stored resource. Renaming it
would cause the once-only sweeps to be treated as new work. Source:
[sqliteobject_migration.go](../../pkg/registry/file/sqliteobject_migration.go),
`migrationStateName`, `readDone`, `markDone`.

## Migration and recovery requirements

The draft's durable per-resource intent, whole-directory rename, parent sync,
and atomic SQLite updates plus completion marker are the appropriate recovery
boundaries. Preflight every resource before applying any move; refuse mixed
old/new data and unknown pending states. Keep all consumers stopped until every
resource succeeds. Source: [prefix migration](../../pkg/registry/file/qualified_resource_migration.go),
`MigrateQualifiedResourcePrefixes`, `prefixMove.inspect`, `prefixMove.apply`.

Use actual connection settings to enforce WAL with synchronous FULL or DELETE
with EXTRA before any mutation, including empty-directory removal. SQLite
documents that FULL provides WAL commit durability, while EXTRA additionally
syncs the directory after deleting a rollback journal. Reject modes outside the
supported protocol. Source: [SQLite synchronous documentation](https://www.sqlite.org/pragma.html#pragma_synchronous).

The offline command must use explicit open flags: this driver's default pool
flags enable create and WAL. Preserve existing journal mode and schema version;
recognize supported legacy schema prefixes and create only the missing migration
state table. The normal schema migration uses `CREATE TABLE IF NOT EXISTS` for
that table, so its later scheduled creation remains compatible. Sources:
[pool defaults](../../vendor/zombiezen.com/go/sqlite/sqlitex/pool.go),
[schema migrations](../../pkg/registry/file/sqlite.go), and
[migration version handling](../../vendor/zombiezen.com/go/sqlite/sqlitemigration/sqlitemigration.go).

A strict source-preserving dry-run must copy the stopped database with its
present journal/WAL/shared-memory sidecars and open only the temporary copy.
SQLite can recover on access, and WAL read-only access can require creating
sidecars; opening the original read-only is insufficient for the stronger
no-source-change guarantee. Copying an active database or mismatching it with
its recovery journal is unsafe. Sources: [SQLite WAL behavior](https://www.sqlite.org/wal.html),
[SQLite backup and journal hazards](https://www.sqlite.org/howtocorrupt.html).

Startup must run prefix migration before CP data migration, advisory census,
cleanup, and API serving. Ship the offline migration executable in the runtime
image so its rollback instructions are executable. Integration points:
[main.go](../../main.go), [runtime image](../../build/Dockerfile),
[debug image](../../build/Dockerfile.debug).

## Verification and rollback boundaries

Seed old native-CP rows directly for migration tests. Creating them through the
new ObjectStore can normalize their kind immediately and accidentally test an
already-migrated store. Verify legacy rows exist before migration, then reopen
and check GET/LIST, discovery, payload bytes, UID, resource version, and rowids.
Sources: [REST persistence test](../../pkg/registry/file/qualified_resource_rest_test.go),
[native store](../../pkg/registry/file/sqliteobject_store.go).

Exercise both directions and database-only recovery, conflicts across files and
all tables, rename/sync/SQL failures, old schemas, and restart after interruption.
Dry-run tests must compare the original directory inventory and DB/sidecar bytes.
The existing crash-hook tests provide a starting point, not evidence of a real
power failure. Source: [migration tests](../../pkg/registry/file/qualified_resource_migration_test.go).

Before downgrading native CP data to a file-only binary, export canonical data
and reconcile stale files before reversing prefixes. Prefix reversal alone can
leave a deleted object's old file readable by the older binary. Add a complete
deleted-object rollback test. Preserve unrelated directory-only legacy data;
do not turn prefix migration into a general orphan deletion sweep. Source:
[CP exporter](../../pkg/registry/file/sqliteobject_export.go),
`ExportContainerProfiles`, `reconcileStaleExportedFiles`.

Generated fixtures establish local behavior. A sanitized production-PVC rehearsal
and live kubectl discovery/GET/LIST checks remain operational acceptance steps;
the working tree contains no supplied production snapshot proving them. See the
[operator runbook](qualified-resource-migration.md).
