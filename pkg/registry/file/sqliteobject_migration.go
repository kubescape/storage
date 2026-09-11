package file

// Startup data migration of ContainerProfile rows into the ObjectStore schema
// (design: .omc/plans/full-acid-storage-architecture.md §8.2, with K-1 and
// K-2's corrected reconcile rules).
//
// It runs synchronously in main.go, after the pool and the write gate exist
// and before the cleanup goroutine and the API server (R2: a concurrent
// cleanup tick could delete a row after its payload was copied). Every
// batch is one gated BEGIN IMMEDIATE … COMMIT containing only SQL on bytes
// prepared before the ticket (INV-1); the gob decode of a legacy file, the
// external migration tool and the JSON encode all run in the prepare phase
// on a pool connection that is released before the ticket is taken.
//
// The reconcile (steps 2 and 2b) runs on EVERY start: an older binary opens
// the migrated database without error and its INSERT OR REPLACE nulls rv/uid
// (R3), and its delete leaves the payloads row (K-2). The done-flag gates
// only the file sweeps (steps 3-4). Idempotent: a repaired row no longer
// matches the predicate. Resumable: a crash rolls back the batch in flight
// and the next start picks it up from the same predicate.

import (
	"bufio"
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// Reconcile shapes: the storage_cp_migration_total{shape} label values.
const (
	// MigrationShapeMigrated: a legacy row (rv NULL, no payloads row) whose
	// file decoded and agrees with the row on resourceVersion.
	MigrationShapeMigrated = "migrated"
	// MigrationShapeDiverged: same, but the row and the file disagree on
	// resourceVersion — one of the PC-DIV shapes, met once; the object gets
	// max(rowRV, payloadRV)+1.
	MigrationShapeDiverged = "diverged"
	// MigrationShapeRowWithoutFile: a legacy row with no payload file — the
	// row is deleted (today's get() self-repair, done once).
	MigrationShapeRowWithoutFile = "row_without_file"
	// MigrationShapeFileWithoutRow: a payload file with no metadata row (the
	// file sweep) — imported at the file's resourceVersion and UID.
	MigrationShapeFileWithoutRow = "file_without_row"
	// MigrationShapeLegacyRewrite: rv NULL with a payloads row — a legacy
	// writer replaced the row after a rollback (K-1). source=file when the
	// legacy writer's .g file exists (the payloads body is the stale side);
	// source=payloads for a row-only legacy write.
	MigrationShapeLegacyRewrite = "legacy_rewrite"
	// MigrationShapeOrphanPayload: a payloads row with no metadata row — a
	// legacy delete after a rollback (K-2); deleted, or every Create of the
	// key fails on the UNIQUE constraint forever.
	MigrationShapeOrphanPayload = "orphan_payload"
	// MigrationShapeUndecodable: a payload that neither gob nor the external
	// tool could decode — skipped and counted, never deleted (PM-2).
	MigrationShapeUndecodable = "undecodable"
	// MigrationShapeTempFile: a *.g.t* staging file a legacy writer never
	// committed — removed.
	MigrationShapeTempFile = "temp_file"

	MigrationSourceFile     = "file"
	MigrationSourcePayloads = "payloads"

	migrationStateName = "containerprofile"
	migrationStateDone = "done"

	// DefaultMigrationBatchSize is the number of objects per gated
	// transaction (§8.2: B ≈ 50).
	DefaultMigrationBatchSize = 50
)

// ContainerProfileMigrationOptions tunes MigrateContainerProfiles.
type ContainerProfileMigrationOptions struct {
	// BatchSize is the number of objects per transaction; non-positive =
	// DefaultMigrationBatchSize.
	BatchSize int
	// DryRun reconciles and counts without writing anything (§8.3): no
	// gate is needed, no row, payload or file is touched, the done-flag is
	// not set.
	DryRun bool

	hooks migrationHooks
}

// migrationHooks are test seams; nil in production.
type migrationHooks struct {
	// beforeStatement runs inside the gated transaction of batch n before
	// its statement idx (name labels it). Crash-injection tests return an
	// error, panic, or exit the process from it.
	beforeStatement func(batch, idx int, name string) error
}

// ContainerProfileMigrationReport is what one run did.
type ContainerProfileMigrationReport struct {
	// Counts is keyed by shape, or "shape/source" for legacy_rewrite.
	Counts map[string]int
	// Batches is the number of transactions committed (prepared, in dry-run).
	Batches int
	// SweepsRun reports whether the file sweeps (steps 3-4) ran this start.
	SweepsRun bool
	DryRun    bool
	Elapsed   time.Duration
}

func (r *ContainerProfileMigrationReport) add(shape, source string) {
	key := shape
	if source != "" {
		key = shape + "/" + source
	}
	r.Counts[key]++
	if !r.DryRun {
		metrics.IncCPMigration(shape, source)
	}
}

// Count returns the total for shape across sources.
func (r *ContainerProfileMigrationReport) Count(shape string) int {
	n := r.Counts[shape]
	for k, v := range r.Counts {
		if strings.HasPrefix(k, shape+"/") {
			n += v
		}
	}
	return n
}

// Work reports whether the run reconciled anything.
func (r *ContainerProfileMigrationReport) Work() int {
	n := 0
	for _, v := range r.Counts {
		n += v
	}
	return n
}

func (r *ContainerProfileMigrationReport) logFields() []helpers.IDetails {
	fields := []helpers.IDetails{
		helpers.Int("batches", r.Batches),
		helpers.String("elapsed", r.Elapsed.String()),
		helpers.Interface("sweepsRun", r.SweepsRun),
		helpers.Interface("dryRun", r.DryRun),
	}
	for k, v := range r.Counts {
		fields = append(fields, helpers.Int(k, v))
	}
	return fields
}

type containerProfileMigrator struct {
	pool   *sqlitemigration.Pool
	gate   *writeGate
	fs     afero.Fs
	root   string
	scheme *runtime.Scheme
	opts   ContainerProfileMigrationOptions
	report *ContainerProfileMigrationReport
}

// migrationAction is one prepared write of a batch: SQL on bytes only.
type migrationAction struct {
	name string
	exec func(conn *sqlite.Conn) error
}

// MigrateContainerProfiles reconciles every ContainerProfile row, payload
// and legacy file into the ObjectStore schema (see the file comment). gate
// may be nil only with opts.DryRun. fs/root are the legacy payload
// filesystem and its root (DefaultStorageRoot in production).
func MigrateContainerProfiles(ctx context.Context, pool *sqlitemigration.Pool, gate *WriteGate, fs afero.Fs, root string, scheme *runtime.Scheme, opts ContainerProfileMigrationOptions) (*ContainerProfileMigrationReport, error) {
	if gate == nil && !opts.DryRun {
		return nil, errors.New("containerprofile migration: a write gate is required (AC-G1: the migration's batches are gated writes)")
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultMigrationBatchSize
	}
	start := time.Now()
	m := &containerProfileMigrator{
		pool: pool, gate: gate, fs: fs, root: root, scheme: scheme, opts: opts,
		report: &ContainerProfileMigrationReport{Counts: map[string]int{}, DryRun: opts.DryRun},
	}
	if err := m.reconcileRows(ctx); err != nil {
		return m.report, err
	}
	if err := m.reconcileOrphanPayloads(ctx); err != nil {
		return m.report, err
	}
	done, err := m.readDone(ctx)
	if err != nil {
		return m.report, err
	}
	if !done {
		m.report.SweepsRun = true
		undecodable, err := m.sweepFiles(ctx)
		if err != nil {
			return m.report, err
		}
		// An undecodable file is left in place and counted; the sweep runs
		// again next start so it stays visible until an operator acts.
		if !opts.DryRun && undecodable == 0 {
			if err := m.markDone(ctx); err != nil {
				return m.report, err
			}
		}
	}
	if !opts.DryRun {
		if err := m.checkpoint(ctx); err != nil {
			return m.report, err
		}
	}
	m.report.Elapsed = time.Since(start)
	logger.L().Debug("containerprofile migration: done", m.report.logFields()...)
	return m.report, nil
}

// ---- step 2: rows with rv NULL or no payloads row ----

type migrationCandidate struct {
	rowid        int64
	namespace    string
	name         string
	metadataJSON []byte
	rvNull       bool
	hasPayload   bool
	encoding     string
	body         []byte
}

func (m *containerProfileMigrator) readCandidates(ctx context.Context, cursor int64) ([]migrationCandidate, error) {
	conn, err := m.pool.Take(ctx)
	if err != nil {
		return nil, fmt.Errorf("containerprofile migration: take connection: %w", err)
	}
	defer m.pool.Put(conn)
	var out []migrationCandidate
	err = sqlitex.Execute(conn,
		`SELECT m.rowid, m.namespace, m.name, m.metadata, m.rv IS NULL, p.encoding IS NOT NULL, p.encoding, p.body
			FROM metadata m LEFT JOIN payloads p USING (kind, namespace, name)
			WHERE m.kind = :kind AND m.rowid > :cursor AND (m.rv IS NULL OR p.encoding IS NULL)
			ORDER BY m.rowid LIMIT :limit`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": ContainerProfileKind, ":cursor": cursor, ":limit": m.opts.BatchSize},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				c := migrationCandidate{
					rowid:        stmt.ColumnInt64(0),
					namespace:    stmt.ColumnText(1),
					name:         stmt.ColumnText(2),
					metadataJSON: []byte(stmt.ColumnText(3)),
					rvNull:       stmt.ColumnInt64(4) == 1,
					hasPayload:   stmt.ColumnInt64(5) == 1,
					encoding:     stmt.ColumnText(6),
				}
				if c.hasPayload {
					c.body = make([]byte, stmt.ColumnLen(7))
					stmt.ColumnBytes(7, c.body)
				}
				out = append(out, c)
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("containerprofile migration: read candidates: %w", err)
	}
	return out, nil
}

func (m *containerProfileMigrator) reconcileRows(ctx context.Context) error {
	cursor := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cands, err := m.readCandidates(ctx, cursor)
		if err != nil {
			return err
		}
		if len(cands) == 0 {
			return nil
		}
		cursor = cands[len(cands)-1].rowid
		var actions []migrationAction
		for i := range cands {
			actions = append(actions, m.prepareCandidate(ctx, &cands[i])...)
		}
		if err := m.commit(ctx, actions); err != nil {
			return err
		}
	}
}

func (m *containerProfileMigrator) key(namespace, name string) string {
	return K8sKeysToPath("", softwarecomposition.GroupName, ContainerProfileKind, "", namespace, name)
}

func parseRV(s string) int64 {
	rv, err := strconv.ParseInt(s, 10, 64)
	if err != nil || rv < 0 {
		return 0
	}
	return rv
}

// prepareCandidate decides and prepares one row's reconcile; it returns the
// SQL to run under the gate (none when the row is skipped).
func (m *containerProfileMigrator) prepareCandidate(ctx context.Context, c *migrationCandidate) []migrationAction {
	key := m.key(c.namespace, c.name)
	row := &PartialObjectMetadata{}
	if err := json.Unmarshal(c.metadataJSON, row); err != nil {
		m.report.add(MigrationShapeUndecodable, "row")
		logger.L().Warning("containerprofile migration: metadata row is not JSON; skipped", helpers.Error(err), helpers.String("key", key))
		return nil
	}
	rowRV := parseRV(row.ResourceVersion)
	obj, found, err := m.decodeLegacyFile(ctx, key)
	if err != nil {
		m.report.add(MigrationShapeUndecodable, MigrationSourceFile)
		logger.L().Warning("containerprofile migration: payload file undecodable; row left as is, file left for the export tool", helpers.Error(err), helpers.String("key", key))
		return nil
	}

	if !c.hasPayload {
		if !found {
			m.report.add(MigrationShapeRowWithoutFile, "")
			logger.L().Warning("containerprofile migration: row without payload file; row deleted", helpers.String("key", key))
			return []migrationAction{m.deleteRowAction(key)}
		}
		payloadRV := parseRV(obj.ResourceVersion)
		rv := rowRV
		shape := MigrationShapeMigrated
		switch {
		case rowRV != payloadRV:
			rv = max(rowRV, payloadRV) + 1
			shape = MigrationShapeDiverged
		case rv == 0:
			rv = 1
		}
		// PreSave's non-TS revert (processor.go: a Completed base never
		// regresses to Learning): the row's Completed beats the file's Learning.
		if obj.Annotations[helpersv1.ReportSeriesIdMetadataKey] == "" &&
			row.Annotations[helpersv1.StatusMetadataKey] == helpersv1.Completed &&
			obj.Annotations[helpersv1.StatusMetadataKey] == helpersv1.Learning {
			obj.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Completed
		}
		uid := obj.UID
		if uid == "" {
			uid = row.UID
		}
		m.report.add(shape, "")
		if shape != MigrationShapeMigrated {
			logger.L().Warning("containerprofile migration: row and payload file disagree on resourceVersion; reconciled at max+1",
				helpers.String("key", key), helpers.Interface("rowRV", rowRV), helpers.Interface("payloadRV", payloadRV), helpers.Interface("rv", rv))
		}
		return m.rewriteActions(key, obj, rv, uid, true)
	}

	// rv NULL with a payloads row: K-1's legacy_rewrite.
	source := MigrationSourcePayloads
	if found {
		source = MigrationSourceFile
	} else {
		obj = &softwarecomposition.ContainerProfile{}
		if err := decodePayloadBody(m.scheme, c.encoding, c.body, obj); err != nil {
			m.report.add(MigrationShapeUndecodable, MigrationSourcePayloads)
			logger.L().Warning("containerprofile migration: payloads body undecodable; row left as is", helpers.Error(err), helpers.String("key", key))
			return nil
		}
	}
	// rv := the row JSON's resourceVersion, no +1 (the legacy writer already
	// bumped it). When a crash left the legacy writer's file one version
	// ahead of its row, or a stale pre-migration file sits behind a newer
	// payloads body, the largest persisted version wins so no client sees a
	// version regress; the row JSON is rewritten to match (INV-2).
	rv := max(rowRV, parseRV(obj.ResourceVersion))
	if found {
		bodyObj := &softwarecomposition.ContainerProfile{}
		if err := decodePayloadBody(m.scheme, c.encoding, c.body, bodyObj); err == nil {
			rv = max(rv, parseRV(bodyObj.ResourceVersion))
		}
	}
	if rv == 0 {
		rv = 1
	}
	uid := row.UID
	if uid == "" {
		uid = obj.UID
	}
	m.report.add(MigrationShapeLegacyRewrite, source)
	logger.L().Warning("containerprofile migration: legacy writer replaced a migrated row (rv NULL with a payloads row); repaired",
		helpers.String("key", key), helpers.String("source", source), helpers.Interface("rv", rv))
	return m.rewriteActions(key, obj, rv, uid, false)
}

// rewriteActions stamps obj at (rv, uid), encodes the metadata JSON and the
// body, and returns the UPDATE of the metadata row plus the INSERT (fresh
// payload) or UPDATE (existing payload) of the payloads row.
func (m *containerProfileMigrator) rewriteActions(key string, obj *softwarecomposition.ContainerProfile, rv int64, uid types.UID, insertPayload bool) []migrationAction {
	if uid == "" {
		uid = uuid.NewUUID()
	}
	obj.ResourceVersion = strconv.FormatInt(rv, 10)
	obj.UID = uid
	metadataJSON, err := json.Marshal(extractFields(obj, []string{"ObjectMeta", "SchemaVersion"}))
	if err != nil {
		m.report.add(MigrationShapeUndecodable, "encode")
		logger.L().Warning("containerprofile migration: marshal metadata failed; skipped", helpers.Error(err), helpers.String("key", key))
		return nil
	}
	body, err := encodePayloadBody(m.scheme, obj)
	if err != nil {
		m.report.add(MigrationShapeUndecodable, "encode")
		logger.L().Warning("containerprofile migration: encode payload failed; skipped", helpers.Error(err), helpers.String("key", key))
		return nil
	}
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	actions := []migrationAction{{
		name: "update-metadata",
		exec: func(conn *sqlite.Conn) error {
			if err := sqlitex.Execute(conn,
				`UPDATE metadata SET metadata = ?, rv = ?, uid = ? WHERE kind = ? AND namespace = ? AND name = ?`,
				&sqlitex.ExecOptions{Args: []any{string(metadataJSON), rv, string(uid), kind, namespace, name}}); err != nil {
				return fmt.Errorf("update metadata %s: %w", key, err)
			}
			if n := conn.Changes(); n != 1 {
				return fmt.Errorf("update metadata %s: %d rows", key, n)
			}
			return nil
		},
	}}
	if insertPayload {
		actions = append(actions, migrationAction{
			name: "insert-payload",
			exec: func(conn *sqlite.Conn) error {
				return execInsertPayload(conn, key, kind, namespace, name, body)
			},
		})
	} else {
		actions = append(actions, migrationAction{
			name: "update-payload",
			exec: func(conn *sqlite.Conn) error {
				if err := sqlitex.Execute(conn,
					`UPDATE payloads SET encoding = ?, body = ? WHERE kind = ? AND namespace = ? AND name = ?`,
					&sqlitex.ExecOptions{Args: []any{PayloadEncodingJSONV1Beta1, body, kind, namespace, name}}); err != nil {
					return fmt.Errorf("update payload %s: %w", key, err)
				}
				if n := conn.Changes(); n != 1 {
					return fmt.Errorf("update payload %s: %d rows", key, n)
				}
				return nil
			},
		})
	}
	return actions
}

func execInsertPayload(conn *sqlite.Conn, key, kind, namespace, name string, body []byte) error {
	if err := sqlitex.Execute(conn,
		`INSERT INTO payloads (kind, namespace, name, encoding, body) VALUES (?, ?, ?, ?, ?)`,
		&sqlitex.ExecOptions{Args: []any{kind, namespace, name, PayloadEncodingJSONV1Beta1, body}}); err != nil {
		return fmt.Errorf("insert payload %s: %w", key, err)
	}
	return nil
}

func (m *containerProfileMigrator) deleteRowAction(key string) migrationAction {
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	return migrationAction{
		name: "delete-metadata",
		exec: func(conn *sqlite.Conn) error {
			if err := sqlitex.Execute(conn,
				`DELETE FROM metadata WHERE kind = ? AND namespace = ? AND name = ?`,
				&sqlitex.ExecOptions{Args: []any{kind, namespace, name}}); err != nil {
				return fmt.Errorf("delete metadata %s: %w", key, err)
			}
			return nil
		},
	}
}

// decodeLegacyFile decodes the legacy gob payload of key. found is false
// when there is no file; err is set when the file exists but neither gob
// nor the external migration tool could decode it.
func (m *containerProfileMigrator) decodeLegacyFile(ctx context.Context, key string) (*softwarecomposition.ContainerProfile, bool, error) {
	return decodeLegacyFileAt(ctx, m.fs, m.root, key)
}

// decodeLegacyFileAt is decodeLegacyFile without a migrator receiver, for
// callers (the export tool's stale-file reconciliation) that need the same
// "would an old binary read this as a valid object" decode, outside of a
// migration run.
func decodeLegacyFileAt(ctx context.Context, fs afero.Fs, root, key string) (*softwarecomposition.ContainerProfile, bool, error) {
	p := filepath.Join(root, key)
	f, err := fs.Open(makePayloadPath(p))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, afero.ErrFileNotFound) {
			return nil, false, nil
		}
		return nil, true, fmt.Errorf("open payload file: %w", err)
	}
	defer func() { _ = f.Close() }()
	obj := &softwarecomposition.ContainerProfile{}
	err = gob.NewDecoder(bufio.NewReader(f)).Decode(obj)
	if err == nil {
		return obj, true, nil
	}
	if strings.Contains(err.Error(), "gob: wrong type") || strings.Contains(err.Error(), "extra fields") {
		// The last time the external tool runs for this kind.
		out, terr := execMigrationTool(ctx, p, "ContainerProfile")
		if terr != nil {
			return nil, true, fmt.Errorf("gob decode: %v; migration tool: %w", err, terr)
		}
		obj = &softwarecomposition.ContainerProfile{}
		if jerr := json.Unmarshal(out, obj); jerr != nil {
			return nil, true, fmt.Errorf("gob decode: %v; migration tool output: %w", err, jerr)
		}
		return obj, true, nil
	}
	return nil, true, fmt.Errorf("gob decode: %w", err)
}

// ---- step 2b: payloads rows with no metadata row (K-2) ----

func (m *containerProfileMigrator) reconcileOrphanPayloads(ctx context.Context) error {
	cursor := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		conn, err := m.pool.Take(ctx)
		if err != nil {
			return fmt.Errorf("containerprofile migration: take connection: %w", err)
		}
		var keys []string
		err = sqlitex.Execute(conn,
			`SELECT p.rowid, p.namespace, p.name FROM payloads p
				WHERE p.kind = :kind AND p.rowid > :cursor AND NOT EXISTS (
					SELECT 1 FROM metadata m WHERE m.kind = p.kind AND m.namespace = p.namespace AND m.name = p.name)
				ORDER BY p.rowid LIMIT :limit`,
			&sqlitex.ExecOptions{
				Named: map[string]any{":kind": ContainerProfileKind, ":cursor": cursor, ":limit": m.opts.BatchSize},
				ResultFunc: func(stmt *sqlite.Stmt) error {
					cursor = stmt.ColumnInt64(0)
					keys = append(keys, m.key(stmt.ColumnText(1), stmt.ColumnText(2)))
					return nil
				},
			})
		m.pool.Put(conn)
		if err != nil {
			return fmt.Errorf("containerprofile migration: read orphan payloads: %w", err)
		}
		if len(keys) == 0 {
			return nil
		}
		var actions []migrationAction
		for _, key := range keys {
			m.report.add(MigrationShapeOrphanPayload, "")
			logger.L().Warning("containerprofile migration: payloads row without a metadata row (a legacy delete after a rollback); deleted", helpers.String("key", key))
			_, _, kind, _, namespace, name := K8sPathToKeys(key)
			actions = append(actions, migrationAction{
				name: "delete-payload",
				exec: func(conn *sqlite.Conn) error {
					if err := sqlitex.Execute(conn,
						`DELETE FROM payloads WHERE kind = ? AND namespace = ? AND name = ?`,
						&sqlitex.ExecOptions{Args: []any{kind, namespace, name}}); err != nil {
						return fmt.Errorf("delete payload %s: %w", key, err)
					}
					return nil
				},
			})
		}
		if err := m.commit(ctx, actions); err != nil {
			return err
		}
	}
}

// ---- steps 3-4: the file sweeps, gated by the done-flag ----

func (m *containerProfileMigrator) readDone(ctx context.Context) (bool, error) {
	conn, err := m.pool.Take(ctx)
	if err != nil {
		return false, fmt.Errorf("containerprofile migration: take connection: %w", err)
	}
	defer m.pool.Put(conn)
	var state string
	err = sqlitex.Execute(conn, `SELECT state FROM migration_state WHERE name = ?`, &sqlitex.ExecOptions{
		Args:       []any{migrationStateName},
		ResultFunc: func(stmt *sqlite.Stmt) error { state = stmt.ColumnText(0); return nil },
	})
	if err != nil {
		return false, fmt.Errorf("containerprofile migration: read state: %w", err)
	}
	return state == migrationStateDone, nil
}

func (m *containerProfileMigrator) markDone(ctx context.Context) error {
	counts, _ := json.Marshal(m.report.Counts)
	return m.commit(ctx, []migrationAction{{
		name: "mark-done",
		exec: func(conn *sqlite.Conn) error {
			return sqlitex.Execute(conn,
				`INSERT OR REPLACE INTO migration_state (name, state, counts, updated_at) VALUES (?, ?, ?, ?)`,
				&sqlitex.ExecOptions{Args: []any{migrationStateName, migrationStateDone, string(counts), time.Now().UTC().Format(time.RFC3339)}})
		},
	}})
}

// sweepFiles imports every payload file with no metadata row and removes
// every staging file. It returns the number of undecodable files met.
func (m *containerProfileMigrator) sweepFiles(ctx context.Context) (int, error) {
	dir := filepath.Join(m.root, softwarecomposition.GroupName, ContainerProfileKind)
	// A real filesystem error here (e.g. permission denied) must not read as
	// "the directory doesn't exist": DirExists returns exists=false on ANY
	// stat error, not just os.ErrNotExist, and the caller treats a nil error
	// here as "sweep found nothing, mark done" -- an unreadable directory
	// would then permanently persist the done marker over an incomplete
	// sweep, with nothing left to trigger a retry on the next start.
	exists, err := afero.DirExists(m.fs, dir)
	if err != nil {
		return 0, fmt.Errorf("containerprofile migration: stat %s: %w", dir, err)
	}
	if !exists {
		return 0, nil
	}
	conn, err := m.pool.Take(ctx)
	if err != nil {
		return 0, fmt.Errorf("containerprofile migration: take connection: %w", err)
	}
	var orphans []string
	walkErr := afero.Walk(m.fs, dir, func(path string, info os.FileInfo, err error) error {
		// A per-entry error (e.g. an unreadable subtree) must abort the walk,
		// not be treated as "nothing interesting here": returning nil let the
		// sweep silently skip whatever orphan payload files sat under that
		// entry and still report success, persisting the done marker over an
		// incomplete sweep (see the DirExists check above for the same class
		// of bug at the top-level directory).
		if err != nil {
			return fmt.Errorf("containerprofile migration: walk %s: %w", path, err)
		}
		if info.IsDir() {
			return nil
		}
		if !IsPayloadFile(path) {
			// The legacy staging names are <key>.g.t (saveObject) and
			// <key>.g.t.<unixnanos>.<seq> (the single writer); neither ends in
			// .g, whereas a payload of a workload named e.g. "app.g.tail" does
			// — the payload test above must come first (a bare ".g.t"
			// substring match would delete that payload as a staging file).
			if isLegacyStagingFile(info.Name()) {
				m.report.add(MigrationShapeTempFile, "")
				if !m.opts.DryRun {
					if rerr := m.fs.Remove(path); rerr != nil {
						logger.L().Warning("containerprofile migration: remove staging file failed", helpers.Error(rerr), helpers.String("path", path))
					}
				}
			}
			return nil
		}
		key := path[len(m.root) : len(path)-len(GobExt)]
		if _, rerr := ReadMetadata(conn, key); rerr == nil {
			return nil
		} else if !errors.Is(rerr, ErrMetadataNotFound) {
			return fmt.Errorf("read metadata %s: %w", key, rerr)
		}
		orphans = append(orphans, key)
		return nil
	})
	m.pool.Put(conn)
	if walkErr != nil {
		return 0, fmt.Errorf("containerprofile migration: sweep %s: %w", dir, walkErr)
	}

	undecodable := 0
	for start := 0; start < len(orphans); start += m.opts.BatchSize {
		if err := ctx.Err(); err != nil {
			return undecodable, err
		}
		end := min(start+m.opts.BatchSize, len(orphans))
		var actions []migrationAction
		for _, key := range orphans[start:end] {
			obj, found, err := m.decodeLegacyFile(ctx, key)
			if err != nil || !found {
				undecodable++
				m.report.add(MigrationShapeUndecodable, MigrationSourceFile)
				logger.L().Warning("containerprofile migration: payload file without a row is undecodable; left for the export tool", helpers.Error(err), helpers.String("key", key))
				continue
			}
			m.report.add(MigrationShapeFileWithoutRow, "")
			logger.L().Warning("containerprofile migration: payload file without a metadata row; imported", helpers.String("key", key))
			actions = append(actions, m.importActions(key, obj)...)
		}
		if err := m.commit(ctx, actions); err != nil {
			return undecodable, err
		}
	}
	return undecodable, nil
}

// isLegacyStagingFile reports whether name is a legacy writer's staging
// file: <key>.g.t (saveObject) or <key>.g.t.<unixnanos>.<seq> (the single
// writer). Callers exclude payload files (*.g) first.
func isLegacyStagingFile(name string) bool {
	return strings.HasSuffix(name, GobExt+".t") || strings.Contains(name, GobExt+".t.")
}

// importActions creates the metadata and payloads rows of a file with no row,
// at the file's resourceVersion and UID.
func (m *containerProfileMigrator) importActions(key string, obj *softwarecomposition.ContainerProfile) []migrationAction {
	rv := max(parseRV(obj.ResourceVersion), 1)
	uid := obj.UID
	if uid == "" {
		uid = uuid.NewUUID()
	}
	obj.ResourceVersion = strconv.FormatInt(rv, 10)
	obj.UID = uid
	metadataJSON, err := json.Marshal(extractFields(obj, []string{"ObjectMeta", "SchemaVersion"}))
	if err != nil {
		logger.L().Warning("containerprofile migration: marshal metadata failed; skipped", helpers.Error(err), helpers.String("key", key))
		return nil
	}
	body, err := encodePayloadBody(m.scheme, obj)
	if err != nil {
		logger.L().Warning("containerprofile migration: encode payload failed; skipped", helpers.Error(err), helpers.String("key", key))
		return nil
	}
	_, _, kind, _, namespace, name := K8sPathToKeys(key)
	return []migrationAction{
		{
			name: "insert-metadata",
			exec: func(conn *sqlite.Conn) error {
				if err := sqlitex.Execute(conn,
					`INSERT INTO metadata (kind, namespace, name, metadata, rv, uid) VALUES (?, ?, ?, ?, ?, ?)`,
					&sqlitex.ExecOptions{Args: []any{kind, namespace, name, string(metadataJSON), rv, string(uid)}}); err != nil {
					return fmt.Errorf("insert metadata %s: %w", key, err)
				}
				return nil
			},
		},
		{
			name: "insert-payload",
			exec: func(conn *sqlite.Conn) error {
				return execInsertPayload(conn, key, kind, namespace, name, body)
			},
		},
	}
}

// ---- the gated batch ----

// commit runs actions in one gated transaction (nothing in dry-run).
func (m *containerProfileMigrator) commit(ctx context.Context, actions []migrationAction) error {
	if len(actions) == 0 {
		return nil
	}
	m.report.Batches++
	if m.opts.DryRun {
		return nil
	}
	batch := m.report.Batches
	err := m.gate.run(ctx, priorityLow, holdPathCPMigration, ContainerProfileKindPlural, func(_ context.Context, conn *sqlite.Conn) error {
		for i, a := range actions {
			if m.opts.hooks.beforeStatement != nil {
				if err := m.opts.hooks.beforeStatement(batch, i, a.name); err != nil {
					return err
				}
			}
			if err := a.exec(conn); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("containerprofile migration: batch %d rolled back: %w", batch, err)
	}
	return nil
}

// checkpoint runs one PASSIVE checkpoint so the server does not start with
// the migration's whole WAL (wal_autocheckpoint is 0 on every connection).
func (m *containerProfileMigrator) checkpoint(ctx context.Context) error {
	conn, err := m.pool.Take(ctx)
	if err != nil {
		return fmt.Errorf("containerprofile migration: take connection: %w", err)
	}
	defer m.pool.Put(conn)
	if err := sqlitex.ExecuteTransient(conn, `PRAGMA wal_checkpoint(PASSIVE)`, nil); err != nil {
		return fmt.Errorf("containerprofile migration: checkpoint: %w", err)
	}
	return nil
}
