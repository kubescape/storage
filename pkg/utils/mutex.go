package utils

import (
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

func (m *MapMutex[T]) Lock(key T) {
	m.ensureLock(key).Lock()
}

func (m *MapMutex[T]) RLock(key T) {
	m.ensureLock(key).RLock()
}

func (m *MapMutex[T]) RUnlock(key T) {
	m.ensureLock(key).RUnlock()
}

func (m *MapMutex[T]) Unlock(key T) {
	m.ensureLock(key).Unlock()
}
