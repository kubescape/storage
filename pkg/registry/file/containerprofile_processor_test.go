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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		DeleteThreshold:         0, // disable deletion
		MaxContainerProfileSize: 40000,
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
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))

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
	err = processor.ConsolidateTimeSeries(ctx)
	assert.NoError(t, err)
	create("testdata/p5.json")
	create("testdata/p6.json")
	create("testdata/p7.json")
	err = processor.ConsolidateTimeSeries(ctx)
	assert.NoError(t, err)
	create("testdata/p8.json")
	create("testdata/p9.json")
	err = processor.ConsolidateTimeSeries(ctx)
	assert.NoError(t, err)
	create("testdata/p10.json")
	create("testdata/p11.json")
	create("testdata/p12.json")
	err = processor.ConsolidateTimeSeries(ctx)
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

func TestSendConsolidatedSlugToChannel(t *testing.T) {
	tests := []struct {
		name           string
		channel        chan ConsolidatedSlugData
		profile        softwarecomposition.ContainerProfile
		namespace      string
		ctx            context.Context
		expectError    bool
		expectedSlug   string
		expectedResult bool // whether we expect data in channel
	}{
		{
			name:           "nil channel returns nil",
			channel:        nil,
			profile:        softwarecomposition.ContainerProfile{},
			namespace:      "default",
			ctx:            context.Background(),
			expectError:    false,
			expectedResult: false,
		},
		{
			name:    "missing instance ID annotation",
			channel: make(chan ConsolidatedSlugData, 1),
			profile: softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{},
				},
			},
			namespace:      "default",
			ctx:            context.Background(),
			expectError:    true,
			expectedResult: false,
		},
		{
			name:    "invalid instance ID",
			channel: make(chan ConsolidatedSlugData, 1),
			profile: softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpersv1.InstanceIDMetadataKey: "invalid-instance-id",
					},
				},
			},
			namespace:      "default",
			ctx:            context.Background(),
			expectError:    true,
			expectedResult: false,
		},
		{
			name:    "successful send to channel",
			channel: make(chan ConsolidatedSlugData, 1),
			profile: softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpersv1.InstanceIDMetadataKey: "apiVersion-apps/v1/namespace-default/kind-Deployment/name-test-app",
					},
				},
			},
			namespace:      "default",
			ctx:            context.Background(),
			expectError:    false,
			expectedSlug:   "deployment-test-app", // GetSlug(true) includes kind prefix
			expectedResult: true,
		},
		{
			name: "context cancellation",
			channel: func() chan ConsolidatedSlugData {
				// Use buffered channel of size 1, but fill it so the next send will block
				ch := make(chan ConsolidatedSlugData, 1)
				// Fill the channel to make subsequent send block
				ch <- ConsolidatedSlugData{Name: "blocking", Namespace: "test"}
				return ch
			}(),
			profile: softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpersv1.InstanceIDMetadataKey: "apiVersion-apps/v1/namespace-default/kind-Deployment/name-test-app",
					},
				},
			},
			namespace: "default",
			ctx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel immediately
				return ctx
			}(),
			expectError:    true,
			expectedResult: false,
		},
		{
			name:    "different namespace",
			channel: make(chan ConsolidatedSlugData, 1),
			profile: softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpersv1.InstanceIDMetadataKey: "apiVersion-apps/v1/namespace-kube-system/kind-Deployment/name-coredns",
					},
				},
			},
			namespace:      "kube-system",
			ctx:            context.Background(),
			expectError:    false,
			expectedSlug:   "deployment-coredns", // GetSlug(true) includes kind prefix
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processor := &ContainerProfileProcessor{
				ConsolidatedSlugChannel: tt.channel,
			}

			err := processor.sendConsolidatedSlugToChannel(tt.ctx, tt.profile, tt.namespace)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectedResult {
				// Verify data was sent to channel
				select {
				case slugData := <-tt.channel:
					assert.Equal(t, tt.expectedSlug, slugData.Name)
					assert.Equal(t, tt.namespace, slugData.Namespace)
				case <-time.After(100 * time.Millisecond):
					t.Fatal("Expected data in channel but none received")
				}
			} else if tt.channel != nil && tt.name != "context cancellation" {
				// Verify no data was sent (except for context cancellation test which has blocking data)
				select {
				case <-tt.channel:
					t.Fatal("Unexpected data in channel")
				case <-time.After(10 * time.Millisecond):
					// Expected - no data
				}
			} else if tt.name == "context cancellation" {
				// For context cancellation, drain the blocking data we put in
				select {
				case <-tt.channel:
					// Expected - this is the blocking data we put in
				case <-time.After(10 * time.Millisecond):
					// No blocking data (shouldn't happen)
				}
			}
		})
	}
}
