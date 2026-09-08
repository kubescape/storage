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
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/metrics"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/metrics/testutil"
	"zombiezen.com/go/sqlite"
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
