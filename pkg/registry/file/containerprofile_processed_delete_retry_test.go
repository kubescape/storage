package file

import (
	"context"
	"errors"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/stretchr/testify/require"
)

type deleteFailingCPStorage struct {
	ContainerProfileStorage
	failures int
	calls    int
}

func (d *deleteFailingCPStorage) DeleteContainerProfile(ctx context.Context, key string) error {
	d.calls++
	if d.failures > 0 {
		d.failures--
		return errors.New("sqlite: step: database is locked")
	}
	return d.ContainerProfileStorage.DeleteContainerProfile(ctx, key)
}

func (d *deleteFailingCPStorage) WriteTimeSeriesEntry(ctx context.Context, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp string, hasData bool) error {
	return d.ContainerProfileStorage.(*hookedCPStorage).ContainerProfileStorage.(TimeSeriesEntryWriter).WriteTimeSeriesEntry(ctx, kind, namespace, name, seriesID, tsSuffix, reportTimestamp, status, completion, previousReportTimestamp, hasData)
}

func TestConsolidation_FailedObjectDeleteAfterCommitIsRetriedOnTheNextTick(t *testing.T) {
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "retry"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Learning, helpersv1.Partial, "base"))
	tsKeys := h.seedChain(t, ns, name, "A", helpersv1.Completed, helpersv1.Full)

	failing := &deleteFailingCPStorage{ContainerProfileStorage: h.hooks, failures: 1}
	h.proc.SetStorage(failing)

	err := h.proc.ConsolidateTimeSeries(context.Background())
	require.Error(t, err, "the tick reports the delete it could not perform")
	require.GreaterOrEqual(t, failing.calls, len(tsKeys), "one failed delete does not stop the deletes of the other processed objects")

	remaining := 0
	for _, tsKey := range tsKeys {
		if h.objectExists(t, tsKey) {
			remaining++
		}
	}
	require.Equal(t, 1, remaining, "only the object whose delete failed survives the tick")
	require.Equal(t, 1, countRows(h.listRows(t, key)), "the surviving object has its time series row back so the next tick sees it")

	require.NoError(t, h.proc.ConsolidateTimeSeries(context.Background()))
	for _, tsKey := range tsKeys {
		require.False(t, h.objectExists(t, tsKey), "the next tick deletes the object that survived")
	}
	require.Equal(t, 0, countRows(h.listRows(t, key)))
}
