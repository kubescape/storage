package file

import (
	"context"
	"sync"
	"sync/atomic"

	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// writeGate serializes every SQLite write section in this process. This
// process is the only writer of the database file, so holding the gate for
// exactly the duration of one write transaction makes SQLITE_BUSY between our
// own writers structurally impossible (see DURESS.md, rows 1/12/14/15).
// SQLite serializes writers anyway; the gate moves the queue from the SQLite
// write lock — where waiting costs a pool connection and surfaces
// "database is locked" — to a Go mutex, where waiting is cheap, bounded by the
// caller's context, and fail-fast.
//
// Lock ordering: per-key app lock → gate. Gate holders must not acquire app
// locks. Reads never take the gate (WAL keeps them non-blocking).
//
// Ownership is tracked per connection: a connection that already holds the
// gate (an enclosing gated transaction, e.g. a consolidation pass) re-enters
// without blocking, because nested SQLite work on the same connection is part
// of the same write transaction. A connection is only ever driven by one
// goroutine at a time (pool semantics), so the owner check is race-free for
// the re-entry case.
type writeGate struct {
	slot  chan struct{}
	owner atomic.Pointer[sqlite.Conn]
}

// poolGates maps a connection pool (one per database file) to its gate.
var poolGates sync.Map // *sqlitemigration.Pool → *writeGate

func gateForPool(pool *sqlitemigration.Pool) *writeGate {
	if g, ok := poolGates.Load(pool); ok {
		return g.(*writeGate)
	}
	g, _ := poolGates.LoadOrStore(pool, &writeGate{slot: make(chan struct{}, 1)})
	return g.(*writeGate)
}

// acquire takes the gate for conn, or re-enters if conn already holds it.
// The returned release must be called exactly once. ctx bounds the wait.
func (g *writeGate) acquire(ctx context.Context, conn *sqlite.Conn) (release func(), err error) {
	if g.owner.Load() == conn && conn != nil {
		return func() {}, nil // re-entry: enclosing transaction on this conn holds the gate
	}
	select {
	case g.slot <- struct{}{}:
		g.owner.Store(conn)
		return func() {
			g.owner.Store(nil)
			<-g.slot
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
