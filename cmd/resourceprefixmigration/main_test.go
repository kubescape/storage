package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func TestCommandRoundTrip(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	pool := file.NewPool(db, 2, 0)
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
	pool.Put(c)
	require.NoError(t, pool.Close())
	for _, direction := range []string{"forward", "rollback", "forward"} {
		before, err := os.ReadFile(db)
		require.NoError(t, err)
		var out bytes.Buffer
		require.NoError(t, run([]string{"-root", root, "-direction", direction, "-dry-run"}, &out))
		after, err := os.ReadFile(db)
		require.NoError(t, err)
		require.Equal(t, before, after)
		require.Contains(t, out.String(), "resources=1 dryRun=true")
		out.Reset()
		require.NoError(t, run([]string{"-root", root, "-direction", direction}, &out))
		require.Contains(t, out.String(), "resources=1 dryRun=false")
	}
}

func TestCommandRejectsArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"-direction", "bad"}, {"-direction", "forward", "extra"}, {"-direction", "forward", "-db", "/does-not-exist"}} {
		require.Error(t, run(args, &bytes.Buffer{}))
	}
}

func TestCommandDryRunOlderSchema(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	c, err := sqlite.OpenConn(db)
	require.NoError(t, err)
	require.NoError(t, sqlitex.Execute(c, file.SchemaMigrations()[0], nil))
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
	require.NoError(t, sqlitex.Execute(c, `PRAGMA user_version=1`, nil))
	require.NoError(t, c.Close())
	before, err := os.ReadFile(db)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, run([]string{"-root", root, "-direction", "forward", "-dry-run"}, &out))
	require.Contains(t, out.String(), "resources=1 dryRun=true")
	after, err := os.ReadFile(db)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func snapshotSource(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			files[path] = "directory"
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[path] = string(b)
		return nil
	}))
	return files
}

func TestCommandHistoricalSchemasAndStrictDryRun(t *testing.T) {
	for version := 1; version <= len(file.SchemaMigrations()); version++ {
		for _, journal := range []string{"WAL", "DELETE"} {
			t.Run(fmt.Sprintf("v%d/%s", version, journal), func(t *testing.T) {
				root := t.TempDir()
				db := filepath.Join(root, "metadata.sq3")
				c, err := sqlite.OpenConn(db)
				require.NoError(t, err)
				for _, statement := range file.SchemaMigrations()[:version] {
					require.NoError(t, sqlitex.Execute(c, statement, nil))
				}
				require.NoError(t, sqlitex.Execute(c, fmt.Sprintf("PRAGMA user_version=%d", version), nil))
				require.NoError(t, sqlitex.Execute(c, "PRAGMA journal_mode="+journal, nil))
				require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
				require.NoError(t, c.Close())
				before := snapshotSource(t, root)
				require.NoError(t, run([]string{"-root", root, "-direction", "forward", "-dry-run"}, &bytes.Buffer{}))
				require.Equal(t, before, snapshotSource(t, root))
				for _, direction := range []string{"forward", "rollback", "forward"} {
					require.NoError(t, run([]string{"-root", root, "-direction", direction}, &bytes.Buffer{}))
				}
				c, err = sqlite.OpenConn(db, sqlite.OpenReadOnly)
				require.NoError(t, err)
				defer c.Close()
				require.NoError(t, sqlitex.Execute(c, `PRAGMA user_version`, &sqlitex.ExecOptions{ResultFunc: func(s *sqlite.Stmt) error { require.Equal(t, version, s.ColumnInt(0)); return nil }}))
			})
		}
	}
}

func TestCommandWrongPathDoesNotCreate(t *testing.T) {
	root := t.TempDir()
	before := snapshotSource(t, root)
	require.Error(t, run([]string{"-root", root, "-direction", "forward", "-dry-run"}, &bytes.Buffer{}))
	require.Equal(t, before, snapshotSource(t, root))
}

func TestCommandDryRunPreservesLiveSidecarsAndPendingState(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	pool := file.NewPool(db, 2, 0)
	defer pool.Close()
	c, err := pool.Take(context.Background())
	require.NoError(t, err)
	defer pool.Put(c)
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name) VALUES('sbomsyft','ns','x')`, nil))
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO migration_state(name,state,counts) VALUES('qualified-resource/sbomsyft','forward:pending','{"version":1,"directory":false,"rows":{"metadata":1,"payloads":0,"time_series":0}}')`, nil))
	// The connection is idle throughout copying: no concurrent source writer.
	before := snapshotSource(t, root)
	require.NoError(t, run([]string{"-root", root, "-direction", "forward", "-dry-run"}, &bytes.Buffer{}))
	require.Equal(t, before, snapshotSource(t, root))
}

func TestCommandDryRunHotRollbackJournal(t *testing.T) {
	if db := os.Getenv("PREFIX_HOT_DB"); db != "" {
		c, err := sqlite.OpenConn(db, sqlite.OpenReadWrite)
		require.NoError(t, err)
		for _, q := range []string{`PRAGMA cache_size=10`, `PRAGMA synchronous=EXTRA`, `BEGIN IMMEDIATE`, `UPDATE metadata SET metadata=zeroblob(1048576)`} {
			require.NoError(t, sqlitex.Execute(c, q, nil))
		}
		os.Exit(23)
	}
	root := t.TempDir()
	db := filepath.Join(root, "metadata.sq3")
	c, err := sqlite.OpenConn(db, sqlite.OpenReadWrite|sqlite.OpenCreate)
	require.NoError(t, err)
	require.NoError(t, sqlitex.Execute(c, file.SchemaMigrations()[0], nil))
	require.NoError(t, sqlitex.Execute(c, `PRAGMA user_version=1`, nil))
	require.NoError(t, sqlitex.Execute(c, `INSERT INTO metadata(kind,namespace,name,metadata) VALUES('sbomsyft','ns','x','{}')`, nil))
	require.NoError(t, c.Close())
	cmd := exec.Command(os.Args[0], "-test.run=^TestCommandDryRunHotRollbackJournal$")
	cmd.Env = append(os.Environ(), "PREFIX_HOT_DB="+db)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, "%s", out)
	require.Equal(t, 23, exit.ExitCode())
	require.FileExists(t, db+"-journal")
	before := snapshotSource(t, root)
	require.NoError(t, run([]string{"-root", root, "-direction", "forward", "-dry-run"}, &bytes.Buffer{}))
	require.Equal(t, before, snapshotSource(t, root))
}

func TestCommandRejectsUnsafeAndUnknownStores(t *testing.T) {
	for _, scenario := range []string{"mismatch", "symlink", "sidecar-symlink", "unknown-version", "malformed-schema"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			db := filepath.Join(root, "metadata.sq3")
			c, err := sqlite.OpenConn(db)
			require.NoError(t, err)
			query := file.SchemaMigrations()[0]
			if scenario == "malformed-schema" {
				query = `CREATE TABLE metadata(kind TEXT, namespace TEXT, name TEXT, metadata JSON)`
			}
			require.NoError(t, sqlitex.Execute(c, query, nil))
			require.NoError(t, sqlitex.Execute(c, `PRAGMA user_version=1`, nil))
			if scenario == "unknown-version" {
				require.NoError(t, sqlitex.Execute(c, `PRAGMA user_version=99`, nil))
			}
			require.NoError(t, c.Close())
			if scenario == "symlink" {
				require.NoError(t, os.Rename(db, db+".real"))
				require.NoError(t, os.Symlink(db+".real", db))
			}
			if scenario == "sidecar-symlink" {
				require.NoError(t, os.Symlink(db, db+"-wal"))
			}
			if scenario == "mismatch" {
				root = t.TempDir()
			}
			before := snapshotSource(t, root)
			require.Error(t, run([]string{"-root", root, "-db", db, "-direction", "forward", "-dry-run"}, &bytes.Buffer{}))
			require.Equal(t, before, snapshotSource(t, root))
		})
	}
}
