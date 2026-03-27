package utils

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestMapMutex_BasicLockUnlock(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	err := m.Lock(ctx, "key1")
	require.NoError(t, err)
	m.Unlock("key1")
}

func TestMapMutex_ConcurrentLockSameKey(t *testing.T) {
	m := NewMapMutex[string]()
	var counter int64
	var wg sync.WaitGroup

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			ctx := testCtx(t)
			for j := 0; j < 100; j++ {
				err := m.Lock(ctx, "shared")
				require.NoError(t, err)
				v := counter
				counter = v + 1
				m.Unlock("shared")
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, int64(200), counter)
}

func TestMapMutex_RLockAllowsConcurrentReaders(t *testing.T) {
	m := NewMapMutex[string]()
	var wg sync.WaitGroup
	bothHeld := make(chan struct{})

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			ctx := testCtx(t)
			err := m.RLock(ctx, "key")
			require.NoError(t, err)
			defer m.RUnlock("key")
			select {
			case bothHeld <- struct{}{}:
			case <-time.After(2 * time.Second):
				t.Error("timed out waiting for concurrent reader")
			}
		}()
	}
	<-bothHeld
	<-bothHeld
	wg.Wait()
}

func TestMapMutex_EvictionWhileHeldAndAfterUnlock(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	err := m.Lock(ctx, "key")
	require.NoError(t, err)

	m.mu.Lock()
	assert.Equal(t, 1, len(m.keys), "map should have entry while lock is held")
	m.mu.Unlock()

	m.Unlock("key")

	m.mu.Lock()
	assert.Equal(t, 0, len(m.keys), "map should be empty after unlock")
	m.mu.Unlock()
}

func TestMapMutex_ContextTimeoutDoesNotLeakRefcount(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	// Hold an exclusive lock so the second Lock must wait and timeout.
	err := m.Lock(ctx, "blocked")
	require.NoError(t, err)

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = m.Lock(shortCtx, "blocked")
	assert.Error(t, err, "should fail due to context timeout")

	// Release the original lock — the cleanup goroutine will acquire then release.
	m.Unlock("blocked")

	assert.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.keys) == 0
	}, time.Second, time.Millisecond, "map should be empty — timed-out acquire must not leak")
}

func TestMapMutex_ContextCancellationReturnsError(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	// Hold the lock so the second Lock actually blocks and observes cancellation.
	err := m.Lock(ctx, "key")
	require.NoError(t, err)

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err = m.RLock(cancelCtx, "key")
	assert.ErrorIs(t, err, context.Canceled)

	m.Unlock("key")

	assert.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.keys) == 0
	}, time.Second, time.Millisecond)
}

func TestMapMutex_NilContextReturnsError(t *testing.T) {
	m := NewMapMutex[string]()
	assert.ErrorIs(t, m.Lock(nil, "key"), ContextNilError)
	assert.ErrorIs(t, m.RLock(nil, "key"), ContextNilError)
}

func TestMapMutex_StressTest(t *testing.T) {
	m := NewMapMutex[string]()
	keys := make([]string, 20)
	for i := range keys {
		keys[i] = "key-" + string(rune('a'+i))
	}

	var wg sync.WaitGroup
	const goroutines = 100
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for j := 0; j < 50; j++ {
				key := keys[rng.Intn(len(keys))]
				useRead := rng.Intn(3) == 0
				var timeout time.Duration
				if rng.Intn(5) == 0 {
					timeout = time.Millisecond
				} else {
					timeout = 5 * time.Second
				}
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				if useRead {
					if err := m.RLock(ctx, key); err == nil {
						time.Sleep(time.Duration(rng.Intn(100)) * time.Microsecond)
						m.RUnlock(key)
					}
				} else {
					if err := m.Lock(ctx, key); err == nil {
						time.Sleep(time.Duration(rng.Intn(100)) * time.Microsecond)
						m.Unlock(key)
					}
				}
				cancel()
			}
		}(i)
	}
	wg.Wait()

	assert.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.keys) == 0
	}, 5*time.Second, time.Millisecond, "map should be empty after all goroutines complete")
}

func TestMapMutex_SingleKeyHighContention(t *testing.T) {
	m := NewMapMutex[string]()
	const key = "hot-key"
	var wg sync.WaitGroup
	const goroutines = 200
	const iterations = 200
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(id)))
			for j := 0; j < iterations; j++ {
				timeout := time.Duration(rng.Intn(10)+1) * time.Millisecond
				ctx, cancel := context.WithTimeout(context.Background(), timeout)
				switch rng.Intn(3) {
				case 0:
					if err := m.Lock(ctx, key); err == nil {
						m.Unlock(key)
					}
				case 1:
					if err := m.RLock(ctx, key); err == nil {
						m.RUnlock(key)
					}
				case 2:
					if err := m.RLock(ctx, key); err == nil {
						time.Sleep(time.Duration(rng.Intn(50)) * time.Microsecond)
						m.RUnlock(key)
					}
				}
				cancel()
			}
		}(i)
	}
	wg.Wait()

	assert.Eventually(t, func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		return len(m.keys) == 0
	}, 5*time.Second, time.Millisecond, "map should be empty after all goroutines complete")
}

func TestMapMutex_WriterPriority(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	// Reader holds the lock.
	err := m.RLock(ctx, "key")
	require.NoError(t, err)

	// Writer blocks behind the reader.
	writerDone := make(chan struct{})
	go func() {
		err := m.Lock(ctx, "key")
		require.NoError(t, err)
		close(writerDone)
		time.Sleep(50 * time.Millisecond)
		m.Unlock("key")
	}()

	// Give the writer time to enter the wait loop.
	time.Sleep(10 * time.Millisecond)

	// A new reader must NOT slip past the pending writer.
	shortCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = m.RLock(shortCtx, "key")
	assert.Error(t, err, "new reader should block while a writer is pending")

	// Release original reader — writer acquires.
	m.RUnlock("key")
	<-writerDone

	// After writer releases, readers proceed normally.
	err = m.RLock(ctx, "key")
	require.NoError(t, err)
	m.RUnlock("key")
}
