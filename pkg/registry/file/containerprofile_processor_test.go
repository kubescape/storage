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
		defaultNamespace:        "",
		interval:                0,
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

	ctx, cancel := context.WithCancel(context.TODO())
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

	profile := softwarecomposition.NetworkNeighborhood{}
	key := "/spdx.softwarecomposition.kubescape.io/networkneighborhoods/node-agent-test-hjjz/replicaset-multiple-containers-deployment-d4b8dd5fd"
	err = s.GetWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
	assert.NoError(t, err)
	assert.Equal(t, helpersv1.Full, profile.Annotations[helpersv1.CompletionMetadataKey])
	assert.Equal(t, helpersv1.Completed, profile.Annotations[helpersv1.StatusMetadataKey])

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
