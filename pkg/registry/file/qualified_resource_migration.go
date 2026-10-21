package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

var qualifiedResourcePrefixes = [...]struct{ old, new string }{
	{"containerprofile", "containerprofiles"},
	{"sbomsyft", "sbomsyfts"},
	{"sbomsyftfiltered", "sbomsyftfiltereds"},
	{"seccompprofile", "seccompprofiles"},
}

// QualifiedResourceMigrationOptions controls an offline prefix migration.
// Direction must be "forward" or "rollback". All other writers must be stopped.
type QualifiedResourceMigrationOptions struct {
	Direction string
	DryRun    bool
	hook      func(string) error
}

// QualifiedResourceMigrationReport counts resources needing work, not objects.
type QualifiedResourceMigrationReport struct {
	Resources int
	Details   []QualifiedResourceMigrationDetail
	DryRun    bool
}

// QualifiedResourceMigrationDetail describes the preflight snapshot for one resource.
type QualifiedResourceMigrationDetail struct {
	From      string
	To        string
	State     string
	Rows      map[string]int64
	Directory bool
	Required  bool
}

type prefixPool interface {
	Take(context.Context) (*sqlite.Conn, error)
	Put(*sqlite.Conn)
}

type prefixIntent struct {
	Version   int              `json:"version"`
	Rows      map[string]int64 `json:"rows"`
	Directory bool             `json:"directory"`
}

type prefixMove struct {
	from, to, marker string
	rows             map[string]int64
	fromPath, toPath string
	tables           []string
	state            string
	intent           prefixIntent
	pending          bool
	work             bool
}

// MigrateQualifiedResourcePrefixes runs before any storage consumers start.
// The durable intent distinguishes a completed rename with uncommitted SQL
// from an unrelated split store. Filesystem and SQLite commits are not atomic;
// no reader may run until every move has completed successfully.
func MigrateQualifiedResourcePrefixes(ctx context.Context, pool prefixPool, gate *WriteGate, fs afero.Fs, root string, opts QualifiedResourceMigrationOptions) (*QualifiedResourceMigrationReport, error) {
	if opts.Direction != "forward" && opts.Direction != "rollback" {
		return nil, fmt.Errorf("resource prefix migration: invalid direction %q", opts.Direction)
	}
	if _, _, err := prefixDirectory(fs, root); err != nil {
		return nil, err
	}
	if _, _, err := prefixDirectory(fs, filepath.Join(root, softwarecomposition.GroupName)); err != nil {
		return nil, err
	}
	conn, err := pool.Take(ctx)
	if err != nil {
		return nil, err
	}
	if err := validatePrefixSchema(conn); err != nil {
		pool.Put(conn)
		return nil, err
	}
	var moves []prefixMove
	for _, pair := range qualifiedResourcePrefixes {
		m := prefixMove{rows: map[string]int64{}, from: pair.old, to: pair.new, marker: "qualified-resource/" + pair.old}
		if opts.Direction == "rollback" {
			m.from, m.to = m.to, m.from
		}
		m.fromPath = filepath.Join(root, softwarecomposition.GroupName, m.from)
		m.toPath = filepath.Join(root, softwarecomposition.GroupName, m.to)
		if err = m.inspect(conn, fs, opts.Direction); err != nil {
			err = fmt.Errorf("%s -> %s: %w", m.from, m.to, err)
			break
		}
		moves = append(moves, m)
	}
	pool.Put(conn)
	if err != nil {
		return nil, fmt.Errorf("resource prefix migration preflight: %w", err)
	}
	report := &QualifiedResourceMigrationReport{DryRun: opts.DryRun}
	for _, m := range moves {
		report.Details = append(report.Details, QualifiedResourceMigrationDetail{From: m.from, To: m.to, State: m.state, Rows: m.rows, Directory: m.intent.Directory, Required: m.work})
		if !m.work {
			continue
		}
		report.Resources++
		if opts.DryRun {
			continue
		}
		if err := m.apply(ctx, pool, gate, fs, opts); err != nil {
			return report, fmt.Errorf("resource prefix migration %s -> %s: %w; keep storage stopped and rerun direction=%s", m.from, m.to, err, opts.Direction)
		}
	}
	return report, nil
}

// prefixDirectory rejects symlinks and counts any entry, including sidecars and
// staging files, as data. Empty directories are safe to remove before rename.
func prefixDirectory(fs afero.Fs, path string) (exists, populated bool, err error) {
	var info os.FileInfo
	if lfs, ok := fs.(afero.Lstater); ok {
		info, _, err = lfs.LstatIfPossible(path)
	} else {
		info, err = fs.Stat(path)
	}
	if os.IsNotExist(err) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, false, fmt.Errorf("%s is not a real directory", path)
	}
	entries, err := afero.ReadDir(fs, path)
	return true, len(entries) != 0, err
}

func (m *prefixMove) inspect(conn *sqlite.Conn, fs afero.Fs, direction string) error {
	var raw string
	tables := map[string]bool{}
	err := sqlitex.Execute(conn, `SELECT name FROM sqlite_master WHERE type='table'`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error {
		tables[s.ColumnText(0)] = true
		return nil
	}})
	if err != nil {
		return err
	}
	if !tables["metadata"] {
		return fmt.Errorf("metadata table is missing")
	}
	if tables["migration_state"] {
		err = sqlitex.Execute(conn, `SELECT state, counts FROM migration_state WHERE name = ?`, &sqlitex.ExecOptions{Args: []any{m.marker}, ResultFunc: func(s *sqlite.Stmt) error {
			m.state, raw = s.ColumnText(0), s.ColumnText(1)
			return nil
		}})
	}
	if err != nil {
		return err
	}
	if m.state != "" && m.state != "forward:done" && m.state != "rollback:done" {
		if m.state != direction+":pending" {
			return fmt.Errorf("%s has unfinished or unknown migration %q", m.marker, m.state)
		}
		if err := json.Unmarshal([]byte(raw), &m.intent); err != nil {
			return fmt.Errorf("%s invalid intent: %w", m.marker, err)
		}
		m.pending = true
	}
	fromExists, fromFiles, err := prefixDirectory(fs, m.fromPath)
	if err != nil {
		return err
	}
	toExists, toFiles, err := prefixDirectory(fs, m.toPath)
	if err != nil {
		return err
	}
	// A resource directory that is itself a mount cannot be renamed into
	// its parent filesystem. Refuse before writing an intent for any resource.
	for _, path := range []string{m.fromPath, m.toPath} {
		info, e := fs.Stat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		parent, e := fs.Stat(filepath.Dir(path))
		if e != nil {
			return e
		}
		a, aok := info.Sys().(*syscall.Stat_t)
		b, bok := parent.Sys().(*syscall.Stat_t)
		if aok && bok && a.Dev != b.Dev {
			return fmt.Errorf("resource directory crosses filesystems: %s", path)
		}
	}
	var fromRows, toRows bool
	var tableStates []string
	for _, table := range []string{"metadata", "payloads", "time_series"} {
		if !tables[table] {
			continue
		}
		m.tables = append(m.tables, table)
		err = sqlitex.Execute(conn, `SELECT (SELECT count(*) FROM `+table+` WHERE kind = ?), (SELECT count(*) FROM `+table+` WHERE kind = ?)`, &sqlitex.ExecOptions{Args: []any{m.from, m.to}, ResultFunc: func(s *sqlite.Stmt) error {
			m.rows[table] = s.ColumnInt64(0)
			tableStates = append(tableStates, fmt.Sprintf("%s=%t/%t", table, s.ColumnInt(0) != 0, s.ColumnInt(1) != 0))
			fromRows = fromRows || s.ColumnInt(0) != 0
			toRows = toRows || s.ColumnInt(1) != 0
			return nil
		}})
		if err != nil {
			return fmt.Errorf("%s: %w", table, err)
		}
	}
	if m.pending {
		if m.intent.Version != 1 || m.intent.Rows == nil {
			return fmt.Errorf("%s invalid intent version or row snapshot", m.marker)
		}
		if _, ok := m.intent.Rows["metadata"]; !ok {
			return fmt.Errorf("%s intent lacks metadata snapshot", m.marker)
		}
		for table, want := range m.intent.Rows {
			count, ok := m.rows[table]
			if !ok || want < 0 || want != count {
				return fmt.Errorf("%s inconsistent pending row count in %s", m.marker, table)
			}
		}
		// Startup may have added optional empty tables to an older database
		// after an offline interruption. That additive schema upgrade does
		// not invalidate the recorded source population.
		for table, count := range m.rows {
			if _, ok := m.intent.Rows[table]; !ok && count != 0 {
				return fmt.Errorf("%s unexpected pending rows in %s", m.marker, table)
			}
		}
		if toRows || (fromFiles && toFiles) || (m.intent.Directory && fromExists == toExists) || (!m.intent.Directory && (!fromRows || fromExists || toExists)) {
			return fmt.Errorf("%s inconsistent pending migration (%s / %s, directories %t/%t, %s)", m.marker, m.fromPath, m.toPath, fromExists, toExists, strings.Join(tableStates, ", "))
		}
		m.work = true
		return nil
	}
	if (fromExists || fromRows) && (toFiles || toRows) {
		return fmt.Errorf("%s conflicts with %s (files %t/%t, %s)", m.fromPath, m.toPath, fromFiles, toFiles, strings.Join(tableStates, ", "))
	}
	if m.state == direction+":done" && (fromExists || fromRows) {
		return fmt.Errorf("completed marker contradicts source population")
	}
	oppositeDone := m.state != "" && m.state != direction+":done"
	if oppositeDone && (toFiles || toRows) {
		return fmt.Errorf("completed marker contradicts destination population")
	}
	m.work = fromExists || fromRows || oppositeDone
	m.intent = prefixIntent{Version: 1, Rows: m.rows, Directory: fromExists}
	return nil
}

func prefixTransaction(ctx context.Context, pool prefixPool, gate *WriteGate, fn func(*sqlite.Conn) error) (err error) {
	// The intent must reach durable storage before the filesystem rename. Do
	// not accept a pool configured for asynchronous WAL commits.
	durable := func(c *sqlite.Conn) error {
		if err := prefixDurability(c); err != nil {
			return err
		}
		return fn(c)
	}
	if gate != nil {
		return gate.run(ctx, priorityLow, "resource_prefix_migration", "", func(_ context.Context, conn *sqlite.Conn) error { return durable(conn) })
	}
	conn, err := pool.Take(ctx)
	if err != nil {
		return err
	}
	defer pool.Put(conn)
	end, err := sqlitex.ImmediateTransaction(conn)
	if err != nil {
		return err
	}
	// The driver checks autocommit after COMMIT errors and rolls back any
	// remaining transaction, including panic unwinding, before pool return.
	defer end(&err)
	return durable(conn)
}

// Check the connection performing the commit, not another pooled connection.
func prefixDurability(c *sqlite.Conn) error {
	var journal string
	var synchronous int
	if err := sqlitex.Execute(c, `PRAGMA journal_mode`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { journal = s.ColumnText(0); return nil }}); err != nil {
		return err
	}
	if err := sqlitex.Execute(c, `PRAGMA synchronous`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { synchronous = s.ColumnInt(0); return nil }}); err != nil {
		return err
	}
	if (journal == "wal" && synchronous >= 2) || (journal == "delete" && synchronous >= 3) {
		return nil
	}
	return fmt.Errorf("resource prefix migration requires WAL synchronous=FULL/EXTRA or DELETE synchronous=EXTRA (journal=%s synchronous=%d)", journal, synchronous)
}

func syncPrefixParent(fs afero.Fs, path string) error {
	f, err := fs.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	err = f.Sync()
	return errors.Join(err, f.Close())
}

func (m *prefixMove) apply(ctx context.Context, pool prefixPool, gate *WriteGate, fs afero.Fs, opts QualifiedResourceMigrationOptions) error {
	check := func(step string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if opts.hook != nil {
			return opts.hook(step)
		}
		return nil
	}
	// Validate the actual writer before even removing an empty destination.
	if err := prefixTransaction(ctx, pool, gate, func(c *sqlite.Conn) error { return nil }); err != nil {
		return err
	}
	// A completed migration still changes direction after every object was
	// deleted. Otherwise the next old-binary write would contradict a stale
	// forward:done marker on re-upgrade. With no files or rows, a single SQL
	// commit is sufficient; never create an ambiguous empty pending intent.
	empty := !m.intent.Directory
	for _, count := range m.rows {
		empty = empty && count == 0
	}
	if !m.pending && empty {
		if err := prefixTransaction(ctx, pool, gate, func(c *sqlite.Conn) error {
			if err := sqlitex.Execute(c, `UPDATE migration_state SET state = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?`, &sqlitex.ExecOptions{Args: []any{opts.Direction + ":done", m.marker}}); err != nil {
				return err
			}
			return check("before-commit")
		}); err != nil {
			return err
		}
		return check("after-commit")
	}
	if !m.pending {
		// Remove only an empty destination, before recording intent; a pending
		// directory move therefore always has exactly one directory present.
		if exists, populated, err := prefixDirectory(fs, m.toPath); err != nil {
			return err
		} else if exists {
			if populated {
				return fmt.Errorf("destination became populated: %s", m.toPath)
			}
			if err := fs.Remove(m.toPath); err != nil {
				return err
			}
			if err := syncPrefixParent(fs, m.toPath); err != nil {
				return err
			}
		}
		raw, err := json.Marshal(m.intent)
		if err != nil {
			return err
		}
		if err := prefixTransaction(ctx, pool, gate, func(c *sqlite.Conn) error {
			if err := sqlitex.Execute(c, SchemaMigrations()[5], nil); err != nil {
				return err
			}
			return sqlitex.Execute(c, `INSERT INTO migration_state(name,state,counts,updated_at) VALUES(?,?,?,CURRENT_TIMESTAMP) ON CONFLICT(name) DO UPDATE SET state=excluded.state, counts=excluded.counts, updated_at=excluded.updated_at`, &sqlitex.ExecOptions{Args: []any{m.marker, opts.Direction + ":pending", string(raw)}})
		}); err != nil {
			return err
		}
	}
	if err := check("before-rename"); err != nil {
		return err
	}
	if m.intent.Directory {
		if exists, _, err := prefixDirectory(fs, m.fromPath); err != nil {
			return err
		} else if exists {
			if err := fs.Rename(m.fromPath, m.toPath); err != nil {
				return err
			}
		}
		if err := check("after-directory-rename"); err != nil {
			return err
		}
		// Also sync after resuming a rename that happened before a crash.
		if err := syncPrefixParent(fs, m.toPath); err != nil {
			return err
		}
	}
	if err := check("after-rename"); err != nil {
		return err
	}
	err := prefixTransaction(ctx, pool, gate, func(c *sqlite.Conn) error {
		for _, table := range m.tables {
			if err := sqlitex.Execute(c, `UPDATE `+table+` SET kind = ? WHERE kind = ?`, &sqlitex.ExecOptions{Args: []any{m.to, m.from}}); err != nil {
				return err
			}
			if err := check("after-sql-" + table); err != nil {
				return err
			}
		}
		if err := sqlitex.Execute(c, `UPDATE migration_state SET state = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?`, &sqlitex.ExecOptions{Args: []any{opts.Direction + ":done", m.marker}}); err != nil {
			return err
		}
		return check("before-commit")
	})
	if err != nil {
		return err
	}
	return check("after-commit")
}

// Only released, ordered schema prefixes are supported. Offline inspection
// never upgrades the schema to make an unknown layout appear compatible.
func validatePrefixSchema(c *sqlite.Conn) error {
	var version int
	if err := sqlitex.Execute(c, `PRAGMA user_version`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { version = s.ColumnInt(0); return nil }}); err != nil {
		return err
	}
	if version < 1 || version > len(SchemaMigrations()) {
		return fmt.Errorf("unsupported SQLite schema version %d", version)
	}
	expected := map[string]string{"metadata": "kind:1,namespace:2,name:3,metadata:0"}
	if version >= 2 {
		expected["time_series"] = "kind:1,namespace:2,name:3,seriesID:4,reportTimestamp:0,status:0,tsSuffix:5,completion:0,previousReportTimestamp:0,hasData:0"
	}
	if version >= 3 {
		expected["metadata"] += ",rv:0"
	}
	if version >= 4 {
		expected["metadata"] += ",uid:0"
	}
	if version >= 5 {
		expected["payloads"] = "kind:1,namespace:2,name:3,encoding:0,body:0"
	}
	if version >= 7 {
		expected["metadata"] += ",is_time_series:0"
	}
	for _, table := range []string{"metadata", "time_series", "payloads", "migration_state"} {
		var columns []string
		if err := sqlitex.Execute(c, `PRAGMA table_info(`+table+`)`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error {
			name := s.ColumnText(1)
			typ := "TEXT"
			if table == "metadata" && name == "metadata" {
				typ = "JSON"
			}
			if (table == "metadata" && (name == "rv" || name == "is_time_series")) || (table == "time_series" && name == "hasData") {
				typ = "INTEGER"
			}
			if table == "payloads" && name == "body" {
				typ = "BLOB"
			}
			required := table == "payloads" || (table == "migration_state" && name == "state") || (table == "metadata" && name == "is_time_series")
			if strings.ToUpper(s.ColumnText(2)) != typ || (s.ColumnInt(3) != 0) != required {
				return fmt.Errorf("unsupported %s.%s type or nullability", table, name)
			}
			columns = append(columns, fmt.Sprintf("%s:%d", name, s.ColumnInt(5)))
			return nil
		}}); err != nil {
			return err
		}
		want := expected[table]
		if table == "migration_state" && (version >= 6 || len(columns) > 0) {
			want = "name:1,state:0,counts:0,updated_at:0"
		}
		if strings.Join(columns, ",") != want {
			return fmt.Errorf("unsupported %s layout at schema version %d", table, version)
		}
	}
	return sqlitex.Execute(c, `SELECT name FROM sqlite_master WHERE type='trigger' AND tbl_name IN ('metadata','payloads','time_series','migration_state')`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { return fmt.Errorf("unsupported storage trigger %s", s.ColumnText(0)) }})
}
