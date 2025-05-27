package file

import (
	"context"
	"testing"
	"time"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		maxContainerProfileSize: 10,
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

	err = s.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/default/job-test-curl-cronjob-manual-tbd-curl-tester-9e3c-219c-1748847136", &softwarecomposition.ContainerProfile{
		ObjectMeta: v1.ObjectMeta{
			Annotations: map[string]string{
				helpers.CompletionMetadataKey:      helpers.Full,
				helpers.ContainerTypeMetadataKey:   "containers",
				helpers.InstanceIDMetadataKey:      "apiVersion-batch/v1/namespace-default/kind-Job/name-test-curl-cronjob-manual-tbd/containerName-curl-tester",
				helpers.ReportSeriesIdMetadataKey:  "f8a191ee-cfcb-4082-9f23-4f3759b45a0f",
				helpers.ReportTimestampMetadataKey: "2025-06-02T06:52:16Z",
				helpers.StatusMetadataKey:          helpers.Learning,
				helpers.WlidMetadataKey:            "wlid://cluster-kind-kind/namespace-default/cronjob-test-curl-cronjob",
			},
			Labels: map[string]string{
				helpers.ApiGroupMetadataKey:      "batch",
				helpers.ApiVersionMetadataKey:    "v1",
				helpers.ContainerNameMetadataKey: "curl-tester",
				helpers.KindMetadataKey:          "CronJob",
				helpers.NameMetadataKey:          "test-curl-cronjob",
				helpers.NamespaceMetadataKey:     "default",
			},
			Name:      "job-test-curl-cronjob-manual-tbd-curl-tester-9e3c-219c-1748847136",
			Namespace: "default",
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Architectures: []string{"amd64"},
			Execs: []softwarecomposition.ExecCalls{
				{Path: "/usr/bin/basename"},
				{Path: "/usr/bin/readlink"},
			},
			Opens: []softwarecomposition.OpenCalls{
				{Path: "/var/lib/dpkg"},
				{Path: "/var/log/dpkg.log"},
			},
		},
	}, nil, 0)
	require.NoError(t, err)

	err = s.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/default/job-test-curl-cronjob-manual-tbd-curl-tester-9e3c-219c-1748847175", &softwarecomposition.ContainerProfile{
		ObjectMeta: v1.ObjectMeta{
			Annotations: map[string]string{
				helpers.CompletionMetadataKey:              helpers.Full,
				helpers.ContainerTypeMetadataKey:           "containers",
				helpers.InstanceIDMetadataKey:              "apiVersion-batch/v1/namespace-default/kind-Job/name-test-curl-cronjob-manual-tbd/containerName-curl-tester",
				helpers.PreviousReportTimestampMetadataKey: "2025-06-02T06:52:16Z",
				helpers.ReportSeriesIdMetadataKey:          "f8a191ee-cfcb-4082-9f23-4f3759b45a0f",
				helpers.ReportTimestampMetadataKey:         "2025-06-02T06:52:55Z",
				helpers.StatusMetadataKey:                  helpers.Learning,
				helpers.WlidMetadataKey:                    "wlid://cluster-kind-kind/namespace-default/cronjob-test-curl-cronjob",
			},
			Labels: map[string]string{
				helpers.ApiGroupMetadataKey:      "batch",
				helpers.ApiVersionMetadataKey:    "v1",
				helpers.ContainerNameMetadataKey: "curl-tester",
				helpers.KindMetadataKey:          "CronJob",
				helpers.NameMetadataKey:          "test-curl-cronjob",
				helpers.NamespaceMetadataKey:     "default",
			},
			Name:      "job-test-curl-cronjob-manual-tbd-curl-tester-9e3c-219c-1748847175",
			Namespace: "default",
		},
		Spec: softwarecomposition.ContainerProfileSpec{
			Architectures: []string{"amd64"},
			Execs: []softwarecomposition.ExecCalls{
				{Path: "/usr/bin/date"},
			},
			Opens: []softwarecomposition.OpenCalls{
				{Path: "/etc/ld.so.cache"},
				{Path: "/usr/lib/x86_64-linux-gnu/libc.so.6"},
			},
			LabelSelector: v1.LabelSelector{
				MatchLabels: map[string]string{"app": "wikijs"},
			},
		},
	}, nil, 0)
	require.NoError(t, err)

	keys, err := ListTimeSeriesKeys(conn)
	assert.NoError(t, err)
	assert.Len(t, keys, 2)

	for _, key := range keys {
		containers, err := ListTimeSeriesContainers(conn, key)
		assert.NoError(t, err)
		assert.Len(t, containers, 1)
	}

	err = processor.consolidateTimeSeries()
	assert.NoError(t, err)

	profile := softwarecomposition.ContainerProfile{}
	key := "/spdx.softwarecomposition.kubescape.io/containerprofile/default/job-test-curl-cronjob-manual-tbd-curl-tester-9e3c-219c"
	err = s.GetWithConn(ctx, conn, key, storage.GetOptions{}, &profile)
	assert.NoError(t, err)
	assert.Equal(t, "wikijs", profile.Spec.LabelSelector.MatchLabels["app"])

	apProcessor := ApplicationProfileProcessor{
		defaultNamespace:          "",
		maxApplicationProfileSize: 10,
		storageImpl:               s,
	}
	apStorage := NewApplicationProfileStorage(NewStorageImplWithCollector(s.appFs, s.root, s.pool, s.watchDispatcher, s.scheme, &apProcessor))
	applicationprofile := softwarecomposition.ApplicationProfile{}
	apKey := "/spdx.softwarecomposition.kubescape.io/applicationprofiles/default/job-test-curl-cronjob-manual-tbd"
	err = apStorage.Get(ctx, apKey, storage.GetOptions{}, &applicationprofile)
	assert.NoError(t, err)

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
