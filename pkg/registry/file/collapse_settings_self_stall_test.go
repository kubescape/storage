package file

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/client-go/kubernetes/scheme"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitex"
)

// selfStallBusyTimeout is the SQLite busy timeout for the self-stall tests. It
// is short so a failing run finishes quickly, yet far above selfStallBound so
// "blocked for the busy timeout" and "returned immediately" cannot be confused.
const (
	selfStallBusyTimeout = 2 * time.Second
	selfStallBound       = 500 * time.Millisecond
)

// newSelfStallStorage builds a StorageImpl on a real pool whose connections
// carry selfStallBusyTimeout, so a statement that waits on SQLite's write lock
// waits a measurable, bounded time instead of DefaultBusyTimeout.
func newSelfStallStorage(t *testing.T, processor Processor) *StorageImpl {
	t.Helper()
	pool := NewPool(filepath.Join(t.TempDir(), "selfstall.sq3"), 0, selfStallBusyTimeout)
	// Pool.Close blocks until every connection is returned, which also joins
	// any background refresh still holding one.
	t.Cleanup(func() { _ = pool.Close() })
	sch := scheme.Scheme
	install.Install(sch)
	return &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
}

// refreshWaitTimeout bounds how long a test waits for the provider's background
// refresh to finish; it only has to outlast the lock/pool waits that refresh
// may legitimately do (lockTimeout, poolTimeout) once the test has released them.
const refreshWaitTimeout = 30 * time.Second

// countingGetStorage wraps the real StorageImpl so tests can observe when the
// provider's background refresh has COMPLETED its Get (the count is bumped
// after Get returns). The provider only ever calls Get.
type countingGetStorage struct {
	storage.Interface
	completedGets atomic.Int64
}

func (c *countingGetStorage) Get(ctx context.Context, key string, opts storage.GetOptions, out runtime.Object) error {
	err := c.Interface.Get(ctx, key, opts, out)
	c.completedGets.Add(1)
	return err
}

// joinRefresh waits until the provider's construction-time prime and exactly
// one background refresh have completed, so no refresh goroutine outlives the
// test (and none can race a t.Cleanup that restores package state).
func joinRefresh(t *testing.T, c *countingGetStorage) {
	t.Helper()
	require.Eventually(t, func() bool { return c.completedGets.Load() == 2 },
		refreshWaitTimeout, time.Millisecond, "background refresh did not complete")
}

// holdWriteLock opens a deferred transaction on a fresh pool connection and
// performs one write in it, which is what acquires SQLite's WAL writer lock --
// the state processTimeSeriesInTransaction is in once
// ReplaceTimeSeriesContainerEntries has run. The returned func commits and
// returns the connection.
func holdWriteLock(t *testing.T, s *StorageImpl) (conn *sqlite.Conn, release func()) {
	t.Helper()
	conn, err := s.pool.Take(context.Background())
	require.NoError(t, err)
	endFn := sqlitex.Transaction(conn)
	require.NoError(t, WriteTimeSeriesEntry(conn, ContainerProfileKind, "ns", "cp", "series", "ts1", "2026-01-01T00:00:00Z", "", "", "", true))
	return conn, func() {
		var txErr error
		endFn(&txErr)
		require.NoError(t, txErr)
		s.pool.Put(conn)
	}
}

// AC-A1: the provider must not perform storage I/O on the caller's goroutine.
// The caller holds SQLite's write lock in an open transaction (as the
// consolidation save does when PreSave runs), the cache is expired and no
// CollapseConfiguration CR exists. Before the fix the refresh's Get ran on a
// second connection and its absent-row DeleteMetadata waited on the lock the
// caller itself holds, for the whole busy timeout.
func TestCRDCollapseSettingsProvider_NoStorageIOUnderHeldWriteLock(t *testing.T) {
	oldTTL := collapseSettingsTTL
	collapseSettingsTTL = time.Nanosecond // every call finds the cache expired
	t.Cleanup(func() { collapseSettingsTTL = oldTTL })

	s := newSelfStallStorage(t, DefaultProcessor{})
	cs := &countingGetStorage{Interface: s}
	provider := NewCRDCollapseSettingsProvider(cs)

	_, release := holdWriteLock(t, s)
	start := time.Now()
	got := provider()
	elapsed := time.Since(start)
	release()

	assert.Less(t, elapsed, selfStallBound, "provider() must not wait on the caller's own write lock (busy timeout %s)", selfStallBusyTimeout)
	assert.Equal(t, 50, got.OpenDynamicThreshold, "no CR present: defaults are served")
	joinRefresh(t, cs)
}

// AC-A1b: same boundary, different blocker. The per-key write lock of the
// CollapseConfiguration key is held (as a concurrent REST write of the CR
// would), so the refresh's Get cannot even acquire its read lock until the
// key is unlocked (or lockTimeout, 5s, expires). That wait must stay off the
// caller's goroutine too. This variant keeps guarding the boundary once get()
// no longer writes on a miss. lockTimeout is deliberately left at its default:
// shrinking it would only shorten the pre-fix failure, and a package var
// mutated by the test could race the background refresh's read of it.
func TestCRDCollapseSettingsProvider_NoLockWaitOnCallerGoroutine(t *testing.T) {
	oldTTL := collapseSettingsTTL
	collapseSettingsTTL = time.Nanosecond
	t.Cleanup(func() { collapseSettingsTTL = oldTTL })

	s := newSelfStallStorage(t, DefaultProcessor{})
	cs := &countingGetStorage{Interface: s}
	provider := NewCRDCollapseSettingsProvider(cs)

	key := collapseConfigurationKey(DefaultCollapseConfigurationName)
	require.NoError(t, s.locks.Lock(context.Background(), key))
	start := time.Now()
	provider()
	elapsed := time.Since(start)
	s.locks.Unlock(key)

	assert.Less(t, elapsed, selfStallBound, "provider() must not wait for the CR key's lock (lockTimeout %s)", lockTimeout)
	joinRefresh(t, cs)
}

// AC-A2: the production chain. ConsolidateTimeSeries ->
// processTimeSeriesInTransaction -> updateProfile -> SaveContainerProfile ->
// GuaranteedUpdateWithConn -> PreSave -> CollapseSettings(), with the real
// provider wired as pkg/apiserver does and no CR present. Before the fix one
// tick took at least the busy timeout.
func TestConsolidateTimeSeries_DoesNotStallOnCollapseRefresh(t *testing.T) {
	oldTTL := collapseSettingsTTL
	collapseSettingsTTL = time.Nanosecond
	t.Cleanup(func() { collapseSettingsTTL = oldTTL })

	processor := &ContainerProfileProcessor{
		DeleteThreshold:         0,
		MaxContainerProfileSize: 40000,
		Workers:                 1,
	}
	s := newSelfStallStorage(t, processor)
	cs := &countingGetStorage{Interface: s}
	processor.CollapseSettings = NewCRDCollapseSettingsProvider(cs)
	processor.SetStorage(NewContainerProfileStorageImpl(s, s.pool))

	ctx := context.Background()
	for _, f := range []string{"testdata/p1.json", "testdata/p2.json"} {
		content, err := os.ReadFile(f)
		require.NoError(t, err)
		var profile softwarecomposition.ContainerProfile
		require.NoError(t, json.Unmarshal(content, &profile))
		require.NoError(t, s.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/"+profile.Namespace+"/"+profile.Name, &profile, nil, 0))
	}

	start := time.Now()
	err := processor.ConsolidateTimeSeries(ctx)
	elapsed := time.Since(start)
	require.NoError(t, err)
	assert.Less(t, elapsed, selfStallBound, "consolidation tick must not stall on the CollapseConfiguration refresh (busy timeout %s)", selfStallBusyTimeout)
	joinRefresh(t, cs)
}
