package file

// AC-G1 of .omc/plans/write-gate-sharing.md: no write statement is ever
// prepared on a connection the pool's write gate does not own. The package's
// write authorizer (sqlite.go) reports every INSERT/UPDATE/DELETE prepared on
// any pool connection; this ledger keeps each report with the stack of the
// site that prepared it and judges it against every gate ever built on that
// pool (gate.owns: a connection the gate has EVER held, and a record older
// than the gate's Close). Pools that never had a gate — every flag-off test —
// are vacuous.
//
// Record-and-fail, not deny: both found bugs swallow the statement's error
// (`_ = DeleteMetadata(...)`), so a denied statement would have passed the
// test; recording sees the attempt regardless of what the caller does with
// the result. Every gated environment arms a per-test check
// (armUngatedWriteCheck); TestMain sweeps what escaped every per-test check.

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"

	"zombiezen.com/go/sqlite/sqlitemigration"
)

type ungatedRecord struct {
	writeStatement
	pcs    []uintptr
	judged bool
}

func (r *ungatedRecord) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s on %s (seq %d) prepared on a pool connection the write gate never owned; prepared at:\n", writeOpLabel(r.op), r.table, r.seq)
	frames := runtime.CallersFrames(r.pcs)
	for {
		f, more := frames.Next()
		if strings.Contains(f.File, "/pkg/registry/file/") || strings.Contains(f.Function, "kubescape/storage") {
			fmt.Fprintf(&b, "\t%s\n\t\t%s:%d\n", f.Function, f.File, f.Line)
		}
		if !more {
			break
		}
	}
	return b.String()
}

type ungatedLedger struct {
	mu      sync.Mutex
	records []*ungatedRecord
	// gates is every gate ever constructed, per pool; a pool with an entry is
	// armed for the whole test binary's lifetime.
	gates map[*sqlitemigration.Pool][]*writeGate
}

var acg1Ledger = &ungatedLedger{gates: map[*sqlitemigration.Pool][]*writeGate{}}

func (l *ungatedLedger) note(rec writeStatement) {
	var pcs [48]uintptr
	n := runtime.Callers(3, pcs[:])
	r := &ungatedRecord{writeStatement: rec, pcs: pcs[:n]}
	l.mu.Lock()
	l.records = append(l.records, r)
	l.mu.Unlock()
}

func (l *ungatedLedger) noteGate(g *writeGate) {
	l.mu.Lock()
	l.gates[g.pool] = append(l.gates[g.pool], g)
	l.mu.Unlock()
}

// violations judges every unjudged record of pool (every pool when nil) and
// returns those no gate of the pool owns. Records on unarmed pools stay
// unjudged: a gate may still be built on that pool later in the test.
func (l *ungatedLedger) violations(pool *sqlitemigration.Pool) []*ungatedRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*ungatedRecord
	for _, r := range l.records {
		if r.judged || (pool != nil && r.pool != pool) {
			continue
		}
		gates := l.gates[r.pool]
		if len(gates) == 0 {
			continue
		}
		r.judged = true
		owned := false
		for _, g := range gates {
			if g.owns(r.conn, r.seq) {
				owned = true
				break
			}
		}
		if !owned {
			out = append(out, r)
		}
	}
	return out
}

// armUngatedWriteCheck registers AC-G1 for pool on t: at cleanup, every write
// statement recorded on a pool connection that no gate of the pool has ever
// owned fails the test with the stack of the site that prepared it. Register
// it before the pool's own cleanup so it runs after the stores closed.
func armUngatedWriteCheck(t *testing.T, pool *sqlitemigration.Pool) {
	t.Helper()
	t.Cleanup(func() {
		for _, v := range acg1Ledger.violations(pool) {
			t.Errorf("AC-G1 ungated write: %s", v)
		}
	})
}

func TestMain(m *testing.M) {
	note := acg1Ledger.note
	writeStmtObserver.Store(&note)
	noteGate := acg1Ledger.noteGate
	writeGateObserver.Store(&noteGate)

	code := m.Run()

	if vs := acg1Ledger.violations(nil); len(vs) > 0 {
		fmt.Fprintf(os.Stderr, "AC-G1: %d ungated write(s) escaped every per-test check:\n", len(vs))
		for _, v := range vs {
			fmt.Fprintf(os.Stderr, "  %s\n", v)
		}
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}
