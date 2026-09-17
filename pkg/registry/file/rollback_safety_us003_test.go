package file

// Tests for US-003 of .omc/plans/rollback-safety-guard.md, Part 4: the
// rv-column/metadata-JSON invariant Part 2's read fallback depends on (M3),
// and the positive consolidation-tick case for Part 4's INV-2 argument.
//
// Both tests exercise real production write/consolidation paths against a
// fallback-eligible key (a metadata row with rv non-NULL, is_time_series=0,
// a payloads row, and no .g file -- the exact shape US-002's fallback
// serves), never storage.go's internals directly.

import (
	"context"
	"errors"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage"
)

// TestSaveContainerProfile_FirstWriteAfterFallbackSucceedsWithoutConflict is
// the M3 regression named in the plan's Part 4: the fallback stamps
// ResourceVersion from the metadata row's rv COLUMN
// (serveFromPayloadsFallback, storage.go), but GuaranteedUpdate's CAS check
// at commit time reads the base resourceVersion from the metadata JSON BLOB
// (readCurrentResourceVersion, singlewriter.go). These two sources currently
// always agree because every rv-writing site derives both the column and the
// blob's resourceVersion field from one stamped object before committing --
// but that was never proven end-to-end. If the two sources ever disagreed,
// the symptom would be a PERMANENT write-conflict loop on that key, so this
// test drives the real SaveContainerProfile -> guaranteedUpdateSingleWriter
// -> commit path (not the internal helpers in isolation) and asserts the
// first attempt succeeds, with no retry and no errWriteConflict.
func TestSaveContainerProfile_FirstWriteAfterFallbackSucceedsWithoutConflict(t *testing.T) {
	si, cleanup := newSingleWriterTestStorage(t)
	t.Cleanup(cleanup)
	cps := NewContainerProfileStorageImpl(si, si.pool)
	ctx := context.Background()

	key := testProfileKey("fallback-write")
	const uid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

	// The object an ObjectStore-era write would have stamped: rv is carried
	// both in the metadata row's rv column AND in the metadata JSON blob's
	// resourceVersion field, exactly as every real rv-writing site
	// (sqliteobject_store.go, sqliteobject_migration.go) derives both from
	// one object before committing.
	existing := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "fallback-write",
			Namespace: "ns1",
			UID:       types.UID(uid),
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey:     helpersv1.Learning,
				helpersv1.CompletionMetadataKey: helpersv1.Partial,
			},
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Execs: []softwarecomposition.ExecCalls{{Path: "/bin/original", Args: []string{"original"}}},
		},
	}
	require.NoError(t, storage.APIObjectVersioner{}.UpdateObject(existing, 100))

	conn, err := si.pool.Take(ctx)
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, existing, 100, false, uid, false)
	seedDecodablePayloadsRow(t, conn, si, key, existing)
	si.pool.Put(conn)
	// Deliberately no .g file: this key is fallback-eligible (US-002's
	// 4-condition predicate holds), not a normal legacy-write key.

	// Confirm fallback-eligible before the write, so this test does not pass
	// vacuously: a plain Get must serve the object from its payloads body,
	// stamped with rv=100 from the metadata row's rv column.
	before := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, before))
	require.Equal(t, "100", before.ResourceVersion, "the key must be fallback-eligible before the write, or this test passes vacuously")

	updated := existing.DeepCopy()
	updated.Spec.Execs = append(updated.Spec.Execs, softwarecomposition.ExecCalls{Path: "/bin/new", Args: []string{"new"}})

	// The real production call path: SaveContainerProfile ->
	// guaranteedUpdateSingleWriter -> the single writer's prepare (reads the
	// fallback-served object, rv=100) and commit (re-reads the current
	// resourceVersion from the metadata JSON blob) phases.
	err = cps.SaveContainerProfile(ctx, key, updated)
	require.NoError(t, err, "a SaveContainerProfile on a fallback-eligible key must succeed on the first attempt")
	assert.False(t, errors.Is(err, errWriteConflict), "must not be a write-conflict error")

	// The write actually landed and the key left the fallback-eligible set
	// (there is now a .g file backing it).
	after := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, after))
	assert.Equal(t, "101", after.ResourceVersion, "resourceVersion must have advanced by exactly one, not retried")
	assert.True(t, specHasExec(after.Spec, "new"), "the write's content must have landed")
	assert.Equal(t, uid, string(after.UID))

	_, err = si.appFs.Stat(getStoredPayloadFilepath(si.root, key))
	assert.NoError(t, err, "a successful write must leave a .g file behind, taking the key out of the fallback-eligible set")
}

// TestConsolidation_FallbackServedKeyMergesCorrectlyWithoutDivergence is the
// positive case for Part 4's INV-2 argument: a payloads-only key (rv
// non-NULL, is_time_series=0, a payloads row, no .g file -- fallback-eligible
// per US-002's predicate) must, under a REAL consolidation tick, merge
// correctly on top of its fallback-served content (not a synthesized-empty
// profile, and not a new UID) and must not be observed as a divergence --
// the specific failure mode (an earlier design iteration where the fallback
// looked like a divergence to HealDivergence/the consolidation pass's
// divergence-heal arms) this design closes by construction.
func TestConsolidation_FallbackServedKeyMergesCorrectlyWithoutDivergence(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "us003-fallback"
	key := lane0Key(ns, name)
	const uid = "11111111-2222-3333-4444-555555555555"

	// The ObjectStore-era content already persisted for this key: rv=50 in
	// both the metadata row's rv column and the metadata JSON blob (as every
	// real rv-writing site stamps them), a payloads row carrying it, and no
	// .g file -- the exact shape US-002's fallback serves.
	original := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			UID:       types.UID(uid),
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey:     helpersv1.Learning,
				helpersv1.CompletionMetadataKey: helpersv1.Partial,
				helpersv1.InstanceIDMetadataKey: lane0InstanceID,
			},
		},
		Spec: lane0Spec("original"),
	}
	require.NoError(t, storage.APIObjectVersioner{}.UpdateObject(original, 50))

	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	seedMetadataRow(t, conn, key, original, 50, false, uid, false)
	seedDecodablePayloadsRow(t, conn, h.s, key, original)
	h.pool.Put(conn)

	// Confirm fallback-eligible before the tick, so the test does not pass
	// vacuously: a plain Get must serve the original content.
	before := h.readPayload(t, key)
	require.Equal(t, uid, string(before.UID))
	require.True(t, specHasExec(before.Spec, "original"))

	// A time series with data, so ConsolidateTimeSeries's list queries pick
	// up this key and actually run a tick against it.
	h.seedTsRow(t, ns, name, "A", "1", h.ts(1), lane0ZeroTime, helpersv1.Learning, helpersv1.Partial, true)
	h.writeTsObject(t, key, "1", "new", true)

	c0 := snapshotCounters(t)

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	// (a) The persisted profile still carries the original content and UID
	// -- proof that loadOrInitializeProfile's GetContainerProfile served the
	// fallback-read original object (not storage.IsNotFound, which would
	// have synthesized a brand-new, empty-Spec profile with a fresh UID).
	after := h.readPayload(t, key)
	assert.Equal(t, uid, string(after.UID), "the fallback-served profile's UID must survive the tick, not a synthesized-empty one's")
	assert.True(t, specHasExec(after.Spec, "original"), "the original fallback-served content must still be present")
	assert.True(t, specHasExec(after.Spec, "new"), "the tick's new time-series data must have merged in")

	// (b) No divergence metric fired: the fallback-served state must not
	// look like a payload/metadata divergence to consolidateKeyTimeSeriesOnce's
	// divergence check or trigger HealDivergence.
	d := c0.delta(t)
	assert.Zero(t, d.payloadAhead, "the fallback-served key must not be observed as payload-ahead")
	assert.Zero(t, d.metadataAhead, "the fallback-served key must not be observed as metadata-ahead")
}
