package file

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"zombiezen.com/go/sqlite"
)

// writeGarbageDatabase makes path, its WAL and its shm unreadable as SQLite.
// The WAL must be garbage too: an intact WAL still serves every page and the
// database opens.
func writeGarbageDatabase(t *testing.T, path string) {
	t.Helper()
	garbage := make([]byte, 8*4096)
	_, err := rand.Read(garbage)
	require.NoError(t, err)
	for _, name := range []string{path, path + "-wal", path + "-shm"} {
		require.NoError(t, os.WriteFile(name, garbage, 0o600))
	}
}

// A database file that cannot be opened keeps the pool retrying forever, and
// every Take waits out its context. The reason must reach OnOpenError, or
// nothing says why the pool never becomes ready.
func TestNewPoolReportsOpenError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sq3")
	writeGarbageDatabase(t, path)

	var mu sync.Mutex
	var reported []error
	pool := NewPoolWithOptions(path, PoolOptions{
		OnOpenError: func(err error) {
			mu.Lock()
			defer mu.Unlock()
			reported = append(reported, err)
		},
	})
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := pool.Take(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "Take error: %v", err)

	// The open runs in the pool's own goroutine; do not assume it reported
	// before Take gave up.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(reported) > 0
	}, 5*time.Second, 10*time.Millisecond, "the open error was not reported")

	mu.Lock()
	defer mu.Unlock()
	assert.Error(t, reported[0])
	t.Logf("reported open error: %v", reported[0])
}

// The default reporter must not be skipped when no OnOpenError is given: the
// pool still fails Take on its context and never panics.
func TestNewPoolDefaultOpenErrorReporter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata.sq3")
	writeGarbageDatabase(t, path)

	pool := NewPoolWithOptions(path, PoolOptions{})
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := pool.Take(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "Take error: %v", err)
}

// The default reporter also gets the errors the pool hits after the file
// opened, such as a failed connection preparation, so its line must name
// initialization, not the open. Each scenario runs in a child process that
// pins the pretty logger, so the test reads the line it writes to stderr
// whatever logger the environment selects.
func TestNewPoolDefaultReporterNamesInitialization(t *testing.T) {
	if scenario := os.Getenv("STORAGE_TEST_SQLITE_INIT_ERROR"); scenario != "" {
		logger.InitLogger("pretty")
		path := filepath.Join(t.TempDir(), "metadata.sq3")
		var opts PoolOptions
		switch scenario {
		case "open":
			writeGarbageDatabase(t, path)
		case "prepare":
			opts.PrepareConn = func(*sqlite.Conn) error {
				return errors.New("connection preparation failed on purpose")
			}
		default:
			t.Fatalf("unknown scenario %q", scenario)
		}
		pool := NewPoolWithOptions(path, opts)
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, err := pool.Take(ctx)
		require.Error(t, err)
		if scenario == "prepare" {
			assert.ErrorContains(t, err, "connection preparation failed on purpose")
		}
		// Close waits for the pool's goroutine, so its first report is out.
		_ = pool.Close()
		return
	}

	for _, tc := range []struct {
		scenario string
		cause    string
	}{
		{scenario: "open", cause: "metadata.sq3"},
		{scenario: "prepare", cause: "connection preparation failed on purpose"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestNewPoolDefaultReporterNamesInitialization$")
			command.Env = append(os.Environ(), "STORAGE_TEST_SQLITE_INIT_ERROR="+tc.scenario)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			assert.Contains(t, string(output), "sqlite pool - error while initializing the database")
			assert.Contains(t, string(output), tc.cause)
			assert.NotContains(t, string(output), "error while opening the database")
		})
	}
}

func TestOpenErrorThrottle(t *testing.T) {
	now := time.Unix(1000, 0)
	throttle := newOpenErrorThrottle(30*time.Second, func() time.Time { return now })

	suppressed, ok := throttle.allow()
	assert.True(t, ok, "the first error is always reported")
	assert.Zero(t, suppressed)

	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		_, ok = throttle.allow()
		assert.False(t, ok, "errors inside the interval are suppressed")
	}

	now = now.Add(30 * time.Second)
	suppressed, ok = throttle.allow()
	assert.True(t, ok, "an error after the interval is reported")
	assert.Equal(t, 5, suppressed, "the report carries how many were suppressed")
}
