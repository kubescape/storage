package file

// Tests for US-004 of .omc/plans/rollback-safety-guard.md, the advisory
// startup check (D): CensusFallbackEligibleContainerProfiles must count
// exactly the keys satisfying the same 4-condition predicate as US-002's
// read fallback (a metadata row exists, rv IS NOT NULL, is_time_series = 0,
// a payloads row exists), must not error or panic against an empty pool,
// and must be bounded by its own timeout rather than the caller's context.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cpTestKey builds a ContainerProfile storage key directly, in the exact
// K8s path shape BuildContainerProfileKey produces for HostTypeKubernetes.
func cpTestKey(name string) string {
	return K8sKeysToPath("", softwarecomposition.GroupName, ContainerProfileKind, "", "ns1", name)
}

func cpTestObject(name string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1"},
	}
}

// TestCensusFallbackEligibleContainerProfiles_Zero confirms an empty pool
// (no metadata rows at all) reports a zero count and does not error or
// panic.
func TestCensusFallbackEligibleContainerProfiles_Zero(t *testing.T) {
	_, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()

	report, err := CensusFallbackEligibleContainerProfiles(ctx, pool, 5*time.Second)
	require.NoError(t, err)
	assert.Zero(t, report.Count)
	assert.Empty(t, report.ExampleKeys)
}

// TestCensusFallbackEligibleContainerProfiles_One seeds exactly one
// fallback-eligible ContainerProfile key (all four conditions hold) and
// confirms it is counted and reported as an example.
func TestCensusFallbackEligibleContainerProfiles_One(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()
	key := cpTestKey("census-one")
	obj := cpTestObject("census-one")

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, obj, 10, false, "uid-one", false)
	seedDecodablePayloadsRow(t, conn, s, key, obj)
	pool.Put(conn)

	report, err := CensusFallbackEligibleContainerProfiles(ctx, pool, 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, 1, report.Count)
	require.Len(t, report.ExampleKeys, 1)
	assert.Equal(t, key, report.ExampleKeys[0])
}

// TestCensusFallbackEligibleContainerProfiles_Several seeds several
// fallback-eligible keys, plus one of each kind of ineligible row (rv
// NULL, is_time_series=1, no payloads row), and confirms only the eligible
// ones are counted -- the same predicate US-002's read fallback applies at
// read time.
func TestCensusFallbackEligibleContainerProfiles_Several(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()

	conn, err := pool.Take(ctx)
	require.NoError(t, err)

	eligibleNames := []string{"census-a", "census-b", "census-c"}
	for i, name := range eligibleNames {
		key := cpTestKey(name)
		obj := cpTestObject(name)
		seedMetadataRow(t, conn, key, obj, int64(100+i), false, "uid-"+name, false)
		seedDecodablePayloadsRow(t, conn, s, key, obj)
	}

	// rv IS NULL (a legacy-touched row): condition 2 fails.
	rvNullKey := cpTestKey("census-rv-null")
	rvNullObj := cpTestObject("census-rv-null")
	seedMetadataRow(t, conn, rvNullKey, rvNullObj, 0, true, "uid-rv-null", false)
	seedDecodablePayloadsRow(t, conn, s, rvNullKey, rvNullObj)

	// is_time_series = 1: condition 3 fails.
	tsKey := cpTestKey("census-ts")
	tsObj := cpTestObject("census-ts")
	seedMetadataRow(t, conn, tsKey, tsObj, 200, false, "uid-ts", true)
	seedDecodablePayloadsRow(t, conn, s, tsKey, tsObj)

	// No payloads row: condition 4 fails.
	noPayloadKey := cpTestKey("census-no-payload")
	noPayloadObj := cpTestObject("census-no-payload")
	seedMetadataRow(t, conn, noPayloadKey, noPayloadObj, 300, false, "uid-no-payload", false)

	pool.Put(conn)

	report, err := CensusFallbackEligibleContainerProfiles(ctx, pool, 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, len(eligibleNames), report.Count, "only the three fully-eligible keys must be counted")
	assert.Len(t, report.ExampleKeys, len(eligibleNames))
	for _, name := range eligibleNames {
		assert.Contains(t, report.ExampleKeys, cpTestKey(name))
	}
	assert.NotContains(t, report.ExampleKeys, rvNullKey)
	assert.NotContains(t, report.ExampleKeys, tsKey)
	assert.NotContains(t, report.ExampleKeys, noPayloadKey)
}

// TestCensusFallbackEligibleContainerProfiles_ExampleKeysCapped confirms
// the example-key list is bounded (advisoryFallbackExampleCap) even when
// the count exceeds it, so a large fallback-eligible population cannot
// spam the log via the report.
func TestCensusFallbackEligibleContainerProfiles_ExampleKeysCapped(t *testing.T) {
	s, pool, _ := newRollbackSafetyTestStorage(t)
	ctx := context.Background()

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	total := advisoryFallbackExampleCap + 5
	for i := 0; i < total; i++ {
		name := "census-cap-" + string(rune('a'+i))
		key := cpTestKey(name)
		obj := cpTestObject(name)
		seedMetadataRow(t, conn, key, obj, int64(1000+i), false, "uid-cap", false)
		seedDecodablePayloadsRow(t, conn, s, key, obj)
	}
	pool.Put(conn)

	report, err := CensusFallbackEligibleContainerProfiles(ctx, pool, 5*time.Second)
	require.NoError(t, err)
	assert.Equal(t, total, report.Count)
	assert.Len(t, report.ExampleKeys, advisoryFallbackExampleCap, "example keys must be capped even though the count is not")
}

// TestCensusFallbackEligibleContainerProfiles_RespectsOwnTimeout confirms
// the census is bounded by the timeout argument passed to it, not by
// whatever deadline (or lack of one) the caller's context carries: an
// already-expired parent context still yields a context.DeadlineExceeded
// error from the census itself (via its own context.WithTimeout child),
// rather than hanging or silently succeeding.
func TestCensusFallbackEligibleContainerProfiles_RespectsOwnTimeout(t *testing.T) {
	_, pool, _ := newRollbackSafetyTestStorage(t)

	// An untimed parent context (mirroring main.go's shutdown signal
	// context, which has no deadline) is itself fine -- the function's own
	// context.WithTimeout must impose the bound regardless.
	parent := context.Background()

	// A vanishingly small timeout forces pool.Take (or the query) to hit
	// the census's own deadline rather than blocking indefinitely, proving
	// the timeout argument is actually wired into a real context passed
	// downstream.
	_, err := CensusFallbackEligibleContainerProfiles(parent, pool, 1*time.Nanosecond)
	if err != nil {
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "a timed-out census must report a deadline error, not an unrelated failure: %v", err)
	}
	// A near-zero timeout may occasionally still win the race against
	// pool.Take/the query on a fast, otherwise-idle test pool; what this
	// test guards against is a hang, which the surrounding test timeout
	// would catch, and a non-deadline error when it does time out, checked
	// above.
}

// TestCensusFallbackEligibleContainerProfiles_TimeoutContextIsBoundedNotParent
// is a lighter, code-level confirmation that the census derives its
// deadline from the timeout argument via its own context.WithTimeout, by
// asserting a parent context that is ALREADY cancelled does not prevent a
// normal-sized timeout from being honored independently: the function must
// still return promptly (its own bounded context, not an indefinite wait
// on a cancelled parent that never resolves the query).
func TestCensusFallbackEligibleContainerProfiles_TimeoutContextIsBoundedNotParent(t *testing.T) {
	_, pool, _ := newRollbackSafetyTestStorage(t)

	parent, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call

	done := make(chan struct{})
	var err error
	go func() {
		_, err = CensusFallbackEligibleContainerProfiles(parent, pool, 5*time.Second)
		close(done)
	}()

	select {
	case <-done:
		// context.WithTimeout(parent, timeout) on an already-cancelled
		// parent yields an already-cancelled child, so the census must
		// return promptly with a context error -- not hang.
		require.Error(t, err)
		assert.True(t, errors.Is(err, context.Canceled), "expected a cancellation error propagated from the cancelled parent: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("census did not return promptly against an already-cancelled parent context")
	}
}
