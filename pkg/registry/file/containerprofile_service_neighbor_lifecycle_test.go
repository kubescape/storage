package file

import (
	"context"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const cpPrefixKey = "/spdx.softwarecomposition.kubescape.io/containerprofile"

func svcNeighbor(ns, name, portName string, port int32) softwarecomposition.NetworkNeighbor {
	return softwarecomposition.NetworkNeighbor{
		Identifier: "collide", Type: "internal",
		ServiceRefNamespace: ns, ServiceRefName: name,
		Ports: []softwarecomposition.NetworkPort{{Name: portName, Protocol: "TCP", Port: &port}},
	}
}

func newLifecycleStorage(t *testing.T) (*StorageImpl, *ContainerProfileProcessor, afero.Fs, *sqlitemigration.Pool) {
	t.Helper()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })
	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	fs := afero.NewMemMapFs()
	processor := &ContainerProfileProcessor{
		MaxContainerProfileSize: 40000,
	}
	s := &StorageImpl{
		appFs:           fs,
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))
	return s, processor, fs, pool
}

// TestServiceNeighborLifecycle_ConsolidationPreservesFields drives the real
// TS-create -> ConsolidateTimeSeries -> consolidated-CP-save pipeline and pins
// that serviceRef/serviceSelector/entity neighbors survive merge + deflate:
// duplicates across reports dedup to one entry, distinct service neighbors
// sharing an Identifier are not cross-merged, and a later consolidation tick
// re-running deflate on the stored profile does not strip the fields.
func TestServiceNeighborLifecycle_ConsolidationPreservesFields(t *testing.T) {
	s, processor, _, _ := newLifecycleStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 30*time.Second)
	defer cancel()

	const ns = "honey"
	const baseName = "replicaset-web-abcd-web-1111-2222"
	key := cpPrefixKey + "/" + ns + "/" + baseName

	newTS := func(suffix, reportTs, prevTs string, egress []softwarecomposition.NetworkNeighbor) *softwarecomposition.ContainerProfile {
		return &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{
				Name:      baseName + "-" + suffix,
				Namespace: ns,
				Annotations: map[string]string{
					helpersv1.CompletionMetadataKey:              helpersv1.Partial,
					helpersv1.InstanceIDMetadataKey:              "apiVersion-apps/v1/namespace-honey/kind-ReplicaSet/name-web-abcd/containerName-web",
					helpersv1.PreviousReportTimestampMetadataKey: prevTs,
					helpersv1.ReportSeriesIdMetadataKey:          "11111111-2222-3333-4444-555555555555",
					helpersv1.ReportTimestampMetadataKey:         reportTs,
					helpersv1.StatusMetadataKey:                  helpersv1.Learning,
					helpersv1.ContainerTypeMetadataKey:           "containers",
				},
			},
			Spec: softwarecomposition.ContainerProfileSpec{Egress: egress},
		}
	}

	svcA := svcNeighbor("honey", "alertmanager", "TCP-9093", 9093)
	svcB := svcNeighbor("db", "postgres", "TCP-5432", 5432)
	sel := softwarecomposition.NetworkNeighbor{
		Identifier: "collide", Type: "internal",
		ServiceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "guestbook"}},
	}
	ent := softwarecomposition.NetworkNeighbor{Identifier: "collide", Type: "internal", Entity: "host"}

	ts1 := newTS("aaaa1111", "2025-06-24T10:00:00Z", "", []softwarecomposition.NetworkNeighbor{svcA, ent})
	require.NoError(t, s.Create(ctx, key+"-aaaa1111", ts1, nil, 0))
	ts2 := newTS("bbbb2222", "2025-06-24T10:05:00Z", "2025-06-24T10:00:00Z", []softwarecomposition.NetworkNeighbor{svcA, svcB, sel})
	require.NoError(t, s.Create(ctx, key+"-bbbb2222", ts2, nil, 0))

	require.NoError(t, processor.ConsolidateTimeSeries(ctx))

	readEgress := func() []softwarecomposition.NetworkNeighbor {
		var cp softwarecomposition.ContainerProfile
		require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, &cp))
		return cp.Spec.Egress
	}

	assertServiceNeighbors := func(egress []softwarecomposition.NetworkNeighbor) {
		var gotA, gotB, gotSel, gotEnt []softwarecomposition.NetworkNeighbor
		for _, e := range egress {
			switch {
			case e.ServiceRefName == "alertmanager":
				gotA = append(gotA, e)
			case e.ServiceRefName == "postgres":
				gotB = append(gotB, e)
			case e.ServiceSelector != nil:
				gotSel = append(gotSel, e)
			case e.Entity == "host":
				gotEnt = append(gotEnt, e)
			}
		}
		require.Len(t, gotA, 1, "duplicate serviceRef neighbor must dedup to one entry")
		assert.Equal(t, "honey", gotA[0].ServiceRefNamespace)
		require.Len(t, gotA[0].Ports, 1, "no port cross-contamination from other service neighbors")
		assert.Equal(t, int32(9093), *gotA[0].Ports[0].Port)
		require.Len(t, gotB, 1)
		assert.Equal(t, "db", gotB[0].ServiceRefNamespace)
		require.Len(t, gotB[0].Ports, 1)
		assert.Equal(t, int32(5432), *gotB[0].Ports[0].Port)
		require.Len(t, gotSel, 1)
		assert.Equal(t, map[string]string{"app": "guestbook"}, gotSel[0].ServiceSelector.MatchLabels)
		require.Len(t, gotEnt, 1)
	}
	assertServiceNeighbors(readEgress())

	// A later report carrying only a plain IP neighbor re-runs merge + deflate on
	// the stored profile; the service neighbors must survive that cycle unchanged.
	plain := softwarecomposition.NetworkNeighbor{Identifier: "ip-1", Type: "external", IPAddress: "1.2.3.4"}
	ts3 := newTS("cccc3333", "2025-06-24T10:10:00Z", "2025-06-24T10:05:00Z", []softwarecomposition.NetworkNeighbor{plain})
	require.NoError(t, s.Create(ctx, key+"-cccc3333", ts3, nil, 0))
	require.NoError(t, processor.ConsolidateTimeSeries(ctx))

	egress := readEgress()
	assertServiceNeighbors(egress)
	var gotPlain bool
	for _, e := range egress {
		if e.IPAddress == "1.2.3.4" {
			gotPlain = true
		}
	}
	assert.True(t, gotPlain, "new plain neighbor merged in alongside service neighbors")
}

// TestServiceNeighborLifecycle_FieldOnlyUpdateRefreshesRVAndWatch pins the
// stale-cache contract: an update that changes ONLY a service field must bump
// the ResourceVersion, emit a full-spec Modified watch event carrying the new
// value, and be visible to a cold reader (fresh StorageImpl over the same
// disk+pool); an identical re-update must stay a no-op (no event, same RV).
func TestServiceNeighborLifecycle_FieldOnlyUpdateRefreshesRVAndWatch(t *testing.T) {
	s, _, fs, pool := newLifecycleStorage(t)
	ctx, cancel := context.WithTimeout(context.TODO(), 30*time.Second)
	defer cancel()

	const ns = "honey"
	key := cpPrefixKey + "/" + ns + "/replicaset-web-abcd-web-1111-2222"

	w, err := s.Watch(ctx, cpPrefixKey, storage.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec})
	require.NoError(t, err)
	defer w.Stop()

	profile := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "replicaset-web-abcd-web-1111-2222",
			Namespace:   ns,
			Annotations: map[string]string{},
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Egress: []softwarecomposition.NetworkNeighbor{
				svcNeighbor("honey", "alertmanager", "TCP-9093", 9093),
				{Identifier: "ip-1", Type: "external", IPAddress: "1.2.3.4"},
			},
		},
	}
	require.NoError(t, s.Create(ctx, key, profile, nil, 0))

	recvEvent := func() *watch.Event {
		select {
		case ev := <-w.ResultChan():
			return &ev
		case <-time.After(500 * time.Millisecond):
			return nil
		}
	}
	added := recvEvent()
	require.NotNil(t, added, "Added event expected after create")
	assert.Equal(t, watch.Added, added.Type)

	// update that changes ONLY ServiceRefName
	metaOut := &softwarecomposition.ContainerProfile{}
	tryUpdate := func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		cp := input.(*softwarecomposition.ContainerProfile).DeepCopy()
		for i := range cp.Spec.Egress {
			if cp.Spec.Egress[i].ServiceRefName == "alertmanager" {
				cp.Spec.Egress[i].ServiceRefName = "alertmanager-v2"
			}
		}
		return cp, nil, nil
	}
	require.NoError(t, s.GuaranteedUpdate(ctx, key, metaOut, false, nil, tryUpdate, nil))
	rvAfterUpdate := metaOut.ResourceVersion
	assert.NotEqual(t, "1", rvAfterUpdate, "service-field-only update must bump the ResourceVersion")

	modified := recvEvent()
	require.NotNil(t, modified, "Modified event expected after service-field-only update")
	assert.Equal(t, watch.Modified, modified.Type)
	evCP, ok := modified.Object.(*softwarecomposition.ContainerProfile)
	require.True(t, ok, "full-spec watcher must receive the full object")
	var evName string
	for _, e := range evCP.Spec.Egress {
		if e.ServiceRefNamespace == "honey" {
			evName = e.ServiceRefName
		}
	}
	assert.Equal(t, "alertmanager-v2", evName, "watch payload must carry the updated service field")

	// identical re-update is a no-op: no event, RV unchanged
	noop := func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
		return input.(*softwarecomposition.ContainerProfile).DeepCopy(), nil, nil
	}
	metaOut2 := &softwarecomposition.ContainerProfile{}
	require.NoError(t, s.GuaranteedUpdate(ctx, key, metaOut2, false, nil, noop, nil))
	assert.Equal(t, rvAfterUpdate, metaOut2.ResourceVersion, "identical update must not bump the ResourceVersion")
	assert.Nil(t, recvEvent(), "identical update must not emit a watch event")

	// cold read: a fresh StorageImpl over the same disk+pool sees the new value
	sch := scheme.Scheme
	s2 := NewStorageImpl(fs, DefaultStorageRoot, pool, nil, sch)
	var cold softwarecomposition.ContainerProfile
	require.NoError(t, s2.Get(ctx, key, storage.GetOptions{}, &cold))
	var coldName string
	var coldSelector bool
	for _, e := range cold.Spec.Egress {
		if e.ServiceRefNamespace == "honey" {
			coldName = e.ServiceRefName
		}
		if e.ServiceSelector != nil {
			coldSelector = true
		}
	}
	assert.Equal(t, "alertmanager-v2", coldName, "cold read from disk must return the updated service field")
	assert.False(t, coldSelector)

	// cold list (namespace scan decodes every payload from disk)
	list := softwarecomposition.ContainerProfileList{}
	require.NoError(t, s2.GetByNamespace(ctx, "spdx.softwarecomposition.kubescape.io", "containerprofile", ns, &list))
	require.Len(t, list.Items, 1)
	var listName string
	for _, e := range list.Items[0].Spec.Egress {
		if e.ServiceRefNamespace == "honey" {
			listName = e.ServiceRefName
		}
	}
	assert.Equal(t, "alertmanager-v2", listName, "cold list must return the updated service field")
}
