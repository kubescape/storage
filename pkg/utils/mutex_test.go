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
			for j := 0; j < 100; j++ {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := m.Lock(ctx, "shared")
				require.NoError(t, err)
				// plain read-modify-write; race detector will catch if lock is broken
				v := counter
				counter = v + 1
				m.Unlock("shared")
				cancel()
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := m.RLock(ctx, "key")
			require.NoError(t, err)
			defer m.RUnlock("key")
			// signal that we hold the lock, wait for both
			select {
			case bothHeld <- struct{}{}:
			case <-time.After(2 * time.Second):
				t.Error("timed out waiting for concurrent reader")
			}
		}()
	}
	// drain both signals
	<-bothHeld
	<-bothHeld
	wg.Wait()
}

func TestMapMutex_EvictionAfterUnlock(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	err := m.Lock(ctx, "evict-me")
	require.NoError(t, err)
	m.Unlock("evict-me")

	m.m.Lock()
	assert.Equal(t, 0, len(m.locks), "map should be empty after unlock")
	m.m.Unlock()
}

func TestMapMutex_NoEvictionWhileHeld(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	err := m.Lock(ctx, "held")
	require.NoError(t, err)

	m.m.Lock()
	assert.Equal(t, 1, len(m.locks), "map should have entry while lock is held")
	m.m.Unlock()

	m.Unlock("held")

	m.m.Lock()
	assert.Equal(t, 0, len(m.locks), "map should be empty after unlock")
	m.m.Unlock()
}

func TestMapMutex_ContextTimeoutDoesNotLeakRefcount(t *testing.T) {
	m := NewMapMutex[string]()
	ctx := testCtx(t)

	// Hold an exclusive lock so the second Lock must wait and timeout
	err := m.Lock(ctx, "blocked")
	require.NoError(t, err)

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err = m.Lock(shortCtx, "blocked")
	assert.Error(t, err, "should fail due to context timeout")

	// Release the original lock
	m.Unlock("blocked")

	m.m.Lock()
	assert.Equal(t, 0, len(m.locks), "map should be empty — timed-out acquire must not leak")
	m.m.Unlock()
}

func TestMapMutex_ContextCancellationReturnsError(t *testing.T) {
	m := NewMapMutex[string]()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	// cancelled context has no deadline, so verifyContext returns error
	err := m.Lock(ctx, "key")
	assert.Error(t, err)
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
				useRead := rng.Intn(3) == 0  // 1/3 reads
				useShortTimeout := rng.Intn(5) == 0 // 1/5 short timeouts

				var timeout time.Duration
				if useShortTimeout {
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

	m.m.Lock()
	assert.Equal(t, 0, len(m.locks), "map should be empty after all goroutines complete")
	m.m.Unlock()
}
