package file

import "sync/atomic"

// stmtObserver is the Tier A work-budget hook (TestWorkBudget): every
// sqlitex.Execute call site in this package and every transaction opener
// reports its enclosing function's name through observeStmt. It is nil in
// production, where observeStmt is one atomic load and a nil check and
// allocates nothing. An atomic.Pointer rather than a bare func var so the
// harness can install and clear it while a leaked shard or watch goroutine
// from an earlier test is still executing statements, without a data race.
var stmtObserver atomic.Pointer[func(string)]

// observeStmt reports one statement (or transaction opener) execution at the
// named site to the installed observer, if any.
func observeStmt(site string) {
	if f := stmtObserver.Load(); f != nil {
		(*f)(site)
	}
}

// setStmtObserver installs f as the statement observer; nil uninstalls it.
func setStmtObserver(f func(string)) {
	if f == nil {
		stmtObserver.Store(nil)
		return
	}
	stmtObserver.Store(&f)
}
