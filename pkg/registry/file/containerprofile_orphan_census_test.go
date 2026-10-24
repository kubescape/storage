package file

import (
	"context"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type censusCPStorage struct {
	ContainerProfileStorage
}

func (c *censusCPStorage) impl() *ContainerProfileStorageImpl {
	return c.ContainerProfileStorage.(*hookedCPStorage).ContainerProfileStorage.(*ContainerProfileStorageImpl)
}

func (c *censusCPStorage) ListOrphanTimeSeriesObjects(ctx context.Context, limit int) ([]string, error) {
	return c.impl().ListOrphanTimeSeriesObjects(ctx, limit)
}

func (c *censusCPStorage) WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	return c.impl().WriteTimeSeriesEntry(ctx, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData)
}

func (h *lane0Harness) writeOrphanChunk(t *testing.T, baseKey, suffix, tag string) string {
	t.Helper()
	_, _, _, _, ns, name := K8sPathToKeys(baseKey)
	tsKey := baseKey + "-" + suffix
	obj := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + suffix,
			Namespace: ns,
			Annotations: map[string]string{
				helpersv1.ReportSeriesIdMetadataKey:          "orphan-series",
				helpersv1.ReportTimestampMetadataKey:         h.ts(1),
				helpersv1.PreviousReportTimestampMetadataKey: lane0ZeroTime,
				helpersv1.StatusMetadataKey:                  helpersv1.Learning,
				helpersv1.CompletionMetadataKey:              helpersv1.Partial,
				helpersv1.InstanceIDMetadataKey:              lane0InstanceID,
			},
		},
		Spec: lane0Spec(tag),
	}
	conn, err := h.pool.Take(context.Background())
	require.NoError(t, err)
	defer h.pool.Put(conn)
	_, err = h.s.saveObject(context.Background(), conn, tsKey, obj, &softwarecomposition.ContainerProfile{}, "", priorityLow, holdPathLegacyCommit)
	require.NoError(t, err)
	return tsKey
}

func TestConsolidation_ChunkObjectWithoutRowIsRegisteredAndFolded(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "census"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	orphan := h.writeOrphanChunk(t, key, "0123456789abcdef0123456789abcdef", "orphan")
	require.Equal(t, 0, countRows(h.listRows(t, key)), "the chunk object exists without a time series row")
	h.proc.SetStorage(&censusCPStorage{h.hooks})

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))

	require.False(t, h.objectExists(t, orphan), "the chunk without a row is registered, folded and deleted in one pass")
	p := h.readPayload(t, key)
	require.True(t, specHasExec(p.Spec, "orphan"), "its payload reached the base profile")
	for _, rows := range h.listRows(t, key) {
		for _, r := range rows {
			require.False(t, r.HasData, "the re-registered row is consumed by the merge; only the series marker may remain")
		}
	}
}
