package file

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// TestCheckpointer_WalGrowthTriggersPassiveCheckpoint: with autocheckpoint off
// on every connection, a write burst grows the WAL past the threshold; the
// size check after a gated commit kicks the checkpointer, which runs PASSIVE on
// its own pooled connection and leaves no frame un-checkpointed.
func TestCheckpointer_WalGrowthTriggersPassiveCheckpoint(t *testing.T) {
	e := newObjectStoreEnv(t, withCheckpoint(64*1024, time.Hour))
	c := e.store.checkpointer
	require.Equal(t, int64(0), c.runs.Load())

	// ~40 KB of spec per object, several objects: well past 64 KB of WAL.
	for i := 0; i < 8; i++ {
		p := e.plain(fmt.Sprintf("wal-%d", i))
		p.Spec.Syscalls = append(p.Spec.Syscalls, strings.Repeat("x", 5000), strings.Repeat("y", 5000))
		e.create(p)
	}
	st, err := os.Stat(e.dbPath + "-wal")
	require.NoError(t, err)
	assert.Greater(t, st.Size(), int64(64*1024), "the WAL grew past the threshold (autocheckpoint is off)")

	require.Eventually(t, func() bool { return c.runs.Load() > 0 }, 5*time.Second, 10*time.Millisecond, "the size check did not kick the checkpointer")
	assert.Equal(t, int64(0), c.restarts.Load())

	// Nothing left to checkpoint: a PASSIVE run from the test reports
	// checkpointed == log frames.
	require.Eventually(t, func() bool {
		var busy, logFrames, ckpt int64
		e.withConn(func(conn *sqlite.Conn) {
			require.NoError(t, sqlitex.ExecuteTransient(conn, `PRAGMA wal_checkpoint(PASSIVE)`, &sqlitex.ExecOptions{
				ResultFunc: func(stmt *sqlite.Stmt) error {
					busy, logFrames, ckpt = stmt.ColumnInt64(0), stmt.ColumnInt64(1), stmt.ColumnInt64(2)
					return nil
				},
			}))
		})
		return busy == 0 && logFrames == ckpt
	}, 5*time.Second, 20*time.Millisecond)
	assert.Greater(t, c.walPages.Load(), int64(0), "wal pages gauge observed")
}

// TestCheckpointer_TimerFallback: WAL growth from writers that never touch the
// gate is still checkpointed, on the timer.
func TestCheckpointer_TimerFallback(t *testing.T) {
	e := newObjectStoreEnv(t, withCheckpoint(1<<40, 30*time.Millisecond))
	c := e.store.checkpointer
	// An ungated writer: WAL growth from a connection outside the gate (the
	// fixture handle; a pool connection here would be an AC-G1 violation).
	e.withFixture(func(conn *sqlite.Conn) {
		require.NoError(t, sqlitex.ExecuteTransient(conn, `INSERT INTO metadata (kind,namespace,name,metadata) VALUES ('other','n','x','{}')`, nil))
	})
	require.Eventually(t, func() bool { return c.runs.Load() >= 2 }, 5*time.Second, 5*time.Millisecond)
}

// TestCheckpointer_SupervisedRestartAfterPanic: a panic in the checkpointer
// goroutine is recovered, counted, and the loop restarted; the next kick runs.
func TestCheckpointer_SupervisedRestartAfterPanic(t *testing.T) {
	e := newObjectStoreEnv(t, withCheckpoint(1, time.Hour))
	c := e.store.checkpointer
	var fired bool
	c.beforeCheckpoint = func() {
		if !fired {
			fired = true
			panic("checkpointer test panic")
		}
	}
	e.create(e.plain("cp-1")) // WAL > 1 byte → kick → panic → restart
	require.Eventually(t, func() bool { return c.restarts.Load() == 1 }, 5*time.Second, 5*time.Millisecond)
	e.create(e.plain("cp-2")) // kick again → the restarted loop serves it
	// The panicked run never reached runs.Add; the restarted loop's run does.
	require.Eventually(t, func() bool { return c.runs.Load() >= 1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, int64(1), c.restarts.Load())
}
