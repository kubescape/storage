package utils

import (
	"context"
	"sync"
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
	done := make(chan struct{})
	go func() {
		m.ensureLock(key).Lock()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (m *MapMutex[T]) RLock(ctx context.Context, key T) error {
	done := make(chan struct{})
	go func() {
		m.ensureLock(key).RLock()
		close(done)
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}

func (m *MapMutex[T]) RUnlock(key T) {
	m.ensureLock(key).RUnlock()
}

func (m *MapMutex[T]) Unlock(key T) {
	m.ensureLock(key).Unlock()
}
