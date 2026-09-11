package file

// One write gate per pool, and the instrument that proves it is the only
// writer (design: .omc/plans/write-gate-sharing.md §3.6 R-7, §7 AC-G1).
//
// SQLite has one write lock per database. Every write that goes through the
// gate is queued FIFO in Go; every write that does not acquires the same lock
// through SQLite's busy handler, which polls every 1…100 ms, is not
// ctx-bounded, and loses systematically against a gate that commits
// continuously — a stall of the whole busy timeout (60 s in production) that
// the gate's own instruments cannot see. So:
//
//   - a pool may carry at most one live gate (two gates on two dedicated
//     connections would each gate their own writes while busy-waiting
//     against each other — the class, visible from neither side);
//   - every INSERT/UPDATE/DELETE prepared on a connection the pool's gate has
//     never owned is counted (storage_sqlite_ungated_write_total, the
//     production canary) and handed to the test observer that turns it into
//     a failure with the stack of the site that prepared it.
//
// *sqlitemigration.Pool is a third-party type with nothing to register on,
// so the registry is a package-level map keyed by the pool pointer.

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kubescape/storage/pkg/metrics"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// errGateExists is returned by newWriteGate for a pool that already has a
// live gate.
var errGateExists = errors.New("write gate: the pool already has a live write gate")

var gateRegistry = struct {
	mu    sync.Mutex
	gates map[*sqlitemigration.Pool]*writeGate
}{gates: map[*sqlitemigration.Pool]*writeGate{}}

func registerWriteGate(pool *sqlitemigration.Pool, g *writeGate) error {
	gateRegistry.mu.Lock()
	defer gateRegistry.mu.Unlock()
	if _, exists := gateRegistry.gates[pool]; exists {
		return errGateExists
	}
	gateRegistry.gates[pool] = g
	return nil
}

func unregisterWriteGate(pool *sqlitemigration.Pool, g *writeGate) {
	gateRegistry.mu.Lock()
	defer gateRegistry.mu.Unlock()
	if gateRegistry.gates[pool] == g {
		delete(gateRegistry.gates, pool)
	}
}

// gateForPool returns the pool's live gate, or nil.
func gateForPool(pool *sqlitemigration.Pool) *writeGate {
	gateRegistry.mu.Lock()
	defer gateRegistry.mu.Unlock()
	return gateRegistry.gates[pool]
}

// writeStatement is one INSERT/UPDATE/DELETE prepared on a pool connection.
// seq orders it against the gate's Close (writeGate.owns).
type writeStatement struct {
	pool  *sqlitemigration.Pool
	conn  *sqlite.Conn
	op    sqlite.OpType
	table string
	seq   uint64
}

var writeStmtSeq atomic.Uint64

// writeStmtObserver receives every write statement prepared on any pool
// connection; nil in production. Tests install the AC-G1 ledger here.
var writeStmtObserver atomic.Pointer[func(writeStatement)]

// writeGateObserver receives every gate at construction; nil in production.
var writeGateObserver atomic.Pointer[func(*writeGate)]

// noteWriteStatement is the write authorizer's sink (sqlite.go).
func noteWriteStatement(pool *sqlitemigration.Pool, conn *sqlite.Conn, op sqlite.OpType, table string) {
	if strings.HasPrefix(table, "sqlite_") {
		// Schema migration bookkeeping (CREATE/ALTER TABLE write sqlite_master)
		// runs on the migration connection at pool open, before any traffic.
		return
	}
	rec := writeStatement{pool: pool, conn: conn, op: op, table: table, seq: writeStmtSeq.Add(1)}
	if g := gateForPool(pool); g != nil && !g.owns(conn, rec.seq) {
		metrics.IncSqliteUngatedWrite(writeOpLabel(op), table)
	}
	if obs := writeStmtObserver.Load(); obs != nil {
		(*obs)(rec)
	}
}

func writeOpLabel(op sqlite.OpType) string {
	switch op {
	case sqlite.OpInsert:
		return "insert"
	case sqlite.OpUpdate:
		return "update"
	case sqlite.OpDelete:
		return "delete"
	}
	return "other"
}
