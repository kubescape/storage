package file

import (
	"context"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/component-base/metrics/legacyregistry"
	"zombiezen.com/go/sqlite"
)

func singleWriterCommits(t *testing.T, outcome string) float64 {
	t.Helper()
	families, err := legacyregistry.DefaultGatherer.Gather()
	require.NoError(t, err)
	var n float64
	for _, mf := range families {
		if mf.GetName() != "storage_single_writer_commit_total" {
			continue
		}
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "outcome" && l.GetValue() == outcome {
					n += m.GetCounter().GetValue()
				}
			}
		}
	}
	return n
}

func TestSingleWriter_CallerGoneBeforeCommit_JobIsDroppedNotFailed(t *testing.T) {
	enableSingleWriter(t)
	setSingleWriterShards(t, 1)
	si, cleanup := newSingleWriterTestStorage(t)
	defer cleanup()

	errorsBefore := singleWriterCommits(t, "error")
	cancelledBefore := singleWriterCommits(t, "cancelled")

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	si.writeMetadataFn = func(conn *sqlite.Conn, path string, metadata runtime.Object) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return writeMetadata(conn, path, metadata)
	}

	blockerDone := make(chan error, 1)
	go func() {
		obj := &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "blocker", Namespace: "ns1"}}
		blockerDone <- si.createSingleWriter(context.Background(), testProfileKey("blocker"), obj, &softwarecomposition.ContainerProfile{}, priorityHigh)
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	obj := &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "abandoned", Namespace: "ns1"}}
	err := si.createSingleWriter(ctx, testProfileKey("abandoned"), obj, &softwarecomposition.ContainerProfile{}, priorityHigh)
	require.ErrorIs(t, err, context.DeadlineExceeded, "the caller stops waiting when its context expires while the job is queued")

	close(release)
	require.NoError(t, <-blockerDone)

	require.Eventually(t, func() bool {
		return singleWriterCommits(t, "cancelled") == cancelledBefore+1
	}, 5*time.Second, 10*time.Millisecond, "the abandoned job is dropped as cancelled once the shard reaches it")
	require.Equal(t, errorsBefore, singleWriterCommits(t, "error"), "an abandoned job is not a failed commit")
}
