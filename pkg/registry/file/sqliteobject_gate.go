package file

// The process's write gate: a caller-side two-lane FIFO ticket semaphore
// owning ONE dedicated write connection (design:
// .omc/plans/full-acid-storage-architecture.md §6.4, shared with the legacy
// kinds per .omc/plans/write-gate-sharing.md §3).
//
// Acquisition order, fixed: prepare (pool connection for reads, released) →
// gate ticket (queued on ctx) → BEGIN IMMEDIATE on the gate's connection →
// statements → COMMIT → release ticket → dispatch. No pool connection is held
// while queued on the gate on the hot paths, and no ticket is held while
// waiting on the pool.
//
// Not a sync.Mutex: Go's mutex is not FIFO and cannot be abandoned on ctx
// cancellation. The releaser hands the ticket directly to the next waiter
// (high lane first, a waiting low job forced through after highBurstLimit
// consecutive high commits — the policy of singleWriter.run), so fairness is
// FIFO among gated writers. A waiter whose ctx fires after the ticket was
// granted hands the ticket back (INV-5): a leaked ticket would wedge every
// writer forever.
//
// One gate per pool (writegate_registry.go): after R1 the gate is the only
// code that acquires SQLite's write lock; any other writer busy-waits against
// it for the whole busy timeout, invisible to the gate's own histograms.

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/metrics"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// errGateClosed is returned to writers queued on, or arriving at, a gate whose
// Close has begun.
var errGateClosed = errors.New("write gate: closed")

// errGateReentrant is returned when a gated fn tries to acquire the gate again
// through the ctx it was handed: the gate is not re-entrant, and the nested
// acquire would queue behind its own holder forever (INV-1′). In tests the
// same condition panics, so a site that swallows the error (`_ =
// DeleteMetadata(...)`) still fails loudly.
var errGateReentrant = errors.New("write gate: re-entrant acquire from inside a gated transaction")

// gateReentrantPanics makes a re-entrant acquire panic instead of returning
// errGateReentrant. Set by the package's TestMain; false in production.
var gateReentrantPanics atomic.Bool

// gateCtxKey marks the ctx a gated fn receives; valid only for the dynamic
// extent of fn (R-5): a site must not store it in anything that outlives the
// call, or a legitimate later write through the stored ctx is refused.
type gateCtxKey struct{}

// ErrWriteConflict is the exported alias of the single writer's conflict
// sentinel: an ObjectStore compare-and-swap (UPDATE/DELETE … WHERE rv=:rv AND
// uid=:uid) matched no row because another write committed first. Callers
// re-read and retry; the consolidation pass retries once.
var ErrWriteConflict = errWriteConflict

// Watchdog tunables (PM-G2). Package-level vars so tests can shrink them.
var (
	// gateWatchdogInterval is how often the watchdog samples the hold.
	gateWatchdogInterval = time.Second
	// gateWatchdogThreshold is the hold age past which the watchdog logs the
	// holder once, with every goroutine's stack: a leaked ticket, a re-entrant
	// acquire the ctx marker did not see, or a holder blocked on a PV that
	// stopped responding.
	gateWatchdogThreshold = 60 * time.Second
)

// WriteGate is the exported name of the process's write gate for main.go's
// wiring; everything else in this package uses writeGate.
type WriteGate = writeGate

// NewWriteGate builds the process's one write gate over pool. It is created
// beside the pool when config.ContainerProfileSqliteBackend is on and handed
// to the ObjectStore, the legacy StorageImpl and the cleanup handler; with the
// flag off no gate exists and every legacy write site runs today's code.
func NewWriteGate(ctx context.Context, pool *sqlitemigration.Pool) (*WriteGate, error) {
	return newWriteGate(ctx, pool)
}

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
	// holdStart/holdPath/holdKind describe the current holder (busy) for the
	// watchdog and the hold-age gauge.
	holdStart          time.Time
	holdPath, holdKind string
	// conn is the dedicated write connection, taken once from the pool and
	// returned only by Close (K-5: sqlitex.Pool.Close blocks until every
	// connection is back).
	conn *sqlite.Conn
	// owned is every connection this gate has EVER held — the initial one and
	// each replacement, never removed — mapped to the write-statement sequence
	// at which the gate took it. A write recorded on a connection the gate
	// held at the time is a gated write even after cleanOrReplace swapped it
	// out (AC-G1, CR-1); a write recorded on it BEFORE the gate took it (a pool
	// taker's, when it was still a pool connection) is not. A closed pointer
	// stays reachable from the records, so the runtime cannot reuse it.
	// Bounded at pool size + 1: each replacement consumes one pool connection
	// for good.
	owned map[*sqlite.Conn]uint64
	// closeSeq is the write-statement sequence recorded by Close. Close puts
	// the dedicated connection back in the pool, so a later non-gate taker
	// could write on an owned pointer: only records older than closeSeq are
	// the gate's (R-8).
	closeSeq uint64
	// replaced counts connections that could not be cleaned after a failed
	// transaction and were replaced from the pool.
	replaced int

	watchdogStop  chan struct{}
	watchdogDone  chan struct{}
	watchdogFired atomic.Int64
}

// newWriteGate takes the gate's dedicated connection from pool and registers
// the gate as the pool's one writer; a pool that already has a live gate is
// refused (errGateExists) — two gates on one pool would busy-wait against
// each other while each reports every write as gated (PM-G8).
func newWriteGate(ctx context.Context, pool *sqlitemigration.Pool) (*writeGate, error) {
	conn, err := pool.Take(ctx)
	if err != nil {
		return nil, fmt.Errorf("write gate: take dedicated connection: %w", err)
	}
	// Pool.Take bound the interrupt to ctx; the connection outlives it.
	conn.SetInterrupt(nil)
	g := &writeGate{
		pool:         pool,
		conn:         conn,
		owned:        map[*sqlite.Conn]uint64{conn: writeStmtSeq.Load()},
		watchdogStop: make(chan struct{}),
		watchdogDone: make(chan struct{}),
	}
	g.idle = sync.NewCond(&g.mu)
	if err := registerWriteGate(pool, g); err != nil {
		pool.Put(conn)
		return nil, err
	}
	if obs := writeGateObserver.Load(); obs != nil {
		(*obs)(g)
	}
	go g.watchdog()
	return g, nil
}

// owns reports whether a write statement recorded as seq on conn was the
// gate's own: conn is one the gate has ever held, and the record falls
// inside the gate's tenure of it — after the gate took it and before Close
// (a live gate has closeSeq 0).
func (g *writeGate) owns(conn *sqlite.Conn, seq uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	openSeq, ok := g.owned[conn]
	if !ok || seq <= openSeq {
		return false
	}
	return g.closeSeq == 0 || seq < g.closeSeq
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
	g.holdStart, g.holdPath, g.holdKind = time.Time{}, "", ""
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
// already-prepared bytes (INV-1; the legacy kinds' INV-1′ adds one rename of
// a file this process finished writing before the ticket). fn receives a
// ctx marked as gate-held: a nested acquire through it fails at O(1)
// (errGateReentrant) instead of queuing behind its own holder. A non-nil
// error from fn rolls the transaction back and is returned; a panic in fn is
// recovered, rolled back, counted under CommitOutcomePanic and returned as
// an error (L0-B). path labels the hold, kind the commit outcome.
func (g *writeGate) run(ctx context.Context, priority writePriority, path, kind string, fn func(ctx context.Context, conn *sqlite.Conn) error) (err error) {
	if held := ctx.Value(gateCtxKey{}); held != nil {
		metrics.IncWriteGateReentrant(path)
		if gateReentrantPanics.Load() {
			panic(fmt.Sprintf("%v: %s inside %s", errGateReentrant, path, held))
		}
		return fmt.Errorf("%w: %s inside %s", errGateReentrant, path, held)
	}
	if err := g.acquire(ctx, priority); err != nil {
		return err
	}
	defer g.release()
	g.mu.Lock()
	g.holdStart, g.holdPath, g.holdKind = time.Now(), path, kind
	conn := g.conn
	g.mu.Unlock()

	start := time.Now()
	defer func() { metrics.ObserveSqliteWriteHold(path, kind, time.Since(start)) }()

	defer func() {
		if r := recover(); r != nil {
			logger.L().Error("write gate: panic inside the gated transaction, rolled back",
				helpers.String("path", path), helpers.String("kind", kind), helpers.Interface("panic", r), helpers.String("stack", string(debug.Stack())))
			metrics.IncSingleWriterCommit(kind, priority.label(), metrics.CommitOutcomePanic)
			g.cleanOrReplace(conn)
			if perr, ok := r.(error); ok {
				err = fmt.Errorf("write gate: panic during %s transaction: %w", path, perr)
			} else {
				err = fmt.Errorf("write gate: panic during %s transaction: %v", path, r)
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
	err = fn(context.WithValue(ctx, gateCtxKey{}, path), conn)
	endFn(&err)
	if err != nil {
		g.cleanOrReplace(conn)
		if errors.Is(err, errWriteConflict) || storage.IsExist(err) {
			metrics.IncSingleWriterCommit(kind, priority.label(), metrics.CommitOutcomeConflict)
		} else {
			metrics.IncSingleWriterCommit(kind, priority.label(), metrics.CommitOutcomeError)
		}
		return err
	}
	metrics.IncSingleWriterCommit(kind, priority.label(), metrics.CommitOutcomeCommitted)
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
	logger.L().Error("write gate: connection could not be cleaned, replacing it from the pool")
	ctx, cancel := context.WithTimeout(context.Background(), poolTimeout)
	defer cancel()
	fresh, err := g.pool.Take(ctx)
	if err != nil {
		logger.L().Error("write gate: could not take a replacement connection; keeping the dirty one", helpers.Error(err))
		return
	}
	fresh.SetInterrupt(nil)
	openSeq := writeStmtSeq.Load()
	// The dirty connection is closed, not returned: a mid-transaction
	// connection in the pool would poison whoever takes it next. This leaves
	// the pool one short for Pool.Close (the K-5 note on Put(nil)).
	_ = conn.Close()
	g.mu.Lock()
	g.conn = fresh
	g.owned[fresh] = openSeq
	g.replaced++
	g.mu.Unlock()
}

// watchdog samples the hold every gateWatchdogInterval: it keeps the
// hold-age gauge current and, once per hold, logs the holder with every
// goroutine's stack when the hold outlives gateWatchdogThreshold (PM-G2).
func (g *writeGate) watchdog() {
	defer close(g.watchdogDone)
	ticker := time.NewTicker(gateWatchdogInterval)
	defer ticker.Stop()
	logged := false
	for {
		select {
		case <-g.watchdogStop:
			metrics.SetWriteGateHoldAge(0)
			return
		case <-ticker.C:
		}
		g.mu.Lock()
		busy, start, path, kind := g.busy, g.holdStart, g.holdPath, g.holdKind
		g.mu.Unlock()
		if !busy || start.IsZero() {
			metrics.SetWriteGateHoldAge(0)
			logged = false
			continue
		}
		age := time.Since(start)
		metrics.SetWriteGateHoldAge(age)
		if age > gateWatchdogThreshold && !logged {
			logged = true
			g.watchdogFired.Add(1)
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			logger.L().Error("write gate: held past the watchdog threshold; every writer of every kind is queued behind it",
				helpers.String("path", path), helpers.String("kind", kind), helpers.String("age", age.String()), helpers.String("stacks", string(buf[:n])))
		}
	}
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
	// Every gated write was recorded before this point; anything on the
	// returned connection from here on is somebody else's (R-8).
	g.closeSeq = writeStmtSeq.Add(1)
	g.mu.Unlock()
	close(g.watchdogStop)
	<-g.watchdogDone
	unregisterWriteGate(g.pool, g)
	if conn != nil {
		g.pool.Put(conn)
	}
	return nil
}
