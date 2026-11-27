// resourceprefixmigration renames persistent resource prefixes with storage stopped.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("resourceprefixmigration", flag.ContinueOnError)
	flags.SetOutput(out)
	direction := flags.String("direction", "", "required: forward or rollback; stop storage first")
	root := flags.String("root", file.DefaultStorageRoot, "storage root")
	db := flags.String("db", "", "database path (default <root>/metadata.sq3)")
	dry := flags.Bool("dry-run", false, "inspect without changing files or database")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if (*direction != "forward" && *direction != "rollback") || flags.NArg() != 0 {
		return fmt.Errorf("-direction=forward|rollback is required; unexpected positional arguments are refused")
	}
	if *db == "" {
		*db = filepath.Join(*root, "metadata.sq3")
	}
	if info, err := os.Lstat(*db); err != nil {
		return err
	} else if !info.Mode().IsRegular() {
		return fmt.Errorf("database is not a regular file: %s", *db)
	}
	expected, err := filepath.Abs(filepath.Join(*root, "metadata.sq3"))
	if err != nil {
		return err
	}
	actual, err := filepath.Abs(*db)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("database must be <root>/metadata.sq3")
	}
	resolved, err := filepath.EvalSymlinks(actual)
	if err != nil {
		return err
	}
	if resolved != actual {
		return fmt.Errorf("symlinked database or root is not supported")
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		info, err := os.Lstat(*db + suffix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe SQLite sidecar: %s", *db+suffix)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	opts := file.QualifiedResourceMigrationOptions{Direction: *direction, DryRun: *dry}
	inspectPath := *db
	if *dry {
		// Writers must be stopped. SQLite may recover or create sidecars only
		// in this private copy; opening the source read-only is not sufficient.
		dir, err := os.MkdirTemp("", "resourceprefix-inspect-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		inspectPath = filepath.Join(dir, "metadata.sq3")
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			info, err := os.Lstat(*db + suffix)
			if os.IsNotExist(err) && suffix != "" {
				continue
			}
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsafe SQLite file: %s", *db+suffix)
			}
			src, err := os.Open(*db + suffix)
			if err != nil {
				return err
			}
			dst, err := os.OpenFile(inspectPath+suffix, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				src.Close()
				return err
			}
			_, copyErr := io.Copy(dst, src)
			srcErr, dstErr := src.Close(), dst.Close()
			if err := errors.Join(copyErr, srcErr, dstErr); err != nil {
				return err
			}
		}
	}
	// Explicit flags preserve journal mode and never create a missing database.
	// Do not use the application's schema-migrating pool in this offline tool.
	pool, err := sqlitex.NewPool(inspectPath, sqlitex.PoolOptions{PoolSize: 1, Flags: sqlite.OpenReadWrite, PrepareConn: func(c *sqlite.Conn) error {
		return sqlitex.Execute(c, "PRAGMA synchronous=EXTRA", nil)
	}})
	if err != nil {
		return err
	}
	defer pool.Close()
	report, err := file.MigrateQualifiedResourcePrefixes(ctx, pool, nil, afero.NewOsFs(), *root, opts)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "resourceprefixmigration: direction=%s resources=%d dryRun=%t\n", *direction, report.Resources, report.DryRun)
	for _, detail := range report.Details {
		fmt.Fprintf(out, "%s -> %s state=%q rows=%v directory=%t required=%t\n", detail.From, detail.To, detail.State, detail.Rows, detail.Directory, detail.Required)
	}
	return nil
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
