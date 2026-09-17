package file

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

// openFixtureConn opens a connection to pool's database file that is NOT a
// pool connection: the one handle tests may seed state through. A fixture
// write on a pool connection would prepare (and cache) a write statement
// there, and the code under test re-executing the same statement on that
// connection would then never re-invoke the authorizer the write-gate
// invariant (AC-G1) is built on — so fixtures never touch the pool.
//
// The pool's schema migration runs on the first Take; the handle is opened
// only after that, or fixture statements race the migration and fail on a
// missing table. Autocheckpoint is off on the handle so a fixture COMMIT does
// not checkpoint under the tests that measure the background checkpointer.
func openFixtureConn(t *testing.T, pool *sqlitemigration.Pool, dbPath string, busyTimeout time.Duration) *sqlite.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := pool.Take(ctx)
	require.NoError(t, err, "pool did not become ready")
	pool.Put(c)
	conn, err := sqlite.OpenConn(dbPath, sqlite.OpenReadWrite|sqlite.OpenWAL)
	require.NoError(t, err)
	conn.SetBusyTimeout(busyTimeout)
	require.NoError(t, sqlitex.ExecuteTransient(conn, `PRAGMA wal_autocheckpoint=0`, nil))
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
