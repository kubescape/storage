package file

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

func TestConsolidateData(t *testing.T) {
	// Prepare pool and connection
	pool := NewTestPool("/tmp")
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	conn, err := pool.Take(context.TODO())
	require.NoError(t, err)

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	processor := ContainerProfileProcessor{
		deleteThreshold:         0, // disable deletion
		maxContainerProfileSize: 40000,
		pool:                    pool,
	}
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       &processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	processor.SetStorage(s)

	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	create := func(f string) {
		content, err := os.ReadFile(f)
		require.NoError(t, err)
		var profile softwarecomposition.ContainerProfile
		err = json.Unmarshal(content, &profile)
		require.NoError(t, err)
		err = s.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/"+profile.Namespace+"/"+profile.Name, &profile, nil, 0)
		require.NoError(t, err)
	}

	create("testdata/p1.json")
	create("testdata/p2.json")
	create("testdata/p3.json")
	create("testdata/p4.json")
	err = processor.consolidateTimeSeries()
	assert.NoError(t, err)
	create("testdata/p5.json")
	create("testdata/p6.json")
	create("testdata/p7.json")
	err = processor.consolidateTimeSeries()
	assert.NoError(t, err)
	create("testdata/p8.json")
	create("testdata/p9.json")
	err = processor.consolidateTimeSeries()
	assert.NoError(t, err)
	create("testdata/p10.json")
	create("testdata/p11.json")
	create("testdata/p12.json")
	err = processor.consolidateTimeSeries()
	assert.NoError(t, err)

	applicationProfile := softwarecomposition.ApplicationProfile{}
	key := "/spdx.softwarecomposition.kubescape.io/applicationprofiles/node-agent-test-hjjz/replicaset-multiple-containers-deployment-d4b8dd5fd"
	err = s.GetWithConn(ctx, conn, key, storage.GetOptions{}, &applicationProfile)
	assert.NoError(t, err)
	delete(applicationProfile.Annotations, helpersv1.SyncChecksumMetadataKey) // checksum depends on creation time
	assert.Equal(t, map[string]string{
		helpersv1.CompletionMetadataKey: helpersv1.Full,
		helpersv1.InstanceIDMetadataKey: "apiVersion-apps/v1/namespace-node-agent-test-hjjz/kind-ReplicaSet/name-multiple-containers-deployment-d4b8dd5fd",
		helpersv1.StatusMetadataKey:     helpersv1.Completed,
		helpersv1.WlidMetadataKey:       "wlid://cluster-kind-kind/namespace-node-agent-test-hjjz/deployment-multiple-containers-deployment",
	}, applicationProfile.Annotations)
	assert.Equal(t, map[string]string{
		helpersv1.TemplateHashKey:            "d4b8dd5fd",
		helpersv1.ApiGroupMetadataKey:        "apps",
		helpersv1.ApiVersionMetadataKey:      "v1",
		helpersv1.KindMetadataKey:            "Deployment",
		helpersv1.NameMetadataKey:            "multiple-containers-deployment",
		helpersv1.NamespaceMetadataKey:       "node-agent-test-hjjz",
		helpersv1.ResourceVersionMetadataKey: "1448",
	}, applicationProfile.Labels)

	containerProfile := softwarecomposition.ContainerProfile{}
	key = "/spdx.softwarecomposition.kubescape.io/containerprofile/kube-system/replicaset-coredns-5d78c9869d-coredns-185f-129c"
	err = s.GetWithConn(ctx, conn, key, storage.GetOptions{}, &containerProfile)
	assert.NoError(t, err)
	assert.Equal(t, softwarecomposition.CallID("test-call-id"), containerProfile.Spec.IdentifiedCallStacks[0].CallID)

	// Clean up
	pool.Put(conn)
	assert.NoError(t, pool.Close())
}

func Test_isZeroTime(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{
			name: "empty string",
			s:    "",
			want: true,
		},
		{
			name: "zero time string",
			s:    time.Time{}.String(),
			want: true,
		},
		{
			name: "non-zero time string",
			s:    time.Now().String(),
			want: false,
		},
		{
			name: "zero RFC3339 string",
			s:    time.Time{}.Format(time.RFC3339),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equalf(t, tt.want, isZeroTime(tt.s), tt.s)
		})
	}
}
