package file

// Consolidation's fairness escalation against a same-series writer (design
// §3.7 Phase 3: "conflict → one re-read and retry, N=2").
//
// The pass's Phase 1 read → merge → encode → gate → commit window is longer
// than a REST writer's whole read → encode → commit cycle, so a writer
// committing base updates back-to-back on the same key beats the pass's CAS
// on every attempt: the once-retry re-runs the same window and loses again,
// and nothing escalates across ticks. The gate's lanes do not help — a single
// writer never has a second commit queued while its first holds the gate, so
// the pass loses in its prepare window, outside the gate, not in the queue.
//
// The escalation is a per-series reservation the pass takes for its retry
// only, after a real conflict. While it is held, an ObjectStore write to the
// series (the base key or one of its TS keys) that has not started yet waits
// for the retry to end (bounded by keyReserveWaitMax), and the retry itself
// starts only once every same-series write already in flight has finished
// (bounded by keyReserveDrainMax). Absent those two timeouts, no same-series
// write commits inside the retry's window, so its CAS cannot fail from one:
// the series consolidates within the tick, at attempt 2. The reservation is
// released as soon as the retry ends, whichever way, so consolidation never
// holds writers across ticks; a writer whose wait times out proceeds anyway.
// The gate's priority scheme is untouched: writers to any other series are
// not affected at all.

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/kubescape/storage/pkg/metrics"
)

// Package-level so tests can shrink them.
var (
	// keyReserveWaitMax bounds an ordinary writer's wait on a series
	// reservation in enter() (retry-vs-write: common, on every write to a
	// contended series). Tier B (droplet run, b8e42f76) showed a 1s cap
	// here shifting +47% onto update-p95-ms; a clean dedicated-CPU rerun
	// at 200ms (commit d4e8090d) still showed +70.6%. 50ms fixed it
	// (-9% to -10.5% across two runs) with no correctness cost: the
	// starvation-fix regression tests still show consolidation reserving
	// and committing every round via a normal release, never a timeout.
	keyReserveWaitMax = 50 * time.Millisecond
	// keyReserveQueueMax bounds one consolidation retry's wait to reserve
	// a series another retry already holds, in reserve() (retry-vs-retry:
	// overlapping ticks on the same series, rarer than every write). This
	// used to share keyReserveWaitMax with the writer-wait above; shrinking
	// that to 50ms for update-p95-ms made overlapping retries give up and
	// run unreserved far more often, producing extra unconsolidated/
	// duplicate TS rows that List's scan/merge then paid for — list-p95-ms
	// regressed +93.7% to +121.1% even though bumping the SQLite pool size
	// (LOAD_POOL) made it worse, not better, ruling out pool contention as
	// the cause. Split into its own constant, left generous like
	// keyReserveDrainMax: retry-vs-retry collisions are uncommon enough
	// that patiently waiting here costs little, and correctness (never
	// racing two retries unreserved against each other) matters more than
	// shaving this specific wait.
	keyReserveQueueMax = time.Second
	// keyReserveDrainMax bounds the reserving pass's wait for in-flight
	// same-series writes to finish. Left at 1s: this is the tail-latency
	// bound that fixed consolidation starvation and must stay generous.
	keyReserveDrainMax = time.Second
)

type keyReservationCtxKey struct{}

// keyReservation is one series reserved by a consolidation retry.
type keyReservation struct {
	key      string
	released chan struct{}
	// drained is signalled (non-blocking) by every leave of a same-series
	// write; the reserver re-counts on each signal.
	drained chan struct{}
	once    sync.Once
}

// keyReservations tracks reserved series and in-flight writes per key.
type keyReservations struct {
	mu       sync.Mutex
	reserved map[string]*keyReservation
	inflight map[string]int
}

func newKeyReservations() *keyReservations {
	return &keyReservations{reserved: map[string]*keyReservation{}, inflight: map[string]int{}}
}

// sameSeries reports whether key is base itself or one of its TS keys
// (base + "-" + suffix, SplitProfileName's shape). A base named with a
// trailing "-x" segment matches another base's series by prefix; the cost is
// one bounded wait during that series' reserved retry, never a wrong write.
func sameSeries(key, base string) bool {
	return key == base || (len(key) > len(base) && key[len(base)] == '-' && strings.HasPrefix(key, base))
}

// enter registers a write to key as in flight and returns its leave. When a
// reservation covers key's series, the write first waits for it to be
// released (bounded by keyReserveWaitMax and ctx); the reserving pass's own
// writes (ctx carries the reservation) never wait on it.
func (r *keyReservations) enter(ctx context.Context, key string) (leave func()) {
	own, _ := ctx.Value(keyReservationCtxKey{}).(*keyReservation)
	waited := false
	for {
		r.mu.Lock()
		var blocking *keyReservation
		for _, res := range r.reserved {
			if res != own && sameSeries(key, res.key) {
				blocking = res
				break
			}
		}
		if blocking == nil || waited {
			r.inflight[key]++
			r.mu.Unlock()
			return func() { r.leave(key) }
		}
		r.mu.Unlock()

		timer := time.NewTimer(keyReserveWaitMax)
		select {
		case <-blocking.released:
			timer.Stop()
			metrics.IncCPKeyYield(metrics.KeyYieldReleased)
		case <-timer.C:
			metrics.IncCPKeyYield(metrics.KeyYieldTimeout)
			waited = true
		case <-ctx.Done():
			timer.Stop()
			// The write fails on its own ctx at the next step; register it so
			// the leave stays uniform.
			waited = true
		}
	}
}

func (r *keyReservations) leave(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.inflight[key] <= 1 {
		delete(r.inflight, key)
	} else {
		r.inflight[key]--
	}
	for _, res := range r.reserved {
		if sameSeries(key, res.key) {
			select {
			case res.drained <- struct{}{}:
			default:
			}
		}
	}
}

// reserve reserves key's series for the caller and waits for in-flight
// same-series writes to finish (bounded by keyReserveDrainMax and ctx). It
// returns the ctx the reserving pass must use for its own writes and the
// release, which is idempotent and must be called. drained is false when the
// drain timed out: writes were still in flight when the reservation began.
func (r *keyReservations) reserve(ctx context.Context, key string) (reservedCtx context.Context, release func(), drained bool) {
	res := &keyReservation{key: key, released: make(chan struct{}), drained: make(chan struct{}, 1)}
	for {
		r.mu.Lock()
		prev, taken := r.reserved[key]
		if !taken {
			r.reserved[key] = res
			r.mu.Unlock()
			break
		}
		r.mu.Unlock()
		// Another pass reserved the same key (two overlapping ticks): queue
		// behind it, bounded; past the bound the retry runs unreserved.
		timer := time.NewTimer(keyReserveQueueMax)
		select {
		case <-prev.released:
			timer.Stop()
		case <-timer.C:
			return ctx, func() {}, false
		case <-ctx.Done():
			timer.Stop()
			return ctx, func() {}, false
		}
	}
	release = func() { r.mu.Lock(); res.close(r); r.mu.Unlock() }

	timer := time.NewTimer(keyReserveDrainMax)
	defer timer.Stop()
	drained = true
	for r.inflightSameSeries(key) > 0 {
		select {
		case <-res.drained:
		case <-timer.C:
			return context.WithValue(ctx, keyReservationCtxKey{}, res), release, false
		case <-ctx.Done():
			return context.WithValue(ctx, keyReservationCtxKey{}, res), release, false
		}
	}
	return context.WithValue(ctx, keyReservationCtxKey{}, res), release, drained
}

// close removes res from the map (if still registered) and releases its
// waiters. Caller holds r.mu.
func (res *keyReservation) close(r *keyReservations) {
	res.once.Do(func() {
		if r.reserved[res.key] == res {
			delete(r.reserved, res.key)
		}
		close(res.released)
	})
}

func (r *keyReservations) inflightSameSeries(base string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for key, c := range r.inflight {
		if sameSeries(key, base) {
			n += c
		}
	}
	return n
}

// reservedKeys reports the currently reserved series, for tests.
func (r *keyReservations) reservedKeys() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.reserved))
	for k := range r.reserved {
		out = append(out, k)
	}
	return out
}
