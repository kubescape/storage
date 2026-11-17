package file

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

func testManifestKey(name string) string {
	return "/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifests/ns1/" + name
}

// newStaleRowTestStorage returns a single-writer StorageImpl, optionally
// sharing a write gate, so every test here runs on both commit paths.
func newStaleRowTestStorage(t *testing.T, gated bool) *StorageImpl {
	t.Helper()
	enableSingleWriter(t)
	si, cleanup := newSingleWriterTestStorage(t)
	t.Cleanup(cleanup)
	if gated {
		gate, err := newWriteGate(context.Background(), si.pool)
		require.NoError(t, err)
		t.Cleanup(func() { _ = gate.Close() })
		si.SetWriteGate(gate)
	}
	return si
}

// writeRow runs fn as a metadata write the way production sites do: through
// the gate when there is one, on a pool connection otherwise.
func writeRow(t *testing.T, si *StorageImpl, key string, fn func(conn *sqlite.Conn) error) {
	t.Helper()
	ctx := context.Background()
	if si.gate != nil {
		require.NoError(t, si.write(ctx, nil, priorityHigh, holdPathLegacyCommit, resourceFromKey(key), true,
			func(_ context.Context, conn *sqlite.Conn) error { return fn(conn) }))
		return
	}
	conn, err := si.pool.Take(ctx)
	require.NoError(t, err)
	defer si.pool.Put(conn)
	require.NoError(t, fn(conn))
}

// seedManifest creates a manifest and updates it until its resourceVersion is
// payloadRV, then rewrites only the metadata row to say rowRV. That is the
// state objects written by old releases can be left in: a full Get returns
// payloadRV, a metadata-only Get and the commit check see rowRV.
func seedManifest(t *testing.T, si *StorageImpl, name string, payloadRV, rowRV int) string {
	t.Helper()
	ctx := context.Background()
	key := testManifestKey(name)
	obj := &softwarecomposition.VulnerabilityManifest{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns1", Labels: map[string]string{"n": "1"}},
	}
	require.NoError(t, si.Create(ctx, key, obj, &softwarecomposition.VulnerabilityManifest{}, 0))
	for rv := 2; rv <= payloadRV; rv++ {
		require.NoError(t, si.GuaranteedUpdate(ctx, key, &softwarecomposition.VulnerabilityManifest{}, false, nil, setManifestLabel("n", strconv.Itoa(rv)), nil))
	}
	full := &softwarecomposition.VulnerabilityManifest{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, full))
	require.Equal(t, strconv.Itoa(payloadRV), full.ResourceVersion)

	full.ResourceVersion = strconv.Itoa(rowRV)
	row := extractFields(full, []string{"ObjectMeta", "SchemaVersion"})
	writeRow(t, si, key, func(conn *sqlite.Conn) error { return writeMetadata(conn, key, row) })
	return key
}

func setManifestLabel(k, v string) storage.UpdateFunc {
	return func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		cur := input.(*softwarecomposition.VulnerabilityManifest).DeepCopy()
		if cur.Labels == nil {
			cur.Labels = map[string]string{}
		}
		cur.Labels[k] = v
		return cur, nil, nil
	}
}

func getRV(t *testing.T, si *StorageImpl, key string, opts storage.GetOptions) string {
	t.Helper()
	got := &softwarecomposition.VulnerabilityManifest{}
	require.NoError(t, si.Get(context.Background(), key, opts, got))
	return got.ResourceVersion
}

func forEachCommitPath(t *testing.T, fn func(t *testing.T, gated bool)) {
	t.Run("ungated", func(t *testing.T) { fn(t, false) })
	t.Run("gated", func(t *testing.T) { fn(t, true) })
}

// TestSingleWriter_StaleMetadataRow_UpdateSucceedsAndRealigns replays what a
// client sees on an object whose metadata row and payload disagree on the
// resourceVersion. An update prepared from the metadata row is refused (the
// client's version is not the one a full read returns). The client then
// re-reads the full object and updates again. That second update used to
// conflict at commit on every attempt, because the commit compared the
// payload's version against the row's, and retried until the request deadline.
func TestSingleWriter_StaleMetadataRow_UpdateSucceedsAndRealigns(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rowRV int
	}{
		{name: "row behind payload", rowRV: 1},
		{name: "row ahead of payload", rowRV: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forEachCommitPath(t, func(t *testing.T, gated bool) {
				si := newStaleRowTestStorage(t, gated)
				const payloadRV = 3
				key := seedManifest(t, si, "stale-row", payloadRV, tc.rowRV)
				require.Equal(t, strconv.Itoa(tc.rowRV), getRV(t, si, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}))

				// Step 1: an update carrying the row's version is a plain conflict.
				clientRV := strconv.Itoa(tc.rowRV)
				err := si.GuaranteedUpdate(context.Background(), key, &softwarecomposition.VulnerabilityManifest{}, false, nil,
					func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						if cur := input.(*softwarecomposition.VulnerabilityManifest); cur.ResourceVersion != clientRV {
							return nil, nil, apierrors.NewConflict(schema.GroupResource{Resource: "vulnerabilitymanifests"}, cur.Name, nil)
						}
						return input, nil, nil
					}, nil)
				require.True(t, apierrors.IsConflict(err), "expected a conflict, got %v", err)

				// Step 2: the update prepared from a full read must commit.
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				out := &softwarecomposition.VulnerabilityManifest{}
				require.NoError(t, si.GuaranteedUpdate(ctx, key, out, false, nil, setManifestLabel("n", "updated"), nil))
				require.Equal(t, "updated", out.Labels["n"])

				// The new version follows the payload's, the version every full
				// read returned. With the row ahead this lands below the old row
				// version; pinned here so a change to that choice is deliberate.
				want := strconv.Itoa(payloadRV + 1)
				require.Equal(t, want, out.ResourceVersion)
				require.Equal(t, want, getRV(t, si, key, storage.GetOptions{}))
				require.Equal(t, want, getRV(t, si, key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}),
					"the commit must realign the metadata row with the payload")

				// Realigned: the next update takes the ordinary path.
				require.NoError(t, si.GuaranteedUpdate(ctx, key, out, false, nil, setManifestLabel("n", "again"), nil))
				require.Equal(t, strconv.Itoa(payloadRV+2), out.ResourceVersion)
			})
		})
	}
}

// TestSingleWriter_PayloadWithoutMetadataRow_UpdateFailsFast covers the state
// the commit cannot repair: the payload is readable but its metadata row is
// gone. Every commit conflicts and every re-read returns the same version, so
// the update must give up with a conflict instead of retrying until the
// request deadline.
func TestSingleWriter_PayloadWithoutMetadataRow_UpdateFailsFast(t *testing.T) {
	forEachCommitPath(t, func(t *testing.T, gated bool) {
		si := newStaleRowTestStorage(t, gated)
		key := seedManifest(t, si, "no-row", 2, 2)
		writeRow(t, si, key, func(conn *sqlite.Conn) error { return DeleteMetadata(conn, key, nil) })

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		start := time.Now()
		err := si.GuaranteedUpdate(ctx, key, &softwarecomposition.VulnerabilityManifest{}, false, nil, setManifestLabel("n", "updated"), nil)
		require.Error(t, err)
		require.True(t, storage.IsConflict(err), "expected a resourceVersion conflict, got %v", err)
		require.NoError(t, ctx.Err(), "the update must fail before the request deadline")
		require.Less(t, time.Since(start), 2*time.Second)
	})
}
