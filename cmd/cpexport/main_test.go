package main

// cpexport is driven as the operator would drive it: the real built binary,
// a real root directory and database, exit codes and the report line. The
// export's semantics per row shape are pinned in
// pkg/registry/file/sqliteobject_export_test.go; the scale run is
// TestExport_Scale_BinaryAgainstMigratedCorpus there.

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/uuid"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cpexport-bin")
		if err != nil {
			binErr = err
			return
		}
		bin := filepath.Join(dir, "cpexport")
		cmd := exec.Command("go", "build", "-o", bin, ".")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			binErr = fmt.Errorf("%w: %s", err, stderr.String())
			return
		}
		binPath = bin
	})
	require.NoError(t, binErr, "go build cmd/cpexport")
	return binPath
}

var reportRE = regexp.MustCompile(`cpexport: exported=(\d+) legacySkipped=(\d+) undecodable=(\d+) dryRun=(true|false) elapsed=`)

type result struct {
	code                                 int
	exported, legacySkipped, undecodable int
	dryRun                               bool
	stdout, stderr                       string
}

// run drives the binary with a bound: the export of a few rows is
// sub-second, so a run that takes longer is a hang (the pool retrying an
// unopenable database), not a slow machine.
func run(t *testing.T, bin string, args ...string) result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	require.NoError(t, ctx.Err(), "cpexport did not exit within the bound (hung): args=%v", args)
	r := result{stdout: stdout.String(), stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		r.code = exitErr.ExitCode()
	default:
		t.Fatalf("run cpexport: %v", err)
	}
	if m := reportRE.FindStringSubmatch(r.stdout); m != nil {
		r.exported, _ = strconv.Atoi(m[1])
		r.legacySkipped, _ = strconv.Atoi(m[2])
		r.undecodable, _ = strconv.Atoi(m[3])
		r.dryRun = m[4] == "true"
	}
	return r
}

const prefix = "/spdx.softwarecomposition.kubescape.io/containerprofile/"

// migratedRoot seeds n ContainerProfiles through the legacy StorageImpl over
// a real root, migrates them into the ObjectStore schema, and returns the
// root, the database path and the objects by name — with every legacy file
// removed, so the export has something to do.
func migratedRoot(t *testing.T, n int) (root, dbPath string, objs map[string]*softwarecomposition.ContainerProfile) {
	t.Helper()
	root = filepath.Join(t.TempDir(), "data")
	require.NoError(t, os.MkdirAll(root, 0755))
	dbPath = filepath.Join(root, "metadata.sq3")
	sch := runtime.NewScheme()
	install.Install(sch)
	pool := file.NewPoolWithOptions(dbPath, file.PoolOptions{Size: 4, BusyTimeout: 5 * time.Second})
	fs := afero.NewOsFs()
	legacy := file.NewStorageImpl(fs, root, pool, file.NewWatchDispatcher(), sch)
	ctx := context.Background()
	objs = map[string]*softwarecomposition.ContainerProfile{}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("profile-%03d", i)
		p := &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns", UID: uuid.NewUUID(),
				Labels: map[string]string{"n": strconv.Itoa(i)}},
			Spec: softwarecomposition.ContainerProfileSpec{Execs: []softwarecomposition.ExecCalls{{Path: "/bin/" + name}}},
		}
		out := &softwarecomposition.ContainerProfile{}
		require.NoError(t, legacy.Create(ctx, prefix+"ns/"+name, p, out, 0))
		objs[name] = out
	}
	gate, err := file.NewWriteGate(ctx, pool)
	require.NoError(t, err)
	report, err := file.MigrateContainerProfiles(ctx, pool, gate, fs, root, sch, file.ContainerProfileMigrationOptions{})
	require.NoError(t, err)
	require.Equal(t, n, report.Count(file.MigrationShapeMigrated), "%v", report.Counts)
	require.NoError(t, gate.Close())
	require.NoError(t, pool.Close())
	for name := range objs {
		require.NoError(t, os.Remove(payloadPath(root, name)))
	}
	return root, dbPath, objs
}

func payloadPath(root, name string) string {
	return filepath.Join(root, prefix, "ns", name) + file.GobExt
}

func decodeFile(t *testing.T, path string) *softwarecomposition.ContainerProfile {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	obj := &softwarecomposition.ContainerProfile{}
	require.NoError(t, gob.NewDecoder(bytes.NewReader(b)).Decode(obj))
	return obj
}

func payloadFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && file.IsPayloadFile(path) {
			n++
		}
		return err
	}))
	return n
}

func TestCpexport_DryRunThenReal(t *testing.T) {
	bin := buildBinary(t)
	root, dbPath, objs := migratedRoot(t, 30)
	require.Equal(t, 0, payloadFiles(t, root))

	dry := run(t, bin, "-root", root, "-db", dbPath, "-dry-run")
	require.Equal(t, 0, dry.code, "stderr: %s", dry.stderr)
	require.True(t, dry.dryRun, dry.stdout)
	require.Equal(t, 30, dry.exported, dry.stdout)
	require.Equal(t, 0, dry.legacySkipped)
	require.Equal(t, 0, dry.undecodable)
	require.Equal(t, 0, payloadFiles(t, root), "dry-run wrote nothing")

	// The default -db is <root>/metadata.sq3.
	real := run(t, bin, "-root", root)
	require.Equal(t, 0, real.code, "stderr: %s", real.stderr)
	require.False(t, real.dryRun)
	require.Equal(t, 30, real.exported, real.stdout)
	require.Equal(t, 30, payloadFiles(t, root))
	for name, want := range objs {
		got := decodeFile(t, payloadPath(root, name))
		require.Equal(t, want.Name, got.Name)
		require.Equal(t, want.UID, got.UID)
		require.Equal(t, want.ResourceVersion, got.ResourceVersion)
		require.Equal(t, want.Labels, got.Labels)
		require.Equal(t, want.Spec.Execs, got.Spec.Execs)
		_, err := os.Stat(payloadPath(root, name) + ".t")
		require.True(t, os.IsNotExist(err), "no staging file left for %s", name)
	}

	// Idempotent: a second export rewrites the same files.
	again := run(t, bin, "-root", root, "-db", dbPath, "-batch", "7")
	require.Equal(t, 0, again.code)
	require.Equal(t, 30, again.exported)
	require.Equal(t, 30, payloadFiles(t, root))
}

// An undecodable payload is skipped and reported, every other row is still
// exported, and the exit code is 2 so an operator's script notices.
func TestCpexport_UndecodableRowExits2(t *testing.T) {
	bin := buildBinary(t)
	root, dbPath, objs := migratedRoot(t, 5)
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite)
	require.NoError(t, err)
	require.NoError(t, sqlitex.Execute(conn,
		`UPDATE payloads SET body = ? WHERE kind = ? AND namespace = ? AND name = ?`,
		&sqlitex.ExecOptions{Args: []any{[]byte("{not json"), file.ContainerProfileKind, "ns", "profile-002"}}))
	require.Equal(t, 1, conn.Changes())
	require.NoError(t, conn.Close())

	r := run(t, bin, "-root", root, "-db", dbPath)
	require.Equal(t, 2, r.code, "stdout: %s stderr: %s", r.stdout, r.stderr)
	require.Equal(t, 4, r.exported, r.stdout)
	require.Equal(t, 1, r.undecodable, r.stdout)
	require.Equal(t, 4, payloadFiles(t, root))
	_, err = os.Stat(payloadPath(root, "profile-002"))
	require.True(t, os.IsNotExist(err), "the undecodable row gets no file")
	for _, name := range []string{"profile-000", "profile-001", "profile-003", "profile-004"} {
		require.Equal(t, objs[name].UID, decodeFile(t, payloadPath(root, name)).UID)
	}
	// Dry-run reports the same and exits 2 as well.
	dry := run(t, bin, "-root", root, "-db", dbPath, "-dry-run")
	require.Equal(t, 2, dry.code)
	require.Equal(t, 1, dry.undecodable)
}

// A missing database (a wrong -db or -root) is exit 1 with the error on
// stderr and no report line — promptly. Without the up-front check the pool
// retries the open every 5 s until the binary's one-hour context expires.
func TestCpexport_MissingDatabaseExits1(t *testing.T) {
	bin := buildBinary(t)
	root := t.TempDir()
	for _, db := range []string{filepath.Join(root, "missing-dir", "metadata.sq3"), filepath.Join(root, "metadata.sq3")} {
		start := time.Now()
		r := run(t, bin, "-root", root, "-db", db)
		require.Equal(t, 1, r.code, "stdout: %s stderr: %s", r.stdout, r.stderr)
		require.Contains(t, r.stderr, "cpexport: database:")
		require.Empty(t, r.stdout)
		require.Less(t, time.Since(start), 5*time.Second, "must fail before the pool's first retry")
	}
	// The default -db (<root>/metadata.sq3) with an empty root: same.
	r := run(t, bin, "-root", root)
	require.Equal(t, 1, r.code)
	require.Contains(t, r.stderr, "cpexport: database:")
}
