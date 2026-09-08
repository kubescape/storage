package file

// The write gate of the SQLite-native ContainerProfile backend (ObjectStore):
// a caller-side two-lane FIFO ticket semaphore owning ONE dedicated write
// connection (design: .omc/plans/full-acid-storage-architecture.md §6.4).
//
// Acquisition order, fixed: prepare (pool connection for reads, released) →
// gate ticket (queued on ctx) → BEGIN IMMEDIATE on the gate's connection →
// statements → COMMIT → release ticket → dispatch. No pool connection is held
// while queued on the gate and no ticket is held while waiting on the pool.
//
// Not a sync.Mutex: Go's mutex is not FIFO and cannot be abandoned on ctx
// cancellation. The releaser hands the ticket directly to the next waiter
// (high lane first, a waiting low job forced through after highBurstLimit
// consecutive high commits — the policy of singleWriter.run), so fairness is
// FIFO among gated writers. A waiter whose ctx fires after the ticket was
// granted hands the ticket back (INV-5): a leaked ticket would wedge every
// writer forever.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/metrics"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// errGateClosed is returned to writers queued on, or arriving at, a gate whose
// Close has begun.
var errGateClosed = errors.New("write gate: closed")

// ErrWriteConflict is the exported alias of the single writer's conflict
// sentinel: an ObjectStore compare-and-swap (UPDATE/DELETE … WHERE rv=:rv AND
// uid=:uid) matched no row because another write committed first. Callers
// re-read and retry; the consolidation pass retries once.
var ErrWriteConflict = errWriteConflict

type gateWaiter struct {
	ch       chan struct{}
	priority writePriority
	// granted is set under writeGate.mu at the instant the ticket is handed
	// over; a cancelled waiter that finds it set owns the gate and must release.
	granted bool
	// rejected is set under writeGate.mu when Close drains the queue.
	rejected bool
}

type writeGate struct {
	pool *sqlitemigration.Pool

	mu         sync.Mutex
	idle       *sync.Cond
	busy       bool
	high, low  []*gateWaiter
	highStreak int
	closed     bool
	// conn is the dedicated write connection, taken once from the pool and
	// returned only by Close (K-5: sqlitex.Pool.Close blocks until every
	// connection is back).
	conn *sqlite.Conn
	// replaced counts connections that could not be cleaned after a failed
	// transaction and were replaced from the pool.
	replaced int
}

func newWriteGate(ctx context.Context, pool *sqlitemigration.Pool) (*writeGate, error) {
	conn, err := pool.Take(ctx)
	if err != nil {
		return nil, fmt.Errorf("write gate: take dedicated connection: %w", err)
	}
	// Pool.Take bound the interrupt to ctx; the connection outlives it.
	conn.SetInterrupt(nil)
	g := &writeGate{pool: pool, conn: conn}
	g.idle = sync.NewCond(&g.mu)
	return g, nil
}

// acquire blocks until the caller owns the gate or ctx is done. On a nil
// return the caller MUST call release.
func (g *writeGate) acquire(ctx context.Context, priority writePriority) error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return errGateClosed
	}
	if !g.busy {
		g.busy = true
		g.mu.Unlock()
		metrics.ObserveWriteGateWait(priority.label(), 0)
		return nil
	}
	w := &gateWaiter{ch: make(chan struct{}, 1), priority: priority}
	if priority == priorityHigh {
		g.high = append(g.high, w)
	} else {
		g.low = append(g.low, w)
	}
	g.mu.Unlock()

	start := time.Now()
	select {
	case <-w.ch:
		metrics.ObserveWriteGateWait(priority.label(), time.Since(start))
		if w.rejected {
			return errGateClosed
		}
		return nil
	case <-ctx.Done():
		g.mu.Lock()
		if w.granted {
			// The ticket arrived in the same instant: we own the gate. Hand it
			// back instead of leaking it (INV-5).
			g.mu.Unlock()
			<-w.ch
			g.release()
			return ctx.Err()
		}
		g.removeWaiter(w)
		g.mu.Unlock()
		return ctx.Err()
	}
}

// removeWaiter removes w from its lane; caller holds g.mu.
func (g *writeGate) removeWaiter(w *gateWaiter) {
	lane := &g.low
	if w.priority == priorityHigh {
		lane = &g.high
	}
	for i, x := range *lane {
		if x == w {
			*lane = append((*lane)[:i], (*lane)[i+1:]...)
			return
		}
	}
}

// pickNext implements the two-lane policy of singleWriter.run: high first,
// unless highBurstLimit consecutive high commits have run while a low job
// waited, in which case the low job goes through. Caller holds g.mu.
func (g *writeGate) pickNext() *gateWaiter {
	if g.highStreak < highBurstLimit && len(g.high) > 0 {
		w := g.high[0]
		g.high = g.high[1:]
		g.highStreak++
		return w
	}
	if len(g.low) > 0 {
		w := g.low[0]
		g.low = g.low[1:]
		g.highStreak = 0
		return w
	}
	if len(g.high) > 0 {
		w := g.high[0]
		g.high = g.high[1:]
		g.highStreak++
		return w
	}
	return nil
}

// release hands the gate to the next queued writer, or marks it idle.
func (g *writeGate) release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if next := g.pickNext(); next != nil {
		next.granted = true
		next.ch <- struct{}{}
		return
	}
	g.busy = false
	g.idle.Broadcast()
}

// queued reports how many writers are waiting, for tests and gauges.
func (g *writeGate) queued() (high, low int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.high), len(g.low)
}

// held reports whether some writer currently owns the gate.
func (g *writeGate) held() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.busy
}

// run executes fn inside BEGIN IMMEDIATE … COMMIT on the gate's connection,
// holding the gate for exactly that span. fn must contain only SQL on
// already-prepared bytes (INV-1). A non-nil error from fn rolls the
// transaction back and is returned; a panic in fn is recovered, rolled back,
// counted under CommitOutcomePanic and returned as an error (L0-B).
func (g *writeGate) run(ctx context.Context, priority writePriority, path string, fn func(conn *sqlite.Conn) error) (err error) {
	if err := g.acquire(ctx, priority); err != nil {
		return err
	}
	defer g.release()

	conn := g.conn
	start := time.Now()
	defer func() { metrics.ObserveSqliteWriteHold(path, time.Since(start)) }()

	defer func() {
		if r := recover(); r != nil {
			logger.L().Error("ObjectStore: panic inside the gated transaction, rolled back",
				helpers.String("path", path), helpers.Interface("panic", r), helpers.String("stack", string(debug.Stack())))
			metrics.IncSingleWriterCommit(ContainerProfileKindPlural, priority.label(), metrics.CommitOutcomePanic)
			g.cleanOrReplace(conn)
			if perr, ok := r.(error); ok {
				err = fmt.Errorf("ObjectStore: panic during %s transaction: %w", path, perr)
			} else {
				err = fmt.Errorf("ObjectStore: panic during %s transaction: %v", path, r)
			}
		}
	}()

	beforeBegin := time.Now()
	endFn, err := sqlitex.ImmediateTransaction(conn)
	metrics.ObserveSqliteBusyWait(time.Since(beforeBegin))
	if err != nil {
		g.cleanOrReplace(conn)
		return fmt.Errorf("BEGIN IMMEDIATE: %w", err)
	}
	err = fn(conn)
	endFn(&err)
	if err != nil {
		g.cleanOrReplace(conn)
		if errors.Is(err, errWriteConflict) {
			metrics.IncSingleWriterCommit(ContainerProfileKindPlural, priority.label(), metrics.CommitOutcomeConflict)
		} else {
			metrics.IncSingleWriterCommit(ContainerProfileKindPlural, priority.label(), metrics.CommitOutcomeError)
		}
		return err
	}
	metrics.IncSingleWriterCommit(ContainerProfileKindPlural, priority.label(), metrics.CommitOutcomeCommitted)
	return nil
}

// cleanOrReplace makes sure the gate's connection is out of any transaction
// after a failure. A connection that cannot be cleaned is replaced from the
// pool, never dropped: the gate must never end up without a connection.
// Caller owns the gate.
func (g *writeGate) cleanOrReplace(conn *sqlite.Conn) {
	if conn.AutocommitEnabled() {
		return
	}
	_ = sqlitex.ExecuteTransient(conn, "ROLLBACK", nil)
	if conn.AutocommitEnabled() {
		return
	}
	logger.L().Error("ObjectStore: write connection could not be cleaned, replacing it from the pool")
	ctx, cancel := context.WithTimeout(context.Background(), poolTimeout)
	defer cancel()
	fresh, err := g.pool.Take(ctx)
	if err != nil {
		logger.L().Error("ObjectStore: could not take a replacement write connection; keeping the dirty one", helpers.Error(err))
		return
	}
	fresh.SetInterrupt(nil)
	// The dirty connection is closed, not returned: a mid-transaction
	// connection in the pool would poison whoever takes it next. This leaves
	// the pool one short for Pool.Close (the K-5 note on Put(nil)).
	_ = conn.Close()
	g.mu.Lock()
	g.conn = fresh
	g.replaced++
	g.mu.Unlock()
}

// Close stops admitting writers, waits for the current holder to finish, and
// returns the dedicated connection to the pool so that Pool.Close can
// complete (K-5). Idempotent.
func (g *writeGate) Close() error {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return nil
	}
	g.closed = true
	for _, w := range append(g.high, g.low...) {
		w.rejected = true
		w.ch <- struct{}{}
	}
	g.high, g.low = nil, nil
	for g.busy {
		g.idle.Wait()
	}
	conn := g.conn
	g.conn = nil
	g.mu.Unlock()
	if conn != nil {
		g.pool.Put(conn)
	}
	return nil
}
