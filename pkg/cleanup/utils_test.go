package cleanup

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_wlidWithoutClusterName(t *testing.T) {
	tests := []struct {
		name string
		wlid string
		want string
	}{
		{
			name: "wlid with cluster name",
			wlid: "wlid://cluster-docker-desktop/namespace-default/deployment-nginx-deployment",
			want: "namespace-default/deployment-nginx-deployment",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wlidWithoutClusterName(tt.wlid)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestUnmarshalPartialObjectMetadata(t *testing.T) {
	b := []byte(`{"name":"pod-nginx-nginx","namespace":"default","uid":"b0db5ba1-7011-451b-97cd-e259b5635392","resourceVersion":"1","creationTimestamp":"2025-02-10T16:43:26Z","labels":{"kubescape.io/context":"filtered","kubescape.io/image-id":"docker-io-library-nginx-sha256-91734281c0ebfc6f1aea979cffeed507","kubescape.io/image-name":"docker-io-library-nginx","kubescape.io/workload-api-group":"","kubescape.io/workload-api-version":"v1","kubescape.io/workload-container-name":"nginx","kubescape.io/workload-kind":"pod","kubescape.io/workload-name":"nginx","kubescape.io/workload-namespace":"default"},"annotations":{"kubescape.io/image-id":"docker.io/library/nginx@sha256:91734281c0ebfc6f1aea979cffeed5079cfe786228a71cc6f1f46a228cde6e34","kubescape.io/image-tag":"docker.io/library/nginx:latest","kubescape.io/resource-size":"3898615","kubescape.io/scan-id":"pod-nginx-nginx-1ba5-4aaf-v1-13-0-v0-81-0","kubescape.io/status":"ready","kubescape.io/sync-checksum":"9d6a1a104eab8a29f0ce05e2bab2cafecb2a8067a26ae33b41b896ba435cef2c","kubescape.io/timestamp":"1739195147","kubescape.io/wlid":"wlid://cluster-kind-kind/namespace-default/pod-nginx","kubescape.io/workload-container-name":"nginx"},"Spec":{"Severities":{"Critical":{"All":0,"Relevant":0},"High":{"All":0,"Relevant":0},"Medium":{"All":0,"Relevant":0},"Low":{"All":0,"Relevant":0},"Negligible":{"All":0,"Relevant":0},"Unknown":{"All":0,"Relevant":0}},"Vulnerabilities":{"ImageVulnerabilitiesObj":{"Namespace":"","Name":"","Kind":""},"WorkloadVulnerabilitiesObj":{"Namespace":"","Name":"","Kind":""}}},"Status":{}}`)
	metadata := &PartialObjectMetadata{}
	err := json.Unmarshal(b, metadata)
	assert.NoError(t, err)
	assert.Equal(t, "pod-nginx-nginx", metadata.Name)
	assert.Equal(t, "default", metadata.Namespace)
	assert.Equal(t, "1", metadata.ResourceVersion)
	assert.Equal(t, int64(1739205806), metadata.CreationTimestamp.Unix())
}
