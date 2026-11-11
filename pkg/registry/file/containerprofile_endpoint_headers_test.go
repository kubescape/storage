package file

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
)

// An endpoint whose headers are JSON null ("headers": null in a request) is
// stored as is: a time-series profile is not deflated on create. When
// consolidation later merges a duplicate of that endpoint into it, the merge
// must not panic: consolidation runs on a background goroutine with no
// recover, so a panic there stops the whole storage server, and the row it
// failed on is still there when the server starts again. The key is
// consolidated on the test's goroutine so that a regression fails this test
// instead of the test binary.
func TestConsolidation_EndpointWithNullHeadersDoesNotPanic(t *testing.T) {
	s, processor, _, _ := newLifecycleStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 30*time.Second)
	defer cancel()

	const ns = "honey"
	const baseName = "replicaset-web-abcd-web-1111-2222"
	key := cpPrefixKey + "/" + ns + "/" + baseName
	ts := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      baseName + "-aaaa1111",
			Namespace: ns,
			Annotations: map[string]string{
				helpersv1.CompletionMetadataKey:      helpersv1.Partial,
				helpersv1.InstanceIDMetadataKey:      "apiVersion-apps/v1/namespace-honey/kind-ReplicaSet/name-web-abcd/containerName-web",
				helpersv1.ReportSeriesIdMetadataKey:  "11111111-2222-3333-4444-555555555555",
				helpersv1.ReportTimestampMetadataKey: "2025-06-24T10:00:00Z",
				helpersv1.StatusMetadataKey:          helpersv1.Learning,
				helpersv1.ContainerTypeMetadataKey:   "containers",
			},
		},
		Spec: softwarecomposition.ContainerProfileSpec{Endpoints: []softwarecomposition.HTTPEndpoint{
			{Endpoint: ":80/api", Methods: []string{"GET"}, Direction: consts.Inbound, Headers: json.RawMessage("null")},
			{Endpoint: ":80/api", Methods: []string{"POST"}, Direction: consts.Inbound, Headers: json.RawMessage(`{"Host":["web"]}`)},
		}},
	}
	require.NoError(t, s.Create(ctx, key+"-aaaa1111", ts, nil, 0))

	require.NotPanics(t, func() { require.NoError(t, processor.consolidateKeyTimeSeries(ctx, key, false)) })

	var cp softwarecomposition.ContainerProfile
	require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, &cp))
	require.Len(t, cp.Spec.Endpoints, 1)
	assert.ElementsMatch(t, []string{"GET", "POST"}, cp.Spec.Endpoints[0].Methods)
	headers, err := cp.Spec.Endpoints[0].GetHeaders()
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"Host": {"web"}}, headers)
}
