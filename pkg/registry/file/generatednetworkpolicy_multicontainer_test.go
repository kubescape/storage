package file

import (
	"context"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

// These tests pin the WORKLOAD-LEVEL contract of GeneratedNetworkPolicy.
//
// A k8s NetworkPolicy selects pods, and all containers in a pod share the pod's
// network namespace. Therefore the generated policy for a workload MUST be
// workload-level: one policy per workload, named <lower(kind)>-<workload-name>,
// unioning the ingress/egress neighbors of EVERY container of that workload.
//
// Pre-migration this held because the storage read one workload-level
// NetworkNeighborhood per workload. Post-migration the storage reads
// per-container ContainerProfiles (one object per container, slug-named
// <lower(kind)>-<workload-name>-<container-name>-<hash>-<hash>, e.g.
// "replicaset-coredns-5d78c9869d-coredns-185f-129c") but still does a single
// literal-key Get and emits one item per ContainerProfile in GetList — so the
// per-workload aggregation is lost. These tests fail on that gap.

// makeContainerProfile builds a per-container ContainerProfile for a workload,
// mirroring the real per-container slug naming and workload-identifying labels.
func makeContainerProfile(workloadKind, workloadName, containerSlug, containerName, ip string, port int32) *softwarecomposition.ContainerProfile {
	p := port
	return &softwarecomposition.ContainerProfile{
		TypeMeta: v1.TypeMeta{
			Kind:       "ContainerProfile",
			APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1",
		},
		ObjectMeta: v1.ObjectMeta{
			Name:      containerSlug,
			Namespace: "kubescape",
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey: helpersv1.Completed,
			},
			Labels: map[string]string{
				helpersv1.RelatedKindMetadataKey:   workloadKind,
				helpersv1.RelatedNameMetadataKey:   workloadName,
				helpersv1.ContainerNameMetadataKey: containerName,
			},
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Egress: []softwarecomposition.NetworkNeighbor{
				{
					Identifier: containerName + "-egress",
					Type:       softwarecomposition.CommunicationTypeEgress,
					IPAddress:  ip,
					Ports: []softwarecomposition.NetworkPort{
						{
							Name:     "TCP-" + containerName,
							Protocol: softwarecomposition.ProtocolTCP,
							Port:     &p,
						},
					},
				},
			},
		},
	}
}

// egressCIDRs collects every IPBlock CIDR present in the policy's egress rules.
func egressCIDRs(gnp *softwarecomposition.GeneratedNetworkPolicy) []string {
	var out []string
	for _, rule := range gnp.Spec.Spec.Egress {
		for _, peer := range rule.To {
			if peer.IPBlock != nil {
				out = append(out, peer.IPBlock.CIDR)
			}
		}
	}
	return out
}

func newGNPTestStorage(t *testing.T) (StorageQuerier, storage.Interface, *sqlitemigration.Pool) {
	t.Helper()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	realStorage := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch)
	return realStorage, NewGeneratedNetworkPolicyStorage(realStorage), pool
}

// TestGeneratedNetworkPolicyStorage_Get_MultiContainerWorkload pins contract (1):
// Get by the workload-level name of a multi-container workload must resolve the
// workload's per-container ContainerProfiles and return ONE policy that unions
// every container's egress — not NotFound, not a single container's rules.
func TestGeneratedNetworkPolicyStorage_Get_MultiContainerWorkload(t *testing.T) {
	realStorage, gnpStorage, pool := newGNPTestStorage(t)
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	// One Deployment "nginx" with two containers, each a distinct ContainerProfile
	// with a distinct egress neighbor. A Deployment's ContainerProfiles are
	// REPLICASET-named (object name), while the workload identity lives in the
	// workload-kind/workload-name labels — resolution must key off the labels.
	containerA := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-bd5db6555-web-1111-2222", "web", "10.0.0.1", 80)
	containerB := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-bd5db6555-sidecar-3333-4444", "sidecar", "10.0.0.2", 443)

	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-nginx-bd5db6555-web-1111-2222", containerA, nil, 0))
	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-nginx-bd5db6555-sidecar-3333-4444", containerB, nil, 0))

	// A consumer requests the GeneratedNetworkPolicy by the workload-level name
	// <lower(kind)>-<workload-name>, matching pre-migration NetworkNeighborhood naming.
	gnp := &softwarecomposition.GeneratedNetworkPolicy{}
	err := gnpStorage.Get(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/deployment-nginx", storage.GetOptions{}, gnp)

	require.NoError(t, err, "workload-level Get must resolve the workload's container profiles, not 404")

	cidrs := egressCIDRs(gnp)
	assert.Contains(t, cidrs, "10.0.0.1/32", "policy must include container 'web' egress")
	assert.Contains(t, cidrs, "10.0.0.2/32", "policy must include container 'sidecar' egress (union across containers)")
	assert.Equal(t, "deployment-nginx", gnp.Spec.Name, "generated NetworkPolicy must be workload-level named")
}

// TestGeneratedNetworkPolicyStorage_GetList_OnePolicyPerWorkload pins contract (2):
// GetList over a namespace must emit exactly one GeneratedNetworkPolicy per
// WORKLOAD (workload-level named), not one per ContainerProfile, and each
// workload's policy must union all its containers.
func TestGeneratedNetworkPolicyStorage_GetList_OnePolicyPerWorkload(t *testing.T) {
	realStorage, gnpStorage, pool := newGNPTestStorage(t)
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	// Workload 1: Deployment "nginx" with two containers (replicaset-named CPs).
	nginxWeb := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-bd5db6555-web-1111-2222", "web", "10.0.0.1", 80)
	nginxSidecar := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-bd5db6555-sidecar-3333-4444", "sidecar", "10.0.0.2", 443)
	// Workload 2: Deployment "redis" with a single container.
	redis := makeContainerProfile("Deployment", "redis", "replicaset-redis-7c9f8d4b6-redis-5555-6666", "redis", "10.0.0.3", 6379)

	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-nginx-bd5db6555-web-1111-2222", nginxWeb, nil, 0))
	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-nginx-bd5db6555-sidecar-3333-4444", nginxSidecar, nil, 0))
	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-redis-7c9f8d4b6-redis-5555-6666", redis, nil, 0))

	list := &softwarecomposition.GeneratedNetworkPolicyList{}
	opts := storage.ListOptions{Predicate: storage.SelectionPredicate{Limit: 500}}
	err := gnpStorage.GetList(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape", opts, list)
	require.NoError(t, err)

	// Exactly two workloads => exactly two policies (three ContainerProfiles).
	require.Len(t, list.Items, 2, "GetList must emit one policy per WORKLOAD, not one per ContainerProfile")

	byName := map[string]*softwarecomposition.GeneratedNetworkPolicy{}
	for i := range list.Items {
		byName[list.Items[i].Spec.Name] = &list.Items[i]
	}

	nginx, ok := byName["deployment-nginx"]
	require.True(t, ok, "expected a workload-level 'deployment-nginx' policy")
	redisPol, ok := byName["deployment-redis"]
	require.True(t, ok, "expected a workload-level 'deployment-redis' policy")

	nginxCIDRs := egressCIDRs(nginx)
	assert.Contains(t, nginxCIDRs, "10.0.0.1/32", "nginx policy must union container 'web'")
	assert.Contains(t, nginxCIDRs, "10.0.0.2/32", "nginx policy must union container 'sidecar'")

	assert.Contains(t, egressCIDRs(redisPol), "10.0.0.3/32", "redis policy must include its single container")
}

// TestGeneratedNetworkPolicyStorage_ExcludesNonAvailableProfiles pins the
// availability filter (networkpolicy.IsAvailable) that both Get and GetList apply
// before aggregation: a ContainerProfile whose status is neither "ready"
// (Learning) nor "completed" is not yet policy-ready and MUST be excluded.
//
// NOTE: the audit note framed the excluded case as "Learning-status", but
// IsAvailable treats BOTH Learning ("ready") and Completed as available; the real
// non-available branch is a status outside that set. This test exercises the real
// branch with a TooLarge-status profile (and confirms a Learning-status profile is
// NOT excluded, documenting the actual contract).
func TestGeneratedNetworkPolicyStorage_ExcludesNonAvailableProfiles(t *testing.T) {
	realStorage, gnpStorage, pool := newGNPTestStorage(t)
	defer func() { _ = pool.Close() }()

	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	// Available workload: Completed.
	nginx := makeContainerProfile("Deployment", "nginx", "replicaset-nginx-bd5db6555-web-1111-2222", "web", "10.0.0.1", 80)
	// Available workload: Learning ("ready") is ALSO available per IsAvailable.
	api := makeContainerProfile("Deployment", "api", "replicaset-api-aaa-api-1212-3434", "api", "10.0.0.9", 8080)
	api.Annotations[helpersv1.StatusMetadataKey] = helpersv1.Learning
	// Non-available workload: TooLarge => excluded.
	redis := makeContainerProfile("Deployment", "redis", "replicaset-redis-7c9f8d4b6-redis-5555-6666", "redis", "10.0.0.3", 6379)
	redis.Annotations[helpersv1.StatusMetadataKey] = helpersv1.TooLarge

	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-nginx-bd5db6555-web-1111-2222", nginx, nil, 0))
	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-api-aaa-api-1212-3434", api, nil, 0))
	require.NoError(t, realStorage.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/kubescape/replicaset-redis-7c9f8d4b6-redis-5555-6666", redis, nil, 0))

	// GetList: only the two available workloads produce policies.
	list := &softwarecomposition.GeneratedNetworkPolicyList{}
	opts := storage.ListOptions{Predicate: storage.SelectionPredicate{Limit: 500}}
	require.NoError(t, gnpStorage.GetList(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape", opts, list))

	names := map[string]bool{}
	for i := range list.Items {
		names[list.Items[i].Spec.Name] = true
	}
	assert.True(t, names["deployment-nginx"], "Completed workload must be included")
	assert.True(t, names["deployment-api"], "Learning workload must be included (Learning is available)")
	assert.False(t, names["deployment-redis"], "TooLarge (non-available) workload must be excluded from GetList")
	assert.Len(t, list.Items, 2, "exactly the two available workloads produce policies")

	// Get by the non-available workload's name must be NotFound (its only
	// container profile is filtered out, leaving an empty group).
	gnp := &softwarecomposition.GeneratedNetworkPolicy{}
	err := gnpStorage.Get(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/deployment-redis", storage.GetOptions{}, gnp)
	require.Error(t, err, "non-available workload must not resolve via Get")
	assert.True(t, storage.IsNotFound(err), "expected a NotFound error, got %v", err)

	// Sanity: the available workload still resolves.
	require.NoError(t, gnpStorage.Get(ctx, "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/kubescape/deployment-nginx", storage.GetOptions{}, &softwarecomposition.GeneratedNetworkPolicy{}))
}
