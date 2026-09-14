package file

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func shrinkKeyReserveBounds(t *testing.T, wait, drain time.Duration) {
	t.Helper()
	prevWait, prevDrain := keyReserveWaitMax, keyReserveDrainMax
	keyReserveWaitMax, keyReserveDrainMax = wait, drain
	t.Cleanup(func() { keyReserveWaitMax, keyReserveDrainMax = prevWait, prevDrain })
}

func TestSameSeries(t *testing.T) {
	const base = "/prefix/ns/replicaset-nginx-6d4cf56db6-nginx"
	require.True(t, sameSeries(base, base))
	require.True(t, sameSeries(base+"-3f9a", base))
	require.False(t, sameSeries(base+"3f9a", base))
	require.False(t, sameSeries("/prefix/ns/replicaset-nginx", base))
	require.False(t, sameSeries("/prefix/ns/other", base))
	require.False(t, sameSeries("/prefix/other/replicaset-nginx-6d4cf56db6-nginx", base))
}

// A write that starts while the series is reserved waits for the release;
// one that started before the reservation is waited for by the reserver.
func TestKeyReservations_WritersYieldAndReserverDrains(t *testing.T) {
	shrinkKeyReserveBounds(t, 5*time.Second, 5*time.Second)
	r := newKeyReservations()
	const base = "/p/ns/base"
	ctx := context.Background()

	// An in-flight writer on a TS key of the series.
	leaveInflight := r.enter(ctx, base+"-r7")

	reserved := make(chan struct{})
	var reservedCtx context.Context
	var release func()
	var drained bool
	go func() {
		reservedCtx, release, drained = r.reserve(ctx, base)
		close(reserved)
	}()
	select {
	case <-reserved:
		t.Fatal("reserve returned while a same-series write was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	leaveInflight()
	select {
	case <-reserved:
	case <-time.After(2 * time.Second):
		t.Fatal("reserve did not return after the in-flight write left")
	}
	require.True(t, drained)
	require.Equal(t, []string{base}, r.reservedKeys())

	// A new writer on the base key, and one on a TS key, block on the reservation.
	entered := make(chan string, 2)
	for _, k := range []string{base, base + "-r8"} {
		go func(k string) {
			leave := r.enter(ctx, k)
			entered <- k
			leave()
		}(k)
	}
	select {
	case k := <-entered:
		t.Fatalf("writer on %s entered while the series was reserved", k)
	case <-time.After(50 * time.Millisecond):
	}
	// A writer on another series is not affected.
	leaveOther := r.enter(ctx, "/p/ns/other")
	leaveOther()
	// The reserver's own writes (ctx carries the reservation) do not wait.
	leaveOwn := r.enter(reservedCtx, base)
	leaveOwn()

	release()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(2 * time.Second):
			t.Fatal("writer did not enter after the release")
		}
	}
	require.Empty(t, r.reservedKeys())
	release() // idempotent
}

// Both waits are bounded: a writer proceeds after keyReserveWaitMax even if
// the reservation is never released, and the reserver proceeds (drained=false)
// after keyReserveDrainMax even if an in-flight write never leaves.
func TestKeyReservations_BoundedWaits(t *testing.T) {
	shrinkKeyReserveBounds(t, 30*time.Millisecond, 30*time.Millisecond)
	r := newKeyReservations()
	const base = "/p/ns/base"
	ctx := context.Background()

	stuck := r.enter(ctx, base)
	t0 := time.Now()
	_, release, drained := r.reserve(ctx, base)
	require.False(t, drained)
	require.GreaterOrEqual(t, time.Since(t0), 30*time.Millisecond)

	t0 = time.Now()
	leave := r.enter(ctx, base+"-r1")
	require.GreaterOrEqual(t, time.Since(t0), 30*time.Millisecond)
	leave()
	release()
	stuck()
	require.Empty(t, r.reservedKeys())
}

// A cancelled writer ctx ends the wait at once.
func TestKeyReservations_WriterCtxCancel(t *testing.T) {
	shrinkKeyReserveBounds(t, 5*time.Second, 5*time.Second)
	r := newKeyReservations()
	const base = "/p/ns/base"
	_, release, _ := r.reserve(context.Background(), base)
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { r.enter(ctx, base)(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a cancelled writer kept waiting on the reservation")
	}
}

// A second reserver of the same key queues behind the first.
func TestKeyReservations_SameKeyReserversQueue(t *testing.T) {
	shrinkKeyReserveBounds(t, 5*time.Second, 5*time.Second)
	r := newKeyReservations()
	const base = "/p/ns/base"
	ctx := context.Background()
	_, release1, _ := r.reserve(ctx, base)
	second := make(chan struct{})
	var release2 func()
	go func() {
		_, release2, _ = r.reserve(ctx, base)
		close(second)
	}()
	select {
	case <-second:
		t.Fatal("second reserve returned while the first was held")
	case <-time.After(50 * time.Millisecond):
	}
	release1()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("second reserve did not return after the first released")
	}
	require.Equal(t, []string{base}, r.reservedKeys())
	release2()
	require.Empty(t, r.reservedKeys())
}
