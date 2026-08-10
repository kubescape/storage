package file

import (
	"context"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// idleWorkloadStorage models a workload whose time series is already consolidated
// (a single has_data=false entry) and whose observed profile is valid (carries an
// InstanceID). A consolidation run over it produces NO new data, so the aggregated
// AP/NN are not rewritten — and therefore no downstream onFinish should be emitted.
type idleWorkloadStorage struct {
	*fakeStorage
	profile softwarecomposition.ContainerProfile
}

func (s *idleWorkloadStorage) ListTimeSeriesContainers(ctx context.Context, key string) (map[string][]softwarecomposition.TimeSeriesContainers, error) {
	return map[string][]softwarecomposition.TimeSeriesContainers{
		"series-1": {{Status: helpersv1.Learning, HasData: false, PreviousReportTimestamp: "", ReportTimestamp: "2026-01-23T12:27:20Z", TsSuffix: "s1"}},
	}, nil
}

func (s *idleWorkloadStorage) GetContainerProfile(ctx context.Context, key string) (softwarecomposition.ContainerProfile, error) {
	return s.profile, nil
}

// TestConsolidateKeyTimeSeries_NoEmitWhenNoNewData pins the inflow-reduction fix:
// consolidateKeyTimeSeries must emit a consolidated slug (which drives a downstream
// AP/NN onFinish, and ultimately a container_statuses upsert) ONLY when the run
// actually produced new aggregated data. An idle/unchanged workload re-emitting on
// every run is what floods synchronizer-finished-v1 and feeds the container_statuses
// deadlock storm.
func TestConsolidateKeyTimeSeries_NoEmitWhenNoNewData(t *testing.T) {
	ch := make(chan ConsolidatedSlugData, 4)
	profile := softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				helpersv1.InstanceIDMetadataKey: "apiVersion-apps/v1/namespace-default/kind-Deployment/name-test-app",
			},
		},
	}
	spy := &idleWorkloadStorage{fakeStorage: &fakeStorage{}, profile: profile}

	p := &ContainerProfileProcessor{
		HostType:                armotypes.HostTypeKubernetes,
		ConsolidatedSlugChannel: ch,
	}
	p.ContainerProfileStorage = spy

	err := p.consolidateKeyTimeSeries(context.Background(), "spdx/v1beta1/containerprofile/default/deployment-test-app", false)
	require.NoError(t, err)
	assert.Len(t, ch, 0, "no new data consolidated → must not emit a slug (would flood the topic and container_statuses)")
}
