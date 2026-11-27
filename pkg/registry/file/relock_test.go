package file

import (
	"context"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/utils"
	"github.com/stretchr/testify/require"
)

// A migration drops the read lock, and the caller's deferred RUnlock runs on every path,
// so the restore has to succeed even when the request is already cancelled and a writer
// holds the key: it waits for the writer and takes the lock, leaving the key whole.
func TestRestoreReadLockOutlivesACancelledRequest(t *testing.T) {
	s := &StorageImpl{locks: utils.NewMapMutex[string]()}
	const key = "/spdx.softwarecomposition.kubescape.io/containerprofiles/ns/name"
	require.NoError(t, s.locks.Lock(context.Background(), key))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	restored := make(chan struct{})
	go func() {
		s.restoreReadLock(ctx, key)
		close(restored)
	}()
	select {
	case <-restored:
		t.Fatal("the read lock was taken while a writer held the key")
	case <-time.After(50 * time.Millisecond):
	}

	s.locks.Unlock(key)
	select {
	case <-restored:
	case <-time.After(2 * time.Second):
		t.Fatal("the restore did not follow the writer's release")
	}

	// The caller's deferred RUnlock now has its matching acquire, and a writer gets the key.
	s.locks.RUnlock(key)
	lockCtx, lockCancel := context.WithTimeout(context.Background(), time.Second)
	defer lockCancel()
	require.NoError(t, s.locks.Lock(lockCtx, key))
	s.locks.Unlock(key)
}
