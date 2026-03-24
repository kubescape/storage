package utils

import (
	"context"
	"errors"
	"sync"
)

var ContextNilError = errors.New("context is nil")

type keyState struct {
	cond           *sync.Cond
	readers        int
	writer         bool
	waiters        int // goroutines blocked in lockSlow (for eviction)
	pendingWriters int // writers waiting to acquire (blocks new readers)
}

type MapMutex[T comparable] struct {
	mu   sync.Mutex
	keys map[T]*keyState
}

func NewMapMutex[T comparable]() MapMutex[T] {
	return MapMutex[T]{
		keys: make(map[T]*keyState),
	}
}

// getOrCreate returns the keyState for key, creating one if needed.
// Must be called with m.mu held.
func (m *MapMutex[T]) getOrCreate(key T) *keyState {
	s, ok := m.keys[key]
	if !ok {
		s = &keyState{cond: sync.NewCond(&m.mu)}
		m.keys[key] = s
	}
	return s
}

// maybeEvict removes the keyState from the map if nobody holds or waits on it.
// Must be called with m.mu held.
func (m *MapMutex[T]) maybeEvict(key T, s *keyState) {
	if s.readers == 0 && !s.writer && s.waiters == 0 {
		delete(m.keys, key)
	}
}

// lockSlow is the shared slow path for Lock and RLock. The caller must hold
// m.mu and have already checked that the fast path doesn't apply.
// canProceed checks whether the lock can be acquired; onAcquire updates state
// on success; onCancel (if non-nil) runs on context cancellation for cleanup
// (e.g. decrementing pendingWriters).
func (m *MapMutex[T]) lockSlow(
	ctx context.Context, key T, s *keyState,
	canProceed func() bool, onAcquire func(), onCancel func(),
) error {
	s.waiters++

	// Spawn a goroutine to broadcast on context cancellation so that
	// Cond.Wait wakes up and can observe ctx.Err(). The goroutine exits
	// immediately in all cases (no leak).
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			s.cond.Broadcast()
			m.mu.Unlock()
		case <-stop:
		}
	}()

	for !canProceed() {
		if ctx.Err() != nil {
			s.waiters--
			if onCancel != nil {
				onCancel()
			}
			m.maybeEvict(key, s)
			m.mu.Unlock()
			close(stop)
			return ctx.Err()
		}
		s.cond.Wait() // releases m.mu while sleeping
	}

	s.waiters--
	onAcquire()
	m.mu.Unlock()
	close(stop)
	return nil
}

func (m *MapMutex[T]) Lock(ctx context.Context, key T) error {
	if ctx == nil {
		return ContextNilError
	}
	m.mu.Lock()
	s := m.getOrCreate(key)
	if !s.writer && s.readers == 0 {
		s.writer = true
		m.mu.Unlock()
		return nil
	}
	s.pendingWriters++
	return m.lockSlow(ctx, key, s,
		func() bool { return !s.writer && s.readers == 0 },
		func() { s.pendingWriters--; s.writer = true },
		func() { s.pendingWriters-- },
	)
}

func (m *MapMutex[T]) RLock(ctx context.Context, key T) error {
	if ctx == nil {
		return ContextNilError
	}
	m.mu.Lock()
	s := m.getOrCreate(key)
	if !s.writer && s.pendingWriters == 0 {
		s.readers++
		m.mu.Unlock()
		return nil
	}
	return m.lockSlow(ctx, key, s,
		func() bool { return !s.writer && s.pendingWriters == 0 },
		func() { s.readers++ },
		nil,
	)
}

func (m *MapMutex[T]) Unlock(key T) {
	m.mu.Lock()
	s := m.keys[key]
	s.writer = false
	s.cond.Broadcast()
	m.maybeEvict(key, s)
	m.mu.Unlock()
}

func (m *MapMutex[T]) RUnlock(key T) {
	m.mu.Lock()
	s := m.keys[key]
	s.readers--
	if s.readers == 0 {
		s.cond.Broadcast()
	}
	m.maybeEvict(key, s)
	m.mu.Unlock()
}
