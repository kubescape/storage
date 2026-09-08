package file

// Lane 0 / L0-B: panic containment in the shard commit loop (singlewriter.go).
//
// Three layers, each pinned here:
//
//   1. callGuarded around job.custom: a panic in caller-supplied code becomes
//      an error for that caller.
//   2. putChecked in place of `defer s.pool.Put(conn)`: a connection is never
//      returned to the pool with an open transaction/savepoint or a stepped,
//      unreset statement; the temp payload is removed on every non-committed
//      exit.
//   3. recover() in process(): a panic anywhere in commit() -- including
//      sqlitex.Save's own release path -- is delivered to the caller as an
//      error, counted under outcome=panic, and the shard goroutine survives.
//
// Fail-on-today protocol (plan A.9.1): before L0-B the package has no
// recover() anywhere, so every test below except the closure discriminator
// CRASHES the test binary rather than failing an assertion. Record the
// evidence one test per process (`go test -run '^TestName$'`), never in the
// same invocation as the rest of the suite. The assertions deliberately use
// only symbols that exist on pre-L0-B code so the file compiles there.
//
// Same conventions as singlewriter_test.go: the package-level flags are
// flipped directly with a cleanup, and no test uses t.Parallel().

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/component-base/metrics/testutil"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func resetSingleWriterCounters(t *testing.T) {
	t.Helper()
	metrics.SingleWriterCommitTotal.Reset()
	metrics.SingleWriterDirtyConnectionTotal.Reset()
	metrics.SingleWriterDroppedConnectionTotal.Reset()
}

func commitOutcomeCount(t *testing.T, priority, outcome string) float64 {
	t.Helper()
	v, err := testutil.GetCounterMetricValue(metrics.SingleWriterCommitTotal.WithLabelValues("containerprofiles", priority, outcome))
	require.NoError(t, err)
	return v
}

func dirtyDroppedCounts(t *testing.T) (dirty, dropped float64) {
	t.Helper()
	var err error
	dirty, err = testutil.GetCounterMetricValue(metrics.SingleWriterDirtyConnectionTotal)
	require.NoError(t, err)
	dropped, err = testutil.GetCounterMetricValue(metrics.SingleWriterDroppedConnectionTotal)
	require.NoError(t, err)
	return dirty, dropped
}

// requireShardServes asserts the shard owning key still takes jobs: a no-op
// priorityHigh probe on that key completes well inside lockTimeout.
func requireShardServes(t *testing.T, si *StorageImpl, key string) {
	t.Helper()
	probeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := si.ensureWriter().runOnShard(probeCtx, key, priorityHigh, func(*sqlite.Conn) error { return nil })
	require.NoError(t, err, "shard must keep serving after a recovered panic")
}

// requireLockFree asserts the per-key lock was released on the panic path.
func requireLockFree(t *testing.T, si *StorageImpl, key string) {
	t.Helper()
	lockCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, si.locks.Lock(lockCtx, key), "per-key lock must be free after a recovered panic")
	si.locks.Unlock(key)
}

// requirePoolConnClean, on a pool of size 1, asserts the shard's connection
// came back (Take does not time out) and came back clean: autocommit on, no
// statement mid-step.
func requirePoolConnClean(t *testing.T, si *StorageImpl) {
	t.Helper()
	poolCtx, cancel := poolContext()
	defer cancel()
	conn, err := si.pool.Take(poolCtx)
	require.NoError(t, err, "the shard's connection must be back in the pool")
	defer si.pool.Put(conn)
	require.True(t, conn.AutocommitEnabled(), "returned connection must not have an open transaction")
	require.Empty(t, conn.CheckReset(), "returned connection must not have a stepped statement")
}

// tempPayloadFiles lists every temp payload (nextTempPath's ".t.<ns>.<seq>"
// infix) left under the storage root.
func tempPayloadFiles(t *testing.T, si *StorageImpl) []string {
	t.Helper()
	var found []string
	err := afero.Walk(si.appFs, si.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.Contains(info.Name(), ".t.") {
			found = append(found, path)
		}
		return nil
	})
	require.NoError(t, err)
	return found
}

func newLane0Profile(name string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1"},
	}
}

// TestSingleWriter_CustomPanic_CallerGetsErrorShardSurvives pins layer 1: a
// panic in a runOnShard closure reaches its caller as an error (not a hang,
// not a dead process), the shard keeps serving, and neither the per-key lock
// nor the pool connection leaks. Crashes the binary before L0-B.
func TestSingleWriter_CustomPanic_CallerGetsErrorShardSurvives(t *testing.T) {
	enableSingleWriter(t)
	setSingleWriterShards(t, DefaultSingleWriterShards)
	si, cleanup := newSingleWriterTestStorageWithPoolSize(t, 1)
	t.Cleanup(cleanup)
	resetSingleWriterCounters(t)
	ctx := context.Background()
	key := testProfileKey("custom-panic")

	done := make(chan error, 1)
	go func() {
		done <- si.ensureWriter().runOnShard(ctx, key, priorityLow, func(*sqlite.Conn) error {
			panic("injected custom panic")
		})
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runOnShard hung after its closure panicked")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected custom panic")

	requireShardServes(t, si, key)
	requireLockFree(t, si, key)
	requirePoolConnClean(t, si)

	// Layer 1 converts the panic to an ordinary error: it is an error outcome
	// on the shard, not a panic outcome, and the connection was never dirty.
	require.Equal(t, float64(1), commitOutcomeCount(t, metrics.PriorityLow, "error"))
	require.Equal(t, float64(0), commitOutcomeCount(t, metrics.PriorityLow, "panic"))
	dirty, dropped := dirtyDroppedCounts(t)
	require.Equal(t, float64(0), dirty)
	require.Equal(t, float64(0), dropped)
}

// TestSingleWriter_CustomPanicWithSteppedStatement_ConnectionIsReset pins the
// CheckReset branch of layer 2: a closure that steps a cached statement and
// panics without resetting it would make Pool.Put panic ("connection returned
// to pool has active statement") -- a second panic during the unwind that
// replaces the first. putChecked resets the statement before Put. Crashes the
// binary before L0-B.
func TestSingleWriter_CustomPanicWithSteppedStatement_ConnectionIsReset(t *testing.T) {
	enableSingleWriter(t)
	setSingleWriterShards(t, DefaultSingleWriterShards)
	si, cleanup := newSingleWriterTestStorageWithPoolSize(t, 1)
	t.Cleanup(cleanup)
	resetSingleWriterCounters(t)
	ctx := context.Background()
	key := testProfileKey("stepped-panic")

	done := make(chan error, 1)
	go func() {
		done <- si.ensureWriter().runOnShard(ctx, key, priorityLow, func(conn *sqlite.Conn) error {
			stmt := conn.Prep("SELECT 1 UNION ALL SELECT 2;")
			if _, err := stmt.Step(); err != nil {
				return err
			}
			panic("injected panic with a stepped statement")
		})
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runOnShard hung after its closure panicked")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected panic with a stepped statement")

	requireShardServes(t, si, key)
	requireLockFree(t, si, key)
	requirePoolConnClean(t, si)

	dirty, dropped := dirtyDroppedCounts(t)
	require.Equal(t, float64(1), dirty, "the stepped statement must be detected on release")
	require.Equal(t, float64(0), dropped, "a stepped statement is reset, not dropped")
}

// TestSingleWriter_WriteMetadataPanic_ShardSurvivesConnectionRolledBack pins
// layers 2 and 3 together on the shipped defect: commit() calls release(&err)
// normally, not deferred, so a panic in writeMeta skips it and hands a
// connection with an open SAVEPOINT to the pool. After L0-B the caller gets
// an error, the panic is counted once under outcome=panic, the connection is
// rolled back (dirty=1, dropped=0) before it is returned, the temp payload is
// gone, and the same key can be created on the next attempt. Crashes the
// binary before L0-B.
func TestSingleWriter_WriteMetadataPanic_ShardSurvivesConnectionRolledBack(t *testing.T) {
	enableSingleWriter(t)
	setSingleWriterShards(t, DefaultSingleWriterShards)
	si, cleanup := newSingleWriterTestStorageWithPoolSize(t, 1)
	t.Cleanup(cleanup)
	resetSingleWriterCounters(t)
	ctx := context.Background()
	const name = "wm-panic"
	key := testProfileKey(name)

	var mu sync.Mutex
	armed := true
	si.writeMetadataFn = func(conn *sqlite.Conn, path string, metadata runtime.Object) error {
		mu.Lock()
		fire := armed
		armed = false
		mu.Unlock()
		if fire {
			panic("injected writeMetadata panic")
		}
		return writeMetadata(conn, path, metadata)
	}

	done := make(chan error, 1)
	go func() {
		done <- si.Create(ctx, key, newLane0Profile(name), &softwarecomposition.ContainerProfile{}, 0)
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Create hung after writeMetadata panicked")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "injected writeMetadata panic")

	// Counted before the probes below, which are commits of their own.
	require.Equal(t, float64(1), commitOutcomeCount(t, metrics.PriorityHigh, "panic"))
	require.Equal(t, float64(0), commitOutcomeCount(t, metrics.PriorityHigh, "committed"))
	dirty, dropped := dirtyDroppedCounts(t)
	require.Equal(t, float64(1), dirty, "the dangling savepoint must be detected on release")
	require.Equal(t, float64(0), dropped)

	requireShardServes(t, si, key)
	requireLockFree(t, si, key)
	requirePoolConnClean(t, si)
	require.Empty(t, tempPayloadFiles(t, si), "the temp payload must be removed on the panic path")

	// The shard, the lock, the connection and the key are all reusable.
	before := commitOutcomeCount(t, metrics.PriorityHigh, "committed")
	out := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Create(ctx, key, newLane0Profile(name), out, 0))
	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got))
	require.Equal(t, name, got.Name)
	require.Equal(t, before+1, commitOutcomeCount(t, metrics.PriorityHigh, "committed"))
	dirty, dropped = dirtyDroppedCounts(t)
	require.Equal(t, float64(1), dirty, "the retry must not find a dirty connection")
	require.Equal(t, float64(0), dropped)
}

// TestSingleWriter_ReleasePathPanic_ShardSurvives pins layer 3 on the panic
// callGuarded cannot reach: sqlitex.Save's release function panics when its
// ROLLBACK TO fails, from commit()'s own frame. writeMetadataFn ends the
// savepoint's transaction and opens a bare one, so release finds the
// connection in a transaction with no savepoint to release or roll back to.
// After L0-B the caller gets an error, the panic is counted, the leftover
// transaction is rolled back before the connection is returned, and the shard
// keeps serving. Crashes the binary before L0-B.
func TestSingleWriter_ReleasePathPanic_ShardSurvives(t *testing.T) {
	enableSingleWriter(t)
	setSingleWriterShards(t, DefaultSingleWriterShards)
	si, cleanup := newSingleWriterTestStorageWithPoolSize(t, 1)
	t.Cleanup(cleanup)
	resetSingleWriterCounters(t)
	ctx := context.Background()
	const name = "release-panic"
	key := testProfileKey(name)

	var mu sync.Mutex
	armed := true
	si.writeMetadataFn = func(conn *sqlite.Conn, path string, metadata runtime.Object) error {
		mu.Lock()
		fire := armed
		armed = false
		mu.Unlock()
		if !fire {
			return writeMetadata(conn, path, metadata)
		}
		if err := sqlitex.Execute(conn, "ROLLBACK;", nil); err != nil {
			return fmt.Errorf("test: rollback: %w", err)
		}
		if err := sqlitex.Execute(conn, "BEGIN;", nil); err != nil {
			return fmt.Errorf("test: begin: %w", err)
		}
		return nil
	}

	done := make(chan error, 1)
	go func() {
		done <- si.Create(ctx, key, newLane0Profile(name), &softwarecomposition.ContainerProfile{}, 0)
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Create hung after the release path panicked")
	}
	require.Error(t, err)
	require.Contains(t, err.Error(), "no such savepoint")

	require.Equal(t, float64(1), commitOutcomeCount(t, metrics.PriorityHigh, "panic"))
	dirty, dropped := dirtyDroppedCounts(t)
	require.Equal(t, float64(1), dirty, "the bare transaction must be detected on release")
	require.Equal(t, float64(0), dropped)

	requireShardServes(t, si, key)
	requireLockFree(t, si, key)
	requirePoolConnClean(t, si)
	require.Empty(t, tempPayloadFiles(t, si))

	// The renamed payload from the first attempt (rename ran before release
	// panicked) does not stop a retry: the metadata row was never written,
	// so the key does not exist and Create's commit-time check passes.
	_ = si.appFs.Remove(makePayloadPath(filepath.Join(si.root, key)))
	require.NoError(t, si.Create(ctx, key, newLane0Profile(name), &softwarecomposition.ContainerProfile{}, 0))
}

// removeRecordingFs records every Remove so a test can assert one did NOT
// happen.
type removeRecordingFs struct {
	afero.Fs
	mu      sync.Mutex
	removed []string
}

func (f *removeRecordingFs) Remove(name string) error {
	f.mu.Lock()
	f.removed = append(f.removed, name)
	f.mu.Unlock()
	return f.Fs.Remove(name)
}

// TestSingleWriter_SuccessfulCommit_DoesNotRemoveRenamedPayload discriminates
// the argument-at-defer-time bug in layer 2: `defer putChecked(conn,
// committed, job)` would capture committed=false at registration and Remove
// the (already renamed) temp path after every successful commit. The defer
// must be a closure reading committed at execution time. Passes today (no
// Remove at all on the success path) and must keep passing after L0-B.
func TestSingleWriter_SuccessfulCommit_DoesNotRemoveRenamedPayload(t *testing.T) {
	enableSingleWriter(t)
	setSingleWriterShards(t, DefaultSingleWriterShards)
	si, cleanup := newSingleWriterTestStorage(t)
	t.Cleanup(cleanup)
	rfs := &removeRecordingFs{Fs: si.appFs}
	si.appFs = rfs
	ctx := context.Background()
	const name = "no-remove-on-commit"
	key := testProfileKey(name)

	require.NoError(t, si.Create(ctx, key, newLane0Profile(name), &softwarecomposition.ContainerProfile{}, 0))

	rfs.mu.Lock()
	defer rfs.mu.Unlock()
	for _, p := range rfs.removed {
		require.NotContains(t, p, ".t.", "a successful commit must not Remove its temp payload path")
	}
}
