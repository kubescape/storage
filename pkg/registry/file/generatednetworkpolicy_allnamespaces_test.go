package file

import (
	"context"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/storage"
)

// TestGeneratedNetworkPolicyStorage_GetList_AllNamespacesKeepsWorkloadsApart
// lists generated network policies across all namespaces (kubectl get -A).
// Two namespaces each run a Deployment called "nginx". They are different
// workloads, so the list must hold one policy per namespace, each built only
// from its own namespace's container profiles.
func TestGeneratedNetworkPolicyStorage_GetList_AllNamespacesKeepsWorkloadsApart(t *testing.T) {
	realStorage, gnpStorage, pool := newGNPTestStorage(t)
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	nginxA := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-bd5db6555-web-1111-2222", "web", "10.0.0.1", 80)
	nginxA.Namespace = "team-a"
	nginxB := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-6f7c8d9e0-web-3333-4444", "web", "10.0.0.2", 80)
	nginxB.Namespace = "team-b"

	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofiles/team-a/replicaset-nginx-bd5db6555-web-1111-2222", nginxA, nil, 0))
	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofiles/team-b/replicaset-nginx-6f7c8d9e0-web-3333-4444", nginxB, nil, 0))

	list := &softwarecomposition.GeneratedNetworkPolicyList{}
	require.NoError(t, gnpStorage.GetList(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies", storage.ListOptions{}, list))

	byNamespace := map[string]*softwarecomposition.GeneratedNetworkPolicy{}
	for i := range list.Items {
		byNamespace[list.Items[i].Namespace] = &list.Items[i]
	}
	require.Len(t, list.Items, 2, "one policy per workload, and the two nginx Deployments live in different namespaces")

	a, ok := byNamespace["team-a"]
	require.True(t, ok, "expected a policy in team-a")
	b, ok := byNamespace["team-b"]
	require.True(t, ok, "expected a policy in team-b")

	assert.Equal(t, []string{"10.0.0.1/32"}, egressCIDRs(a), "team-a policy must hold only team-a's egress")
	assert.Equal(t, []string{"10.0.0.2/32"}, egressCIDRs(b), "team-b policy must hold only team-b's egress")
}
