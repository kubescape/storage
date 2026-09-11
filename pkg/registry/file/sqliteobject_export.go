package file

// Reverse export of ObjectStore rows into the legacy on-disk representation
// (design §8.4: the rollback order is export-then-downgrade).
//
// An older storage binary opens a migrated database without error and
// reads only the metadata row and the .g file: a key the ObjectStore created
// or updated since the flip has no file (or a stale one), and the old
// binary's get() DELETES the metadata row of a key whose file is missing
// (its self-repair) — the object is destroyed, not merely invisible. Its
// INSERT OR REPLACE also nulls rv/uid, which the every-start reconcile
// repairs on re-enable (legacy_rewrite), but nothing repairs a deleted row.
// So before a downgrade, every migrated row's payload is written back as
// the gob file the old binary expects, at the row's resourceVersion and UID.
//
// The database is not touched: the old binary ignores rv, uid and the
// payloads table, and a row it never rewrites stays consistent with its
// payloads row for the re-enable; a row it rewrites or deletes is the
// legacy_rewrite / orphan_payload shape the reconcile handles.
//
// This is an operator step (cmd/cpexport), run with the server stopped;
// it never runs from the server binary.

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// ContainerProfileExportOptions tunes ExportContainerProfiles.
type ContainerProfileExportOptions struct {
	// BatchSize is the number of rows read per query; non-positive =
	// DefaultMigrationBatchSize.
	BatchSize int
	// DryRun decodes and counts without writing any file.
	DryRun bool
}

// ContainerProfileExportReport is what one export did.
type ContainerProfileExportReport struct {
	// Exported is the number of files written (or, dry-run, that would be).
	Exported int
	// LegacySkipped is the number of rows with rv NULL: legacy rows that were
	// never migrated (or that a legacy writer already rewrote) and whose file
	// is already the legacy writer's. Such a row with NO file (no known
	// producer) is exported from its payloads body at the JSON's
	// resourceVersion instead, and counted under Exported.
	LegacySkipped int
	// Undecodable is the number of payloads that could not be decoded; the
	// row and any existing file are left as they are.
	Undecodable int
	// StaleFilesRemoved is the number of legacy payload files on disk with
	// no matching metadata row: an object deleted under the ObjectStore
	// backend (which removes only the database rows) whose pre-flip .g file
	// survived. Left in place, an old binary reads it back after downgrade
	// and resurrects the deleted object.
	StaleFilesRemoved int
	DryRun            bool
	Elapsed           time.Duration
}

type exportRow struct {
	rowid        int64
	namespace    string
	name         string
	metadataJSON []byte
	rv           int64
	rvNull       bool
	uid          string
	encoding     string
	body         []byte
}

// ExportContainerProfiles writes every migrated ContainerProfile row's
// payload back as its legacy gob file under root, exactly as the legacy
// writer does (staged to <key>.g.t, then renamed into place).
func ExportContainerProfiles(ctx context.Context, pool *sqlitemigration.Pool, fs afero.Fs, root string, scheme *runtime.Scheme, opts ContainerProfileExportOptions) (*ContainerProfileExportReport, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultMigrationBatchSize
	}
	start := time.Now()
	report := &ContainerProfileExportReport{DryRun: opts.DryRun}
	cursor := int64(0)
	for {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		rows, err := readExportRows(ctx, pool, cursor, opts.BatchSize)
		if err != nil {
			return report, err
		}
		if len(rows) == 0 {
			break
		}
		cursor = rows[len(rows)-1].rowid
		for i := range rows {
			r := &rows[i]
			key := K8sKeysToPath("", softwarecomposition.GroupName, ContainerProfileKind, "", r.namespace, r.name)
			p := filepath.Join(root, key)
			rv, uid := r.rv, r.uid
			if r.rvNull {
				if exists, _ := afero.Exists(fs, makePayloadPath(p)); exists {
					report.LegacySkipped++
					continue
				}
				// No file to fall back on: the body is the only copy; the
				// row JSON says which version it is.
				row := &PartialObjectMetadata{}
				if err := json.Unmarshal(r.metadataJSON, row); err != nil {
					report.Undecodable++
					logger.L().Warning("containerprofile export: metadata row is not JSON; skipped", helpers.Error(err), helpers.String("key", key))
					continue
				}
				rv, uid = max(parseRV(row.ResourceVersion), 1), string(row.UID)
				logger.L().Warning("containerprofile export: rv NULL row without a payload file; exported from the payloads body", helpers.String("key", key))
			}
			obj := &softwarecomposition.ContainerProfile{}
			if err := decodePayloadBody(scheme, r.encoding, r.body, obj); err != nil {
				report.Undecodable++
				logger.L().Warning("containerprofile export: payload undecodable; skipped", helpers.Error(err), helpers.String("key", key))
				continue
			}
			// The file carries what the row says (INV-2 makes these equal already).
			obj.ResourceVersion = strconv.FormatInt(rv, 10)
			if uid != "" {
				obj.UID = types.UID(uid)
			}
			if !opts.DryRun {
				if err := writeLegacyPayloadFile(fs, p, obj); err != nil {
					return report, fmt.Errorf("containerprofile export: %s: %w", key, err)
				}
			}
			report.Exported++
		}
	}
	stale, err := reconcileStaleExportedFiles(ctx, pool, fs, root, opts.DryRun)
	if err != nil {
		return report, err
	}
	report.StaleFilesRemoved = stale
	report.Elapsed = time.Since(start)
	logger.L().Info("containerprofile export: done",
		helpers.Int("exported", report.Exported), helpers.Int("legacySkipped", report.LegacySkipped),
		helpers.Int("undecodable", report.Undecodable), helpers.Int("staleFilesRemoved", report.StaleFilesRemoved),
		helpers.Interface("dryRun", report.DryRun), helpers.String("elapsed", report.Elapsed.String()))
	return report, nil
}

// reconcileStaleExportedFiles removes every legacy ContainerProfile payload
// file under root that (a) still decodes as a valid object an old binary
// would serve, AND (b) has no matching metadata row. ObjectStore's delete
// path removes only the database rows, so a key deleted since the pre-flip
// migration (or since a prior export) can still have its old .g file on
// disk; ExportContainerProfiles above only ever visits rows that still
// exist, so it never reaches these. Left in place, an old binary's get()
// after downgrade finds the stale file and returns the deleted object as if
// it were live. This must run after the row export above, using the same
// definition of "current" (the database at read time).
//
// A file that fails to decode is left untouched, exactly as the main export
// loop leaves undecodable payloads: an old binary can't resurrect an object
// from a file it can't decode either, so there is no resurrection risk to
// close, and removing it would just be destroying data outside this tool's
// contract (matches sweepFiles' migration-side handling of undecodable
// content).
func reconcileStaleExportedFiles(ctx context.Context, pool *sqlitemigration.Pool, fs afero.Fs, root string, dryRun bool) (int, error) {
	dir := filepath.Join(root, softwarecomposition.GroupName, ContainerProfileKind)
	if exists, _ := afero.DirExists(fs, dir); !exists {
		return 0, nil
	}
	conn, err := pool.Take(ctx)
	if err != nil {
		return 0, fmt.Errorf("containerprofile export: take connection: %w", err)
	}
	defer pool.Put(conn)
	var stale []string
	walkErr := afero.Walk(fs, dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !IsPayloadFile(path) {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		key := path[len(root) : len(path)-len(GobExt)]
		if _, rerr := ReadMetadata(conn, key); rerr == nil {
			return nil
		} else if !errors.Is(rerr, ErrMetadataNotFound) {
			return fmt.Errorf("read metadata %s: %w", key, rerr)
		}
		if _, found, derr := decodeLegacyFileAt(ctx, fs, root, key); derr != nil || !found {
			// Undecodable, or raced away between Walk and here: leave it.
			return nil
		}
		stale = append(stale, path)
		return nil
	})
	if walkErr != nil {
		return 0, fmt.Errorf("containerprofile export: reconcile stale files: %w", walkErr)
	}
	if dryRun {
		for _, path := range stale {
			logger.L().Warning("containerprofile export: stale payload file with no metadata row would be removed", helpers.String("path", path))
		}
		return len(stale), nil
	}
	for _, path := range stale {
		if rerr := fs.Remove(path); rerr != nil {
			return len(stale), fmt.Errorf("containerprofile export: remove stale file %s: %w", path, rerr)
		}
		logger.L().Warning("containerprofile export: stale payload file with no metadata row removed", helpers.String("path", path))
	}
	return len(stale), nil
}

func readExportRows(ctx context.Context, pool *sqlitemigration.Pool, cursor int64, limit int) ([]exportRow, error) {
	conn, err := pool.Take(ctx)
	if err != nil {
		return nil, fmt.Errorf("containerprofile export: take connection: %w", err)
	}
	defer pool.Put(conn)
	var out []exportRow
	err = sqlitex.Execute(conn,
		`SELECT m.rowid, m.namespace, m.name, m.rv, m.rv IS NULL, m.uid, p.encoding, p.body, m.metadata
			FROM metadata m JOIN payloads p USING (kind, namespace, name)
			WHERE m.kind = :kind AND m.rowid > :cursor
			ORDER BY m.rowid LIMIT :limit`,
		&sqlitex.ExecOptions{
			Named: map[string]any{":kind": ContainerProfileKind, ":cursor": cursor, ":limit": limit},
			ResultFunc: func(stmt *sqlite.Stmt) error {
				r := exportRow{
					rowid:     stmt.ColumnInt64(0),
					namespace: stmt.ColumnText(1),
					name:      stmt.ColumnText(2),
					rv:        stmt.ColumnInt64(3),
					rvNull:    stmt.ColumnInt64(4) == 1,
					uid:       stmt.ColumnText(5),
					encoding:  stmt.ColumnText(6),
				}
				r.body = make([]byte, stmt.ColumnLen(7))
				stmt.ColumnBytes(7, r.body)
				r.metadataJSON = []byte(stmt.ColumnText(8))
				out = append(out, r)
				return nil
			},
		})
	if err != nil {
		return nil, fmt.Errorf("containerprofile export: read rows: %w", err)
	}
	return out, nil
}

// writeLegacyPayloadFile writes obj as the legacy writer does (saveObject):
// gob through the direct-I/O writer into <p>.g.t, then a rename into <p>.g.
func writeLegacyPayloadFile(fs afero.Fs, p string, obj runtime.Object) error {
	if err := fs.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	finalPath := makePayloadPath(p)
	tmpPath := finalPath + ".t"
	f, err := openPayloadFileWithFallbackFs(fs, tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open payload file: %w", err)
	}
	w := NewDirectIOWriter(f)
	if err := gob.NewEncoder(w).Encode(obj); err != nil {
		_ = w.Close()
		_ = f.Close()
		_ = fs.Remove(tmpPath)
		return fmt.Errorf("encode payload: %w", err)
	}
	if err := errors.Join(w.Close(), f.Close()); err != nil {
		_ = fs.Remove(tmpPath)
		return fmt.Errorf("close payload file: %w", err)
	}
	if err := fs.Rename(tmpPath, finalPath); err != nil {
		_ = fs.Remove(tmpPath)
		return fmt.Errorf("rename payload into place: %w", err)
	}
	return nil
}

// openPayloadFileWithFallbackFs is StorageImpl.openPayloadFileWithFallback
// without the receiver.
func openPayloadFileWithFallbackFs(fs afero.Fs, path string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := fs.OpenFile(path, openFlagDirect|flag, perm)
	if err != nil && isDirectIOUnsupported(err) {
		f, err = fs.OpenFile(path, flag, perm)
	}
	return f, err
}
