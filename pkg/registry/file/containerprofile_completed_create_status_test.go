package file

import (
	"context"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCreate_ChunkForCompletedFullProfileIsAConflictStatus(t *testing.T) {
	enableSingleWriter(t)
	h := newLane0Harness(t, 0)
	const ns, name = "ns1", "done"
	key := lane0Key(ns, name)
	h.createBase(t, key, newBaseProfile(ns, name, helpersv1.Completed, helpersv1.Full, "base"))

	const suffix = "0123456789abcdef0123456789abcdef"
	chunk := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-" + suffix,
			Namespace: ns,
			Annotations: map[string]string{
				helpersv1.ReportSeriesIdMetadataKey:  "late-series",
				helpersv1.ReportTimestampMetadataKey: h.ts(1),
				helpersv1.StatusMetadataKey:          helpersv1.Learning,
				helpersv1.CompletionMetadataKey:      helpersv1.Partial,
				helpersv1.InstanceIDMetadataKey:      lane0InstanceID,
			},
		},
		Spec: lane0Spec("late"),
	}
	err := h.s.Create(context.Background(), key+"-"+suffix, chunk, &softwarecomposition.ContainerProfile{}, 0)
	require.Error(t, err)
	require.True(t, apierrors.IsConflict(err), "a report for a profile that is already completed and full is refused with a Conflict status, not a bare error (got %T: %v)", err, err)
	require.False(t, h.objectExists(t, key+"-"+suffix))
}
