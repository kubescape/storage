package file

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

func newTestGate(t *testing.T, poolSize int) (*writeGate, func()) {
	t.Helper()
	pool := NewPoolWithOptions(t.TempDir()+"/gate.sq3", PoolOptions{Size: poolSize, DisableAutoCheckpoint: true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	g, err := newWriteGate(ctx, pool)
	require.NoError(t, err)
	return g, func() {
		require.NoError(t, g.Close())
		done := make(chan error, 1)
		go func() { done <- pool.Close() }()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("pool.Close hung: the gate did not return its connection (K-5)")
		}
	}
}

// TestWriteGate_FIFOWithinLane: the holder hands the ticket to waiters in
// enqueue order.
func TestWriteGate_FIFOWithinLane(t *testing.T) {
	g, done := newTestGate(t, 2)
	defer done()
	require.NoError(t, g.acquire(context.Background(), priorityHigh))

	const n = 8
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			require.NoError(t, g.acquire(context.Background(), priorityHigh))
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			g.release()
		}(i)
		// Enqueue deterministically: wait until this waiter is queued.
		require.Eventually(t, func() bool { h, _ := g.queued(); return h == i+1 }, 5*time.Second, time.Millisecond)
	}
	g.release()
	wg.Wait()
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	assert.Equal(t, want, order)
}

// TestWriteGate_HighBurstLimitForcesLow: with a low job queued, at most
// highBurstLimit consecutive high jobs run before it (singleWriter.run's
// policy, re-hosted).
func TestWriteGate_HighBurstLimitForcesLow(t *testing.T) {
	g, done := newTestGate(t, 2)
	defer done()
	require.NoError(t, g.acquire(context.Background(), priorityHigh))

	var order []string
	var mu sync.Mutex
	var wg sync.WaitGroup
	enqueue := func(label string, p writePriority, expectHigh, expectLow int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, g.acquire(context.Background(), p))
			mu.Lock()
			order = append(order, label)
			mu.Unlock()
			g.release()
		}()
		require.Eventually(t, func() bool { h, l := g.queued(); return h == expectHigh && l == expectLow }, 5*time.Second, time.Millisecond)
	}
	enqueue("low", priorityLow, 0, 1)
	for i := 0; i < highBurstLimit+5; i++ {
		enqueue("high", priorityHigh, i+1, 1)
	}
	g.release()
	wg.Wait()
	lowAt := -1
	for i, l := range order {
		if l == "low" {
			lowAt = i
		}
	}
	assert.Equal(t, highBurstLimit, lowAt, "the low job runs after exactly highBurstLimit high jobs")
}

// TestWriteGate_INV5_NoTicketLeakUnderCancellation cancels callers at every
// point of the hand-off — before enqueue, while queued, at the instant of
// grant, and while holding — under a race-heavy schedule, and asserts the
// gate always drains to idle and a fresh writer acquires promptly.
func TestWriteGate_INV5_NoTicketLeakUnderCancellation(t *testing.T) {
	g, done := newTestGate(t, 2)
	defer done()
	rng := rand.New(rand.NewSource(1))

	for round := 0; round < 40; round++ {
		var wg sync.WaitGroup
		var granted atomic.Int64
		for i := 0; i < 24; i++ {
			wg.Add(1)
			// draw on the test goroutine: math/rand.Rand is not goroutine-safe
			timeout := time.Duration(rng.Intn(600)) * time.Microsecond
			hold := time.Duration(rng.Intn(50)) * time.Microsecond
			p := priorityHigh
			if i%3 == 0 {
				p = priorityLow
			}
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				defer cancel()
				if err := g.acquire(ctx, p); err != nil {
					return
				}
				granted.Add(1)
				// hold briefly; sometimes our own ctx has already fired
				time.Sleep(hold)
				g.release()
			}()
		}
		// The instant-of-grant race: a holder releases while a waiter's ctx
		// expires; the waiter must either run or hand the ticket back.
		wg.Wait()
		require.Eventually(t, func() bool {
			h, l := g.queued()
			return !g.held() && h == 0 && l == 0
		}, 5*time.Second, 100*time.Microsecond, "round %d: gate did not drain (in-flight ticket leaked)", round)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		require.NoError(t, g.acquire(ctx, priorityHigh), "round %d: fresh writer could not acquire", round)
		cancel()
		g.release()
	}
}

// TestWriteGate_GrantAtCancelInstantHandsBack pins the exact hand-off race:
// the waiter's ctx is already done when the holder grants it the ticket.
func TestWriteGate_GrantAtCancelInstantHandsBack(t *testing.T) {
	g, done := newTestGate(t, 2)
	defer done()
	for i := 0; i < 500; i++ {
		require.NoError(t, g.acquire(context.Background(), priorityHigh))
		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() { errCh <- g.acquire(ctx, priorityHigh) }()
		require.Eventually(t, func() bool { h, _ := g.queued(); return h == 1 }, 5*time.Second, 10*time.Microsecond)
		// Cancel and release "simultaneously".
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); cancel() }()
		go func() { defer wg.Done(); g.release() }()
		wg.Wait()
		err := <-errCh
		if err == nil {
			g.release()
		} else {
			assert.ErrorIs(t, err, context.Canceled)
		}
		require.Eventually(t, func() bool { return !g.held() }, 2*time.Second, 10*time.Microsecond, "iteration %d leaked the ticket", i)
	}
}

// TestWriteGate_CloseRejectsWaiters: Close stops admitting, wakes queued
// writers with errGateClosed, waits for the holder and returns the connection
// (the deferred pool.Close proves K-5).
func TestWriteGate_CloseRejectsWaiters(t *testing.T) {
	g, done := newTestGate(t, 2)
	require.NoError(t, g.acquire(context.Background(), priorityHigh))
	waiterErr := make(chan error, 1)
	go func() { waiterErr <- g.acquire(context.Background(), priorityLow) }()
	require.Eventually(t, func() bool { _, l := g.queued(); return l == 1 }, 5*time.Second, time.Millisecond)

	closed := make(chan struct{})
	go func() { _ = g.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("Close returned while the gate was still held")
	case <-time.After(50 * time.Millisecond):
	}
	assert.ErrorIs(t, <-waiterErr, errGateClosed)
	g.release()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return after the holder released")
	}
	assert.ErrorIs(t, g.acquire(context.Background(), priorityHigh), errGateClosed)
	done()
}

// TestWriteGate_RunPanicContainment: a panic inside the transaction body is
// recovered, rolled back, and leaves the connection clean for the next writer.
func TestWriteGate_RunPanicContainment(t *testing.T) {
	g, done := newTestGate(t, 2)
	defer done()
	err := g.run(context.Background(), priorityHigh, "test", func(conn *sqlite.Conn) error {
		require.NoError(t, sqlitex.ExecuteTransient(conn, `INSERT INTO metadata (kind,namespace,name,metadata) VALUES ('k','n','panic','{}')`, nil))
		panic("boom")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic")
	assert.True(t, g.conn.AutocommitEnabled(), "connection left inside a transaction")
	assert.False(t, g.held())

	var n int64
	require.NoError(t, g.run(context.Background(), priorityHigh, "test", func(conn *sqlite.Conn) error {
		return sqlitex.ExecuteTransient(conn, `SELECT count(*) FROM metadata WHERE name='panic'`, &sqlitex.ExecOptions{
			ResultFunc: func(stmt *sqlite.Stmt) error { n = stmt.ColumnInt64(0); return nil },
		})
	}))
	assert.Equal(t, int64(0), n, "the panicked transaction must have been rolled back")

	err = g.run(context.Background(), priorityHigh, "test", func(*sqlite.Conn) error { return errors.New("no") })
	assert.EqualError(t, err, "no")
	assert.True(t, g.conn.AutocommitEnabled())
}
