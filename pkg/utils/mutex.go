package utils

import (
	"context"
	"errors"
	"sync"

	"github.com/cenkalti/backoff/v5"
)

var (
	ContextNilError            = errors.New("context is nil")
	ContextNotCancellableError = errors.New("context is not cancellable")
	ContextNoTimeoutError      = errors.New("context has no timeout")
	TimeOutError               = errors.New("lock acquisition timed out")
)

type refCountedLock struct {
	mu       sync.RWMutex
	refCount int // protected by MapMutex.m
}

type MapMutex[T comparable] struct {
	locks map[T]*refCountedLock
	m     sync.Mutex
}

func NewMapMutex[T comparable]() MapMutex[T] {
	return MapMutex[T]{
		locks: make(map[T]*refCountedLock),
	}
}

func (m *MapMutex[T]) acquire(key T) *refCountedLock {
	m.m.Lock()
	defer m.m.Unlock()
	l, ok := m.locks[key]
	if !ok {
		l = &refCountedLock{}
		m.locks[key] = l
	}
	l.refCount++
	return l
}

func (m *MapMutex[T]) release(key T) *refCountedLock {
	m.m.Lock()
	defer m.m.Unlock()
	l := m.locks[key]
	l.refCount--
	if l.refCount == 0 {
		delete(m.locks, key)
	}
	return l
}

func (m *MapMutex[T]) Lock(ctx context.Context, key T) error {
	done, err := verifyContext(ctx)
	if err != nil {
		return err
	}
	l := m.acquire(key)
	ticker := backoff.NewTicker(backoff.NewExponentialBackOff())
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-done:
			m.release(key)
			return ctx.Err()
		default:
		}
		if l.mu.TryLock() {
			return nil
		}
	}
	m.release(key)
	return TimeOutError
}

func (m *MapMutex[T]) RLock(ctx context.Context, key T) error {
	done, err := verifyContext(ctx)
	if err != nil {
		return err
	}
	l := m.acquire(key)
	ticker := backoff.NewTicker(backoff.NewExponentialBackOff())
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-done:
			m.release(key)
			return ctx.Err()
		default:
		}
		if l.mu.TryRLock() {
			return nil
		}
	}
	m.release(key)
	return TimeOutError
}

// release before unlock: release decrements refcount and may delete the map
// entry under the global lock. The per-key unlock happens after, on the
// returned pointer. Do NOT reorder — unlocking first would allow a concurrent
// acquire to find and reuse the entry before the refcount is decremented,
// preventing eviction.
func (m *MapMutex[T]) Unlock(key T) {
	l := m.release(key)
	l.mu.Unlock()
}

func (m *MapMutex[T]) RUnlock(key T) {
	l := m.release(key)
	l.mu.RUnlock()
}

func verifyContext(ctx context.Context) (<-chan struct{}, error) {
	if ctx == nil {
		return nil, ContextNilError
	}
	if _, ok := ctx.Deadline(); !ok {
		return nil, ContextNoTimeoutError
	}
	done := ctx.Done()
	if done == nil {
		return nil, ContextNotCancellableError
	}
	return done, nil
}
