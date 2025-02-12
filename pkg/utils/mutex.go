package utils

import (
	"fmt"
	"sync"
	"time"
)

type MapMutex[T comparable] struct {
	locks      map[T]*sync.RWMutex
	maxTimeout time.Duration
	m          sync.Mutex
}

func NewMapMutex[T comparable](maxTimeout time.Duration) MapMutex[T] {
	return MapMutex[T]{
		locks:      make(map[T]*sync.RWMutex),
		maxTimeout: maxTimeout,
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

func (m *MapMutex[T]) Lock(key T) error {
	done := make(chan struct{})
	go func() {
		m.ensureLock(key).Lock()
		close(done)
	}()

	select {
	case <-time.After(m.maxTimeout):
		return fmt.Errorf("lock timeout")
	case <-done:
		return nil
	}
}

func (m *MapMutex[T]) RLock(key T) error {
	done := make(chan struct{})
	go func() {
		m.ensureLock(key).RLock()
		close(done)
	}()

	select {
	case <-time.After(m.maxTimeout):
		return fmt.Errorf("lock timeout")
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
