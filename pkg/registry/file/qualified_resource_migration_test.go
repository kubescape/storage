package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

func prefixDB(t *testing.T, pool *sqlitemigration.Pool, query string, args ...any) {
	t.Helper()
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(c)
	require.NoError(t, sqlitex.Execute(c, query, &sqlitex.ExecOptions{Args: args}))
}

func prefixRows(t *testing.T, pool *sqlitemigration.Pool, table, kind string) []string {
	t.Helper()
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(c)
	var rows []string
	require.NoError(t, sqlitex.Execute(c, `SELECT rowid, * FROM `+table+` WHERE kind=? ORDER BY rowid`, &sqlitex.ExecOptions{Args: []any{kind}, ResultFunc: func(s *sqlite.Stmt) error {
		for i := 0; i < s.ColumnCount(); i++ {
			if i != 1 {
				rows = append(rows, s.ColumnText(i))
			}
		}
		return nil
	}}))
	return rows
}

func TestQualifiedResourceMigrationRoundTrip(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	pool := NewPool(db, 2, 0)
	defer func() { require.NoError(t, pool.Close()) }()
	fs := afero.NewOsFs()
	before := map[string][]string{}
	prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name,metadata,rv,uid) VALUES('knownservers','ns','keep','{"keep":true}',42,'unrelated-uid')`)
	unrelated := prefixRows(t, pool, "metadata", "knownservers")
	for _, p := range qualifiedResourcePrefixes {
		prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name,metadata,rv,uid,is_time_series) VALUES(?, 'ns','name','{"name":"name","namespace":"ns","resourceVersion":"7"}',7,'uid',0)`, p.old)
		prefixDB(t, pool, `INSERT INTO payloads(kind,namespace,name,encoding,body) VALUES(?,'ns','name','json',?)`, p.old, append([]byte{0, 255, 0}, []byte("unchanged payload")...))
		prefixDB(t, pool, `INSERT INTO time_series(kind,namespace,name,seriesID,tsSuffix) VALUES(?,'ns','name','series','suffix')`, p.old)
		for _, table := range []string{"metadata", "payloads", "time_series"} {
			before[p.old+table] = prefixRows(t, pool, table, p.old)
		}
		for _, suffix := range []string{"ns/name.g", "ns/name.m", "ns/name.g.t", "cluster/account/region/name.g"} {
			path := filepath.Join(root, softwarecomposition.GroupName, p.old, suffix)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte(suffix), 0644))
		}
	}
	for _, direction := range []string{"forward", "rollback", "forward"} {
		dry, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, fs, root, QualifiedResourceMigrationOptions{Direction: direction, DryRun: true})
		require.NoError(t, err)
		require.Equal(t, 4, dry.Resources)
		report, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, fs, root, QualifiedResourceMigrationOptions{Direction: direction})
		require.NoError(t, err)
		require.Equal(t, 4, report.Resources)
		require.NoError(t, pool.Close())
		pool = NewPool(db, 2, 0)
		for _, p := range qualifiedResourcePrefixes {
			from, to := p.old, p.new
			if direction == "rollback" {
				from, to = to, from
			}
			for _, table := range []string{"metadata", "payloads", "time_series"} {
				require.Empty(t, prefixRows(t, pool, table, from))
				require.Equal(t, before[p.old+table], prefixRows(t, pool, table, to))
			}
			require.NoDirExists(t, filepath.Join(root, softwarecomposition.GroupName, from))
			for _, suffix := range []string{"ns/name.g", "ns/name.m", "ns/name.g.t", "cluster/account/region/name.g"} {
				got, err := os.ReadFile(filepath.Join(root, softwarecomposition.GroupName, to, suffix))
				require.NoError(t, err)
				require.Equal(t, []byte(suffix), got)
			}
		}
		again, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, fs, root, QualifiedResourceMigrationOptions{Direction: direction})
		require.NoError(t, err)
		require.Zero(t, again.Resources)
		require.Equal(t, unrelated, prefixRows(t, pool, "metadata", "knownservers"))
	}
}

func TestQualifiedResourceMigrationResume(t *testing.T) {
	for _, step := range []string{"before-rename", "after-rename", "before-commit", "after-commit"} {
		t.Run(step, func(t *testing.T) {
			root := t.TempDir()
			pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
			defer pool.Close()
			prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name,metadata) VALUES('containerprofile','ns','x','{}')`)
			path := filepath.Join(root, softwarecomposition.GroupName, "containerprofile", "ns", "x.g")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte("bytes"), 0644))
			_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward", hook: func(s string) error {
				if s == step {
					return errors.New("interrupted")
				}
				return nil
			}})
			require.ErrorContains(t, err, "interrupted")
			_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
			require.NoError(t, err)
			require.Empty(t, prefixRows(t, pool, "metadata", "containerprofile"))
			require.NotEmpty(t, prefixRows(t, pool, "metadata", "containerprofiles"))
			require.FileExists(t, filepath.Join(root, softwarecomposition.GroupName, "containerprofiles", "ns", "x.g"))
		})
	}
}

func TestQualifiedResourceMigrationPreflight(t *testing.T) {
	for _, conflict := range []string{"metadata", "payloads", "time_series", "files"} {
		t.Run(conflict, func(t *testing.T) {
			root := t.TempDir()
			pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
			defer pool.Close()
			// A later resource conflict must prevent the first resource moving.
			prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('containerprofile','ns','first'),('seccompprofile','ns','last')`)
			switch conflict {
			case "metadata":
				prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('seccompprofiles','ns','other')`)
			case "payloads":
				prefixDB(t, pool, `INSERT INTO payloads(kind,namespace,name,encoding,body) VALUES('seccompprofiles','ns','other','json','{}')`)
			case "time_series":
				prefixDB(t, pool, `INSERT INTO time_series(kind,namespace,name) VALUES('seccompprofiles','ns','other')`)
			case "files":
				path := filepath.Join(root, softwarecomposition.GroupName, "seccompprofiles", "x.g")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
				require.NoError(t, os.WriteFile(path, []byte("x"), 0644))
			}
			_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
			require.ErrorContains(t, err, "preflight")
			require.NotEmpty(t, prefixRows(t, pool, "metadata", "containerprofile"))
			require.Empty(t, prefixRows(t, pool, "metadata", "containerprofiles"))
		})
	}
}

// Exit without defers, including inside the SQL transaction, to exercise real
// SQLite recovery rather than only the error-return rollback path.
func TestQualifiedResourceMigrationProcessCrash(t *testing.T) {
	if root := os.Getenv("PREFIX_MIGRATION_CRASH_ROOT"); root != "" {
		pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
		seen := 0
		target, _ := strconv.Atoi(os.Getenv("PREFIX_MIGRATION_CRASH_RESOURCE"))
		_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{
			Direction: os.Getenv("PREFIX_MIGRATION_DIRECTION"), hook: func(step string) error {
				if step == os.Getenv("PREFIX_MIGRATION_CRASH_STEP") {
					if seen == target {
						os.Exit(23)
					}
					seen++
				}
				return nil
			},
		})
		t.Fatalf("crash point not reached: %v", err)
	}
	for _, direction := range []string{"forward", "rollback"} {
		for target := range qualifiedResourcePrefixes {
			for _, step := range []string{"before-rename", "after-directory-rename", "after-rename", "after-sql-metadata", "after-sql-payloads", "after-sql-time_series", "before-commit", "after-commit"} {
				t.Run(fmt.Sprintf("%s/%d/%s", direction, target, step), func(t *testing.T) {
					root := t.TempDir()
					db := filepath.Join(root, "metadata.sq3")
					pool := NewPool(db, 2, 0)
					for _, pair := range qualifiedResourcePrefixes {
						from := pair.old
						if direction == "rollback" {
							from = pair.new
						}
						prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES(?,'ns','x')`, from)
						prefixDB(t, pool, `INSERT INTO payloads(kind,namespace,name,encoding,body) VALUES(?,'ns','x','json','{}')`, from)
						prefixDB(t, pool, `INSERT INTO time_series(kind,namespace,name) VALUES(?,'ns','x')`, from)
						path := filepath.Join(root, softwarecomposition.GroupName, from, "ns", "x.g")
						require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
						require.NoError(t, os.WriteFile(path, []byte("unchanged"), 0644))
					}
					require.NoError(t, pool.Close())
					cmd := exec.Command(os.Args[0], "-test.run=^TestQualifiedResourceMigrationProcessCrash$")
					cmd.Env = append(os.Environ(), "PREFIX_MIGRATION_CRASH_ROOT="+root, "PREFIX_MIGRATION_CRASH_STEP="+step, "PREFIX_MIGRATION_DIRECTION="+direction, fmt.Sprintf("PREFIX_MIGRATION_CRASH_RESOURCE=%d", target))
					output, err := cmd.CombinedOutput()
					var exit *exec.ExitError
					require.ErrorAs(t, err, &exit, "%s", output)
					require.Equal(t, 23, exit.ExitCode(), "%s", output)
					pool = NewPool(db, 2, 0)
					defer pool.Close()
					if direction == "rollback" && step != "after-commit" {
						_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
						require.Error(t, err)
					}
					_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: direction})
					require.NoError(t, err)
					for _, pair := range qualifiedResourcePrefixes {
						from, to := pair.old, pair.new
						if direction == "rollback" {
							from, to = to, from
						}
						for _, table := range []string{"metadata", "payloads", "time_series"} {
							require.Empty(t, prefixRows(t, pool, table, from))
							require.NotEmpty(t, prefixRows(t, pool, table, to))
						}
						body, err := os.ReadFile(filepath.Join(root, softwarecomposition.GroupName, to, "ns", "x.g"))
						require.NoError(t, err)
						require.Equal(t, "unchanged", string(body))
					}
				})
			}
		}
	}
}

func TestQualifiedResourceMigrationDirectoryOnly(t *testing.T) {
	root := t.TempDir()
	pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	group := filepath.Join(root, softwarecomposition.GroupName)
	require.NoError(t, os.MkdirAll(filepath.Join(group, "sbomsyft"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(group, "sbomsyft", "orphan.m"), []byte("sidecar"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(group, "sbomsyfts"), 0755))
	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(group, "sbomsyfts", "orphan.m"))
	require.NoDirExists(t, filepath.Join(group, "sbomsyft"))
}

func TestQualifiedResourceMigrationRefusesUnknownPendingState(t *testing.T) {
	for _, state := range []string{"rollback:pending", "unexpected", "forward:pending"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
			defer pool.Close()
			prefixDB(t, pool, `INSERT INTO migration_state(name,state,counts) VALUES('qualified-resource/containerprofile',?, '{"directory":true}')`, state)
			_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
			require.ErrorContains(t, err, "preflight")
		})
	}
}

func TestQualifiedResourceMigrationRefusesEmptyDatabaseOnlyIntent(t *testing.T) {
	root := t.TempDir()
	pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	// A SQL-only intent could only have been recorded for existing rows.
	// Their unexplained disappearance is not a recognized crash boundary.
	prefixDB(t, pool, `INSERT INTO migration_state(name,state,counts) VALUES('qualified-resource/containerprofile','forward:pending','{"directory":false}')`)
	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.Error(t, err)
}

func TestQualifiedResourceMigrationConflictMatrix(t *testing.T) {
	for _, source := range []string{"files", "metadata", "payloads", "time_series"} {
		for _, destination := range []string{"files", "metadata", "payloads", "time_series"} {
			t.Run(source+"/"+destination, func(t *testing.T) {
				root := t.TempDir()
				pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
				defer pool.Close()
				seed := func(location, kind string) {
					switch location {
					case "files":
						path := filepath.Join(root, softwarecomposition.GroupName, kind, "object.g")
						require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
						require.NoError(t, os.WriteFile(path, []byte(kind), 0644))
					case "payloads":
						prefixDB(t, pool, `INSERT INTO payloads(kind,namespace,name,encoding,body) VALUES(?,'ns','x','json','{}')`, kind)
					default:
						prefixDB(t, pool, `INSERT INTO `+location+`(kind,namespace,name) VALUES(?,'ns','x')`, kind)
					}
				}
				seed(source, "sbomsyft")
				seed(destination, "sbomsyfts")
				for _, direction := range []string{"forward", "rollback"} {
					_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: direction})
					require.ErrorContains(t, err, "conflicts")
				}
				c, err := pool.Take(context.Background())
				require.NoError(t, err)
				require.NoError(t, sqlitex.Execute(c, `SELECT count(*) FROM migration_state`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error {
					require.Zero(t, s.ColumnInt(0))
					return nil
				}}))
				pool.Put(c)
			})
		}
	}
}

func TestQualifiedResourceMigrationRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	group := filepath.Join(root, softwarecomposition.GroupName)
	require.NoError(t, os.MkdirAll(group, 0755))
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(group, "sbomsyft")))
	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.ErrorContains(t, err, "not a real directory")
}

func TestQualifiedResourceMigrationEmptyAndDatabaseOnly(t *testing.T) {
	root := t.TempDir()
	pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
	defer pool.Close()
	for _, direction := range []string{"forward", "rollback"} {
		r, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: direction})
		require.NoError(t, err)
		require.Zero(t, r.Resources)
	}
	prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('containerprofile','ns','x')`)
	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
	require.NotEmpty(t, prefixRows(t, pool, "metadata", "containerprofiles"))
}

func TestQualifiedResourceMigrationDurabilityBeforeFilesystemChanges(t *testing.T) {
	for _, gated := range []bool{false, true} {
		t.Run(fmt.Sprint(gated), func(t *testing.T) {
			root := t.TempDir()
			pool := NewPoolWithOptions(filepath.Join(root, "metadata.sq3"), PoolOptions{Size: 2, PrepareConn: func(c *sqlite.Conn) error { return sqlitex.Execute(c, `PRAGMA synchronous=NORMAL`, nil) }})
			defer pool.Close()
			fixture := openFixtureConn(t, pool, filepath.Join(root, "metadata.sq3"), DefaultBusyTimeout)
			require.NoError(t, sqlitex.Execute(fixture, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
			to := filepath.Join(root, softwarecomposition.GroupName, "sbomsyfts")
			require.NoError(t, os.MkdirAll(to, 0755))
			var gate *WriteGate
			if gated {
				var err error
				gate, err = NewWriteGate(context.Background(), pool)
				require.NoError(t, err)
				defer gate.Close()
			}
			_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, gate, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
			require.ErrorContains(t, err, "synchronous")
			require.DirExists(t, to)
			require.NotEmpty(t, prefixRows(t, pool, "metadata", "sbomsyft"))
		})
	}
}

func TestQualifiedResourceMigrationLateSQLFailure(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	pool := NewPool(db, 2, 0)
	defer pool.Close()
	prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`)
	prefixDB(t, pool, `INSERT INTO payloads(kind,namespace,name,encoding,body) VALUES('sbomsyft','ns','x','json','{}')`)
	fixture := openFixtureConn(t, pool, db, DefaultBusyTimeout)
	_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward", hook: func(step string) error {
		if step == "after-rename" {
			return sqlitex.Execute(fixture, `INSERT INTO payloads(kind,namespace,name,encoding,body) VALUES('sbomsyfts','ns','x','json','conflict')`, nil)
		}
		return nil
	}})
	require.Error(t, err)
	require.NotEmpty(t, prefixRows(t, pool, "metadata", "sbomsyft"))
	require.Empty(t, prefixRows(t, pool, "metadata", "sbomsyfts"))
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, sqlitex.Execute(c, `SELECT state FROM migration_state WHERE name='qualified-resource/sbomsyft'`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { require.Equal(t, "forward:pending", s.ColumnText(0)); return nil }}))
	pool.Put(c)
	require.NoError(t, sqlitex.Execute(fixture, `DELETE FROM payloads WHERE kind='sbomsyfts'`, nil))
	_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
}

func TestQualifiedResourceMigrationResumeAfterSchemaUpgrade(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	c, err := sqlite.OpenConn(db)
	require.NoError(t, err)
	require.NoError(t, sqlitex.Execute(c, SchemaMigrations()[0], nil))
	require.NoError(t, sqlitex.Execute(c, `PRAGMA user_version=1`, nil))
	require.NoError(t, sqlitex.Execute(c, SchemaMigrations()[5], nil))
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO migration_state(name,state,counts) VALUES('qualified-resource/sbomsyft','forward:pending','{"version":1,"directory":false,"rows":{"metadata":1}}')`, nil))
	require.NoError(t, c.Close())
	pool := NewPool(db, 2, 0)
	defer pool.Close()
	_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
	require.NotEmpty(t, prefixRows(t, pool, "metadata", "sbomsyfts"))
}

type prefixFailureFS struct {
	afero.Fs
	operation string
}
type prefixFailureFile struct{ afero.File }

func (f prefixFailureFile) Sync() error { return errors.New("injected directory sync failure") }
func (f prefixFailureFS) Rename(old, new string) error {
	if f.operation == "rename" {
		return errors.New("injected rename failure")
	}
	return f.Fs.Rename(old, new)
}
func (f prefixFailureFS) Open(name string) (afero.File, error) {
	handle, err := f.Fs.Open(name)
	if err == nil && f.operation == "sync" && filepath.Base(name) == softwarecomposition.GroupName {
		return prefixFailureFile{handle}, nil
	}
	return handle, err
}

func TestQualifiedResourceFilesystemFailureRecovery(t *testing.T) {
	for _, operation := range []string{"rename", "sync"} {
		t.Run(operation, func(t *testing.T) {
			root := t.TempDir()
			pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
			defer pool.Close()
			prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`)
			path := filepath.Join(root, softwarecomposition.GroupName, "sbomsyft", "x.g")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte("preserved"), 0644))
			_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, prefixFailureFS{afero.NewOsFs(), operation}, root, QualifiedResourceMigrationOptions{Direction: "forward"})
			require.ErrorContains(t, err, "injected")
			require.NotEmpty(t, prefixRows(t, pool, "metadata", "sbomsyft"))
			_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
			require.NoError(t, err)
			b, err := os.ReadFile(filepath.Join(root, softwarecomposition.GroupName, "sbomsyfts", "x.g"))
			require.NoError(t, err)
			require.Equal(t, "preserved", string(b))
		})
	}
}

func TestQualifiedResourceCommitBusyRecovery(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	seed, err := sqlite.OpenConn(db, sqlite.OpenReadWrite|sqlite.OpenCreate)
	require.NoError(t, err)
	for _, statement := range SchemaMigrations() {
		require.NoError(t, sqlitex.Execute(seed, statement, nil))
	}
	require.NoError(t, sqlitex.Execute(seed, `PRAGMA user_version=7`, nil))
	require.NoError(t, sqlitex.Execute(seed, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
	require.NoError(t, seed.Close())
	pool, err := sqlitex.NewPool(db, sqlitex.PoolOptions{PoolSize: 1, Flags: sqlite.OpenReadWrite, PrepareConn: func(c *sqlite.Conn) error {
		c.SetBusyTimeout(0)
		return sqlitex.Execute(c, `PRAGMA synchronous=EXTRA`, nil)
	}})
	require.NoError(t, err)
	defer pool.Close()
	reader, err := sqlite.OpenConn(db, sqlite.OpenReadOnly)
	require.NoError(t, err)
	defer reader.Close()
	_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward", hook: func(step string) error {
		if step == "after-rename" {
			if err := sqlitex.Execute(reader, `BEGIN`, nil); err != nil {
				return err
			}
			return sqlitex.Execute(reader, `SELECT * FROM metadata`, nil)
		}
		return nil
	}})
	require.Equal(t, sqlite.ResultBusy, sqlite.ErrCode(err), "%v", err)
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.True(t, c.AutocommitEnabled(), "failed COMMIT must leave no transaction in pool")
	require.NoError(t, sqlitex.Execute(c, `SELECT kind FROM metadata`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { require.Equal(t, "sbomsyft", s.ColumnText(0)); return nil }}))
	pool.Put(c)
	require.NoError(t, sqlitex.Execute(reader, `ROLLBACK`, nil))
	_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.NoError(t, err)
}

func TestQualifiedResourceChecksDedicatedWriterDurability(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	pool := NewPool(db, 2, 0)
	defer pool.Close()
	fixture := openFixtureConn(t, pool, db, DefaultBusyTimeout)
	require.NoError(t, sqlitex.Execute(fixture, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
	gate, err := NewWriteGate(context.Background(), pool)
	require.NoError(t, err)
	defer gate.Close()
	// No gate operation is running. Only its dedicated writer is weakened;
	// inspecting another borrowed connection would falsely accept this setup.
	require.NoError(t, sqlitex.Execute(gate.conn, `PRAGMA synchronous=NORMAL`, nil))
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, prefixDurability(c))
	pool.Put(c)
	_, err = MigrateQualifiedResourcePrefixes(context.Background(), pool, gate, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: "forward"})
	require.ErrorContains(t, err, "synchronous=1")
	require.NotEmpty(t, prefixRows(t, pool, "metadata", "sbomsyft"))
}

func TestQualifiedResourceEmptyRollbackAllowsOldWriterAndReupgrade(t *testing.T) {
	for _, step := range []string{"", "before-commit", "after-commit"} {
		t.Run(step, func(t *testing.T) {
			root := t.TempDir()
			pool := NewPool(filepath.Join(root, "metadata.sq3"), 2, 0)
			defer pool.Close()
			prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('containerprofile','ns','first')`)
			run := func(direction string, hook func(string) error) error {
				_, err := MigrateQualifiedResourcePrefixes(context.Background(), pool, nil, afero.NewOsFs(), root, QualifiedResourceMigrationOptions{Direction: direction, hook: hook})
				return err
			}
			require.NoError(t, run("forward", nil))
			prefixDB(t, pool, `DELETE FROM metadata WHERE kind='containerprofiles'`)
			err := run("rollback", func(s string) error {
				if step != "" && s == step {
					return errors.New("interrupted empty transition")
				}
				return nil
			})
			if step != "" {
				require.ErrorContains(t, err, "interrupted empty transition")
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, run("rollback", nil))
			prefixDB(t, pool, `INSERT INTO metadata(kind,namespace,name) VALUES('containerprofile','ns','old-writer')`)
			require.NoError(t, run("forward", nil))
			require.NotEmpty(t, prefixRows(t, pool, "metadata", "containerprofiles"))
		})
	}
}
