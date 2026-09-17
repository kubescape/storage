package file

// Background PASSIVE WAL checkpointer for the ObjectStore (design §6.2, K-3).
//
// With PRAGMA wal_autocheckpoint=0 on every pool connection (PoolOptions.
// DisableAutoCheckpoint), no COMMIT ever runs a checkpoint inside the gate
// holder; this goroutine runs PRAGMA wal_checkpoint(PASSIVE) on its own pooled
// connection instead. PASSIVE takes the checkpoint lock, never the writer lock,
// and never invokes the busy handler, so it cannot stall a gated commit.
//
// zombiezen v1.4.0 exposes no sqlite3_wal_hook, so the trigger is a size check
// on the -wal file after each gated commit (one os.Stat) plus a timer for the
// ungated writers (legacy kinds, cleanup.go). The goroutine is supervised: a
// panic is recovered, counted and the loop restarted, because with
// autocheckpoint off everywhere a dead checkpointer means an unbounded WAL.

import (
	"context"
	"os"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/metrics"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
	"zombiezen.com/go/sqlite/sqlitex"
)

const (
	// DefaultCheckpointThresholdBytes is the -wal size that triggers a
	// checkpoint after a gated commit: the 1000 pages × 4 KB SQLite's own
	// autocheckpoint would have used.
	DefaultCheckpointThresholdBytes = 1000 * 4096
	// DefaultCheckpointInterval is the timer fallback for WAL growth produced
	// by writers that do not go through the gate.
	DefaultCheckpointInterval = 5 * time.Second
	// DefaultCheckpointMinSpacing bounds how often PASSIVE checkpoints run when
	// commits keep kicking the checkpointer. Under concurrent readers the WAL
	// cannot be reset, so its file size never drops below the kick threshold
	// and every commit would re-kick; back-to-back checkpoints rewrite the
	// wal-index header continuously and readers that see it change retry with
	// SQLite's quadratic backoff (multi-second silent read stalls, measured in
	// Tier B as 5-10 s ticks/updates with 7000 checkpoints per round). Kicks
	// arriving inside the spacing are coalesced into one run at its end.
	DefaultCheckpointMinSpacing = 250 * time.Millisecond
	// checkpointRestartBackoff spaces supervised restarts after a panic.
	checkpointRestartBackoff = 100 * time.Millisecond
)

type checkpointer struct {
	pool           *sqlitemigration.Pool
	walPath        string
	thresholdBytes int64
	interval       time.Duration
	minSpacing     time.Duration

	kick     chan struct{}
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once

	runs     atomic.Int64
	restarts atomic.Int64
	walPages atomic.Int64

	// beforeCheckpoint is a test seam invoked at the top of every checkpoint
	// run (nil in production); the supervision test panics from it.
	beforeCheckpoint func()
}

func newCheckpointer(pool *sqlitemigration.Pool, dbPath string, thresholdBytes int64, interval time.Duration) *checkpointer {
	if thresholdBytes <= 0 {
		thresholdBytes = DefaultCheckpointThresholdBytes
	}
	if interval <= 0 {
		interval = DefaultCheckpointInterval
	}
	return &checkpointer{
		pool:           pool,
		walPath:        dbPath + "-wal",
		thresholdBytes: thresholdBytes,
		interval:       interval,
		minSpacing:     DefaultCheckpointMinSpacing,
		kick:           make(chan struct{}, 1),
		stop:           make(chan struct{}),
		done:           make(chan struct{}),
	}
}

func (c *checkpointer) start() {
	go c.supervise()
}

func (c *checkpointer) supervise() {
	defer close(c.done)
	for {
		panicked := c.loop()
		if !panicked {
			return
		}
		c.restarts.Add(1)
		metrics.IncSqliteCheckpoint(metrics.CheckpointOutcomePanic)
		select {
		case <-c.stop:
			return
		case <-time.After(checkpointRestartBackoff):
		}
	}
}

// loop runs until stop; it returns true if it exited because of a panic.
func (c *checkpointer) loop() (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			logger.L().Error("ObjectStore checkpointer panicked; restarting",
				helpers.Interface("panic", r), helpers.String("stack", string(debug.Stack())))
			panicked = true
		}
	}()
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	var last time.Time
	for {
		select {
		case <-c.stop:
			return false
		case <-c.kick:
			if wait := c.minSpacing - time.Since(last); wait > 0 {
				select {
				case <-c.stop:
					return false
				case <-time.After(wait):
				}
			}
			c.checkpoint()
			last = time.Now()
		case <-ticker.C:
			c.checkpoint()
			last = time.Now()
		}
	}
}

// afterCommit is called by the gate holder's caller after every COMMIT (never
// inside the hold): one os.Stat, and a non-blocking kick when the WAL is over
// the threshold.
func (c *checkpointer) afterCommit() {
	st, err := os.Stat(c.walPath)
	if err != nil || st.Size() < c.thresholdBytes {
		return
	}
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

func (c *checkpointer) checkpoint() {
	if c.beforeCheckpoint != nil {
		c.beforeCheckpoint()
	}
	c.runs.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), poolTimeout)
	defer cancel()
	conn, err := c.pool.Take(ctx)
	if err != nil {
		metrics.IncSqliteCheckpoint(metrics.CheckpointOutcomeError)
		logger.L().Debug("ObjectStore checkpointer: no pool connection", helpers.Error(err))
		return
	}
	defer c.pool.Put(conn)

	var busy, logPages int64
	err = sqlitex.ExecuteTransient(conn, `PRAGMA wal_checkpoint(PASSIVE)`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			busy = stmt.ColumnInt64(0)
			logPages = stmt.ColumnInt64(1)
			return nil
		},
	})
	if err != nil {
		metrics.IncSqliteCheckpoint(metrics.CheckpointOutcomeError)
		logger.L().Error("ObjectStore checkpointer: wal_checkpoint failed", helpers.Error(err))
		return
	}
	c.walPages.Store(logPages)
	metrics.SetSqliteWalPages(logPages)
	if busy != 0 {
		metrics.IncSqliteCheckpoint(metrics.CheckpointOutcomeBusy)
	} else {
		metrics.IncSqliteCheckpoint(metrics.CheckpointOutcomeOK)
	}
	_ = sqlitex.ExecuteTransient(conn, `PRAGMA freelist_count`, &sqlitex.ExecOptions{
		ResultFunc: func(stmt *sqlite.Stmt) error {
			metrics.SetSqliteFreelistCount(stmt.ColumnInt64(0))
			return nil
		},
	})
}

// Stop ends the goroutine and waits for it. Idempotent.
func (c *checkpointer) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.done
}
