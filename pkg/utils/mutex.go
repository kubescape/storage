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

type MapMutex[T comparable] struct {
	locks map[T]*sync.RWMutex
	m     sync.Mutex
}

func NewMapMutex[T comparable]() MapMutex[T] {
	return MapMutex[T]{
		locks: make(map[T]*sync.RWMutex),
	}
}

// FIXME add a way to remove locks
func (m *MapMutex[T]) ensureLock(key T) *sync.RWMutex {
	m.m.Lock()
	defer m.m.Unlock()
	l, ok := m.locks[key]
	if !ok {
		l = &sync.RWMutex{}
		m.locks[key] = l
	}
	return l
}

func (m *MapMutex[T]) Lock(ctx context.Context, key T) error {
	done, err := verifyContext(ctx)
	if err != nil {
		return err
	}
	lock := m.ensureLock(key)
	ticker := backoff.NewTicker(backoff.NewExponentialBackOff())
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-done:
			// context has expired
			return ctx.Err()
		default:
		}
		if lock.TryLock() {
			// lock acquired
			return nil
		}
	}
	return TimeOutError
}

func (m *MapMutex[T]) RLock(ctx context.Context, key T) error {
	done, err := verifyContext(ctx)
	if err != nil {
		return err
	}
	lock := m.ensureLock(key)
	ticker := backoff.NewTicker(backoff.NewExponentialBackOff())
	defer ticker.Stop()
	for range ticker.C {
		select {
		case <-done:
			// context has expired
			return ctx.Err()
		default:
		}
		if lock.TryRLock() {
			// lock acquired
			return nil
		}
	}
	return TimeOutError
}

func (m *MapMutex[T]) RUnlock(key T) {
	m.ensureLock(key).RUnlock()
}

func (m *MapMutex[T]) Unlock(key T) {
	m.ensureLock(key).Unlock()
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
