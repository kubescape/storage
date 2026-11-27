package file

// §5.6 row 9 of .omc/plans/full-acid-storage-architecture.md:
// GeneratedNetworkPolicyStorage's full-spec ContainerProfile list must read
// through the ContainerProfile storage.Interface (the ObjectStore under the
// flag), not the default StorageImpl — whose get() would delete a row the
// ObjectStore owns when the file it expects is absent (and, guarded, refuses
// the key outright). Its knownservers read stays on the default instance.

import (
	"context"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
)

// querierSpy counts the calls GeneratedNetworkPolicyStorage makes on the
// default StorageQuerier.
type querierSpy struct {
	StorageQuerier
	getList      int
	getByCluster int
}

func (s *querierSpy) GetList(ctx context.Context, key string, opts storage.ListOptions, listObj runtime.Object) error {
	s.getList++
	return s.StorageQuerier.GetList(ctx, key, opts, listObj)
}

func (s *querierSpy) GetByCluster(ctx context.Context, apiVersion, kind string, listObj runtime.Object) error {
	s.getByCluster++
	return s.StorageQuerier.GetByCluster(ctx, apiVersion, kind, listObj)
}

const testGNPPrefix = "/spdx.softwarecomposition.kubescape.io/generatednetworkpolicies/"

func TestGNP_ContainerProfileReadsGoThroughTheCPStore(t *testing.T) {
	e := newObjectStoreEnv(t)
	// The template carries workload-kind Deployment / workload-name coredns
	// and status ready, so it is available and maps to "deployment-coredns".
	e.create(e.plain("deployment-coredns-coredns-1111-2222"))

	spy := &querierSpy{StorageQuerier: e.legacy}
	gnp := NewGeneratedNetworkPolicyStorage(spy, e.store)

	out := &softwarecomposition.GeneratedNetworkPolicy{}
	require.NoError(t, gnp.Get(e.ctx, testGNPPrefix+e.ns+"/deployment-coredns", storage.GetOptions{}, out))
	require.Equal(t, "deployment-coredns", out.Name)

	list := &softwarecomposition.GeneratedNetworkPolicyList{}
	require.NoError(t, gnp.GetList(e.ctx, testGNPPrefix+e.ns, storage.ListOptions{Predicate: storage.Everything}, list))
	require.Len(t, list.Items, 1)
	require.Equal(t, "deployment-coredns", list.Items[0].Name)

	// Non-CP reads are unchanged: knownservers come from the default
	// instance (once per Get/GetList); no CP list ever reaches it.
	require.Equal(t, 2, spy.getByCluster, "knownservers must still be read through the default StorageQuerier")
	require.Equal(t, 0, spy.getList, "no ContainerProfile list may reach the default StorageQuerier")

	// The guarded default instance still refuses the CP key: the old wiring
	// (CP reads through the default instance) is not silently working.
	cpList := &softwarecomposition.ContainerProfileList{}
	err := e.legacy.GetList(e.ctx, testCPPrefix+e.ns, storage.ListOptions{
		ResourceVersion: softwarecomposition.ResourceVersionFullSpec,
		Predicate:       storage.SelectionPredicate{Limit: containerProfileListLimit},
	}, cpList)
	require.Error(t, err)
	require.Contains(t, err.Error(), "owned by the ContainerProfile SQLite backend")
}
