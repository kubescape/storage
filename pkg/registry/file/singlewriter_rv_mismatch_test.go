package file

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

// desyncMetadataRV rewrites key's metadata row so its resourceVersion is rv
// while the payload file keeps its own. Storage before v0.0.306 could leave
// objects in this state (a payload rename that failed after the metadata row
// was written was not rolled back).
func desyncMetadataRV(t *testing.T, si *StorageImpl, key, rv string) {
	t.Helper()
	conn, err := si.pool.Take(context.Background())
	require.NoError(t, err)
	defer si.pool.Put(conn)

	raw, err := ReadMetadata(conn, key)
	require.NoError(t, err)
	meta := &softwarecomposition.VulnerabilityManifestSummary{}
	require.NoError(t, json.Unmarshal(raw, meta))
	require.NotEqual(t, rv, meta.ResourceVersion)
	meta.ResourceVersion = rv
	raw, err = json.Marshal(meta)
	require.NoError(t, err)
	// Through s.write so a gated storage writes on the gate's connection.
	require.NoError(t, si.write(context.Background(), conn, priorityHigh, "test-desync", resourceFromKey(key), false,
		func(_ context.Context, c *sqlite.Conn) error { return WriteJSON(c, key, raw) }))
}

// TestSingleWriter_GuaranteedUpdate_MetadataRVMismatch reproduces the stuck
// VulnerabilityManifestSummary updates seen in production: when the metadata
// row's resourceVersion differs from the payload's, GuaranteedUpdate reads the
// payload (baseRV = payload RV) but the commit compared against the metadata
// row, so every commit returned errWriteConflict and the retry loop spun
// silently until the request deadline (kubevuln: "Timeout: request did not
// complete within requested timeout").
//
// The update must succeed well within the deadline, realign the row, and
// land on a resourceVersion above both observed ones: a lower one would be
// dropped by watchers holding the row's version and would let a stale update
// carrying it pass an exact-match version check later. Covered with and
// without the shared write gate (containerProfileSqliteBackend on/off), with
// the row ahead of and behind the payload.
func TestSingleWriter_GuaranteedUpdate_MetadataRVMismatch(t *testing.T) {
	cases := []struct {
		name       string
		payloadRVs int // updates applied before desync, so payload RV = 1+payloadRVs
		rowRV      string
		wantRV     string
		// detached makes tryUpdate return one separately prepared object,
		// reused across attempts and starting at resourceVersion 1, the
		// shape SaveContainerProfile uses, instead of a copy of its input.
		detached bool
	}{
		{name: "row ahead", payloadRVs: 0, rowRV: "7", wantRV: "8"},
		{name: "row behind", payloadRVs: 4, rowRV: "2", wantRV: "6"},
		{name: "row ahead detached object", payloadRVs: 0, rowRV: "7", wantRV: "8", detached: true},
		{name: "row behind detached object", payloadRVs: 4, rowRV: "2", wantRV: "6", detached: true},
	}
	for _, tc := range cases {
		for _, gated := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/ungated", true: "/gated"}[gated], func(t *testing.T) {
				enableSingleWriter(t)
				si, cleanup := newSingleWriterTestStorage(t)
				defer cleanup()
				if gated {
					gate, err := newWriteGate(context.Background(), si.pool)
					require.NoError(t, err)
					defer gate.Close()
					si.SetWriteGate(gate)
				}

				key := "/spdx.softwarecomposition.kubescape.io/vulnerabilitymanifestsummaries/ns1/deployment-app-app"
				obj := &softwarecomposition.VulnerabilityManifestSummary{
					ObjectMeta: metav1.ObjectMeta{Name: "deployment-app-app", Namespace: "ns1", Labels: map[string]string{"n": "0"}},
				}
				require.NoError(t, si.Create(context.Background(), key, obj, &softwarecomposition.VulnerabilityManifestSummary{}, 0))
				setLabel := func(v string) storage.UpdateFunc {
					return func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						cur := input.(*softwarecomposition.VulnerabilityManifestSummary).DeepCopy()
						cur.Labels["n"] = v
						return cur, nil, nil
					}
				}
				for i := 0; i < tc.payloadRVs; i++ {
					require.NoError(t, si.GuaranteedUpdate(context.Background(), key, &softwarecomposition.VulnerabilityManifestSummary{}, false, nil, setLabel(strconv.Itoa(i+100)), nil))
				}
				payloadRV := strconv.Itoa(1 + tc.payloadRVs)

				desyncMetadataRV(t, si, key, tc.rowRV)

				got := &softwarecomposition.VulnerabilityManifestSummary{}
				require.NoError(t, si.Get(context.Background(), key, storage.GetOptions{}, got))
				require.Equal(t, payloadRV, got.ResourceVersion, "full read serves the payload's resourceVersion")

				const deadline = 3 * time.Second
				ctx, cancel := context.WithTimeout(context.Background(), deadline)
				defer cancel()

				start := time.Now()
				out := &softwarecomposition.VulnerabilityManifestSummary{}
				update := setLabel("1")
				if tc.detached {
					prepared := got.DeepCopy()
					prepared.ResourceVersion = "1"
					prepared.Labels["n"] = "1"
					update = func(runtime.Object, storage.ResponseMeta) (runtime.Object, *uint64, error) {
						return prepared, nil, nil
					}
				}
				err := si.GuaranteedUpdate(ctx, key, out, false, nil, update, nil)
				elapsed := time.Since(start)
				t.Logf("GuaranteedUpdate returned after %s: err=%v", elapsed.Round(time.Millisecond), err)
				require.Less(t, elapsed, deadline/2, "GuaranteedUpdate ran out the caller's deadline (err=%v)", err)
				require.NoError(t, err)
				require.Equal(t, tc.wantRV, out.ResourceVersion, "repair lands above both the payload's and the row's resourceVersion")

				full := &softwarecomposition.VulnerabilityManifestSummary{}
				require.NoError(t, si.Get(context.Background(), key, storage.GetOptions{}, full))
				require.Equal(t, "1", full.Labels["n"])
				require.Equal(t, tc.wantRV, full.ResourceVersion)

				meta := &softwarecomposition.VulnerabilityManifestSummary{}
				require.NoError(t, si.Get(context.Background(), key, storage.GetOptions{ResourceVersion: softwarecomposition.ResourceVersionMetadata}, meta))
				require.Equal(t, tc.wantRV, meta.ResourceVersion, "metadata row realigned with the payload")

				// A stale writer still holding the row's old resourceVersion
				// must be rejected, not slip through.
				stale := full.DeepCopy()
				stale.ResourceVersion = tc.rowRV
				err = si.GuaranteedUpdate(context.Background(), key, &softwarecomposition.VulnerabilityManifestSummary{}, false,
					&storage.Preconditions{ResourceVersion: &stale.ResourceVersion}, setLabel("stale"), nil)
				require.Error(t, err, "an update preconditioned on the old row resourceVersion must conflict")
			})
		}
	}
}
