package file

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/registry/file/dynamicpathdetector"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

func TestDeflateContainerProfileSpec_NetworkNeighborsCollapse(t *testing.T) {
	const hostCount = 60
	newIngress := func() []softwarecomposition.NetworkNeighbor {
		ingress := make([]softwarecomposition.NetworkNeighbor, 0, hostCount)
		for i := 1; i <= hostCount; i++ {
			ingress = append(ingress, softwarecomposition.NetworkNeighbor{
				Identifier: fmt.Sprintf("external-%d", i),
				Type:       "external",
				IPAddress:  fmt.Sprintf("10.0.0.%d", i),
				Ports:      []softwarecomposition.NetworkPort{{Name: "80"}},
			})
		}
		return ingress
	}

	settings := dynamicpathdetector.CollapseSettings{
		NetworkIPGroupThreshold: 10,
		NetworkCIDRFloorBits:    24,
	}

	container := softwarecomposition.ContainerProfileSpec{
		Ingress: newIngress(),
	}

	result := DeflateContainerProfileSpec(container, nil, settings)

	assert.Len(t, result.Ingress, 1, "expected all same-group host IPs to collapse into a single CIDR entry")
	assert.Empty(t, result.Ingress[0].IPAddress)
	assert.Equal(t, []string{"10.0.0.0/26"}, result.Ingress[0].IPAddresses)
	assert.Equal(t, []softwarecomposition.NetworkPort{{Name: "80"}}, result.Ingress[0].Ports)

	// Confirm both call sites (NetworkNeighborhoodProcessor's deflateNetworkNeighbors
	// and DeflateContainerProfileSpec's) collapse identically given the same settings.
	directResult := deflateNetworkNeighbors(newIngress(), settings)
	assert.Equal(t, directResult, result.Ingress)
}

func TestConsolidateData(t *testing.T) {
	// Prepare pool and connection
	pool := NewTestPool("/tmp")
	require.NotNil(t, pool)
	defer func(pool *sqlitemigration.Pool) {
		_ = pool.Close()
	}(pool)
	var err error
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

	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	defer pool.Put(conn)

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
		helpersv1.TemplateHashKey:             "d4b8dd5fd",
		helpersv1.ApiGroupMetadataKey:         "apps",
		helpersv1.ApiVersionMetadataKey:       "v1",
		helpersv1.RelatedKindMetadataKey:      "Deployment",
		helpersv1.RelatedNameMetadataKey:      "multiple-containers-deployment",
		helpersv1.RelatedNamespaceMetadataKey: "node-agent-test-hjjz",
		helpersv1.ResourceVersionMetadataKey:  "1448",
	}, applicationProfile.Labels)

	containerProfile := softwarecomposition.ContainerProfile{}
	key = "/spdx.softwarecomposition.kubescape.io/containerprofile/kube-system/replicaset-coredns-5d78c9869d-coredns-185f-129c"
	err = s.GetWithConn(ctx, conn, key, storage.GetOptions{}, &containerProfile)
	assert.NoError(t, err)
	assert.Equal(t, softwarecomposition.CallID("test-call-id"), containerProfile.Spec.IdentifiedCallStacks[0].CallID)

}

// newConsolidationTestProcessor builds a ContainerProfileProcessor backed by an
// in-memory-ish temp SQLite pool for the AC3 concurrency tests. The returned
// cleanup closes the pool.
func newConsolidationTestProcessor(t *testing.T, deleteThreshold time.Duration) (*ContainerProfileProcessor, *sqlitemigration.Pool, func()) {
	t.Helper()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))
	processor := &ContainerProfileProcessor{
		DeleteThreshold:         deleteThreshold,
		MaxContainerProfileSize: 40000,
		Workers:                 4,
	}
	s := &StorageImpl{
		appFs:           afero.NewMemMapFs(),
		pool:            pool,
		locks:           utils.NewMapMutex[string](),
		processor:       processor,
		root:            DefaultStorageRoot,
		scheme:          sch,
		versioner:       storage.APIObjectVersioner{},
		watchDispatcher: NewWatchDispatcher(),
	}
	// Interval stays 0 so SetStorage does not spawn the background maintenance
	// goroutine that would race the explicit ConsolidateTimeSeries calls.
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))

	return processor, pool, func() { _ = pool.Close() }
}

// seedTimeSeriesRow inserts a single time_series row so ConsolidateTimeSeries's
// list queries observe it. Returns the storage key the list functions produce.
func seedTimeSeriesRow(t *testing.T, pool *sqlitemigration.Pool, ns, name, seriesID, tsSuffix, reportTimestamp string, hasData bool) string {
	t.Helper()
	conn, err := pool.Take(context.TODO())
	require.NoError(t, err)
	defer pool.Put(conn)
	err = WriteTimeSeriesEntry(conn, "containerprofile", ns, name, seriesID, tsSuffix, reportTimestamp, helpersv1.Learning, helpersv1.Partial, "", hasData)
	require.NoError(t, err)
	return K8sKeysToPath("", "spdx.softwarecomposition.kubescape.io", "containerprofile", "", ns, name)
}

// TestConsolidateTimeSeries_Concurrent_NoDeadlock drives the worker pool over
// multiple keys (including several containers that share one workload apKey) and
// asserts it completes without deadlock/race and leaves the pool fully returned.
func TestConsolidateTimeSeries_Concurrent_NoDeadlock(t *testing.T) {
	processor, pool, cleanup := newConsolidationTestProcessor(t, 0)
	defer cleanup()
	s := processor.ContainerProfileStorage.(*ContainerProfileStorageImpl).storageImpl

	ctx, cancel := context.WithTimeout(context.TODO(), 15*time.Second)
	defer cancel()

	create := func(f string) {
		content, err := os.ReadFile(f)
		require.NoError(t, err)
		var profile softwarecomposition.ContainerProfile
		require.NoError(t, json.Unmarshal(content, &profile))
		require.NoError(t, s.Create(ctx, "/spdx.softwarecomposition.kubescape.io/containerprofile/"+profile.Namespace+"/"+profile.Name, &profile, nil, 0))
	}
	// consolidate runs a pass under a deadlock guard (Workers=4, so keys sharing
	// the multiple-containers workload apKey are processed concurrently).
	consolidate := func() {
		done := make(chan error, 1)
		go func() { done <- processor.ConsolidateTimeSeries(ctx) }()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("ConsolidateTimeSeries did not complete in time - possible deadlock")
		}
	}
	// Same staged create/consolidate flow as TestConsolidateData (known-good final
	// state), but with a concurrent worker pool exercising per-key isolation.
	create("testdata/p1.json")
	create("testdata/p2.json")
	create("testdata/p3.json")
	create("testdata/p4.json")
	consolidate()
	create("testdata/p5.json")
	create("testdata/p6.json")
	create("testdata/p7.json")
	consolidate()
	create("testdata/p8.json")
	create("testdata/p9.json")
	consolidate()
	create("testdata/p10.json")
	create("testdata/p11.json")
	create("testdata/p12.json")
	consolidate()

	// The multiple-containers workload consolidated correctly into one application profile.
	ap := softwarecomposition.ApplicationProfile{}
	conn, err := pool.Take(ctx)
	require.NoError(t, err)
	err = s.GetWithConn(ctx, conn, "/spdx.softwarecomposition.kubescape.io/applicationprofiles/node-agent-test-hjjz/replicaset-multiple-containers-deployment-d4b8dd5fd", storage.GetOptions{}, &ap)
	pool.Put(conn)
	require.NoError(t, err)
	delete(ap.Annotations, helpersv1.SyncChecksumMetadataKey)
	assert.Equal(t, map[string]string{
		helpersv1.CompletionMetadataKey: helpersv1.Full,
		helpersv1.InstanceIDMetadataKey: "apiVersion-apps/v1/namespace-node-agent-test-hjjz/kind-ReplicaSet/name-multiple-containers-deployment-d4b8dd5fd",
		helpersv1.StatusMetadataKey:     helpersv1.Completed,
		helpersv1.WlidMetadataKey:       "wlid://cluster-kind-kind/namespace-node-agent-test-hjjz/deployment-multiple-containers-deployment",
	}, ap.Annotations)

	// No connection leak: every pool connection must be re-acquirable promptly.
	acqCtx, acqCancel := context.WithTimeout(ctx, 3*time.Second)
	defer acqCancel()
	var conns []*sqlite.Conn
	for i := 0; i < DefaultPoolSize; i++ {
		c, err := pool.Take(acqCtx)
		require.NoErrorf(t, err, "failed to re-acquire connection %d - pool leak", i)
		conns = append(conns, c)
	}
	for _, c := range conns {
		pool.Put(c)
	}
}

// TestConsolidateTimeSeries_Dedup asserts a key that appears multiple times
// within one list and across both the expired and withData lists is processed
// exactly once, with expired precedence.
func TestConsolidateTimeSeries_Dedup(t *testing.T) {
	processor, pool, cleanup := newConsolidationTestProcessor(t, time.Hour)
	defer cleanup()

	old := time.Now().Add(-2 * time.Hour).String()      // < now-1h threshold => expired
	recent := time.Now().Add(-1 * time.Minute).String() // > threshold => not expired

	// keyA: two rows, both expired AND hasData => appears twice in each list and in both lists.
	keyA := seedTimeSeriesRow(t, pool, "ns-a", "name-a", "series-a", "1", old, true)
	seedTimeSeriesRow(t, pool, "ns-a", "name-a", "series-a", "2", old, true)
	// keyB: recent + hasData => only in withData list.
	keyB := seedTimeSeriesRow(t, pool, "ns-b", "name-b", "series-b", "1", recent, true)
	// keyC: expired + no data => only in expired list.
	keyC := seedTimeSeriesRow(t, pool, "ns-c", "name-c", "series-c", "1", old, false)

	var mu sync.Mutex
	count := map[string]int{}
	expiredSeen := map[string]bool{}
	processor.consolidateKey = func(_ context.Context, key string, expired bool) error {
		mu.Lock()
		defer mu.Unlock()
		count[key]++
		expiredSeen[key] = expired
		return nil
	}

	require.NoError(t, processor.ConsolidateTimeSeries(context.TODO()))

	assert.Equal(t, 3, len(count), "each unique key must be dispatched once")
	assert.Equal(t, 1, count[keyA])
	assert.Equal(t, 1, count[keyB])
	assert.Equal(t, 1, count[keyC])
	assert.True(t, expiredSeen[keyA], "keyA present in both lists must run as expired (expired precedence)")
	assert.False(t, expiredSeen[keyB])
	assert.True(t, expiredSeen[keyC])
}

// TestConsolidateTimeSeries_ErrorIsolation asserts that when exactly one key
// fails, the other keys still complete (no cross-worker cancellation) while
// ConsolidateTimeSeries surfaces the failure. A cancel-on-first-error group
// context (the errgroup.WithContext regression) would cancel the parent ctx the
// other workers observe, so they would not record completion.
func TestConsolidateTimeSeries_ErrorIsolation(t *testing.T) {
	processor, pool, cleanup := newConsolidationTestProcessor(t, time.Hour)
	defer cleanup()

	recent := time.Now().Add(-1 * time.Minute).String()
	var keys []string
	for _, n := range []string{"k0", "k1", "k2", "k3"} {
		keys = append(keys, seedTimeSeriesRow(t, pool, "ns", n, "series", "1", recent, true))
	}
	poison := keys[2]

	var committed sync.Map
	injected := errors.New("injected failure")
	processor.consolidateKey = func(ctx context.Context, key string, _ bool) error {
		if key == poison {
			return injected
		}
		// A cancelled parent ctx (WithContext regression) surfaces here before commit.
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
		committed.Store(key, true)
		return nil
	}

	err := processor.ConsolidateTimeSeries(context.TODO())
	require.Error(t, err)
	assert.ErrorIs(t, err, injected)

	var n int
	committed.Range(func(k, _ any) bool {
		assert.NotEqual(t, poison, k)
		n++
		return true
	})
	assert.Equal(t, len(keys)-1, n, "all non-failing keys must commit despite one failure")
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
	t.Skip("Skipping send consolidated slug to channel test")
	tests := []struct {
		name           string
		channel        chan ConsolidatedSlugData
		profile        softwarecomposition.ContainerProfile
		id             armotypes.ProfileIdentifier
		ctx            context.Context
		expectError    bool
		expectedSlug   string
		expectedResult bool // whether we expect data in channel
	}{
		{
			name:           "nil channel returns nil",
			channel:        nil,
			profile:        softwarecomposition.ContainerProfile{},
			id:             armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{Namespace: "default"}},
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
			id:             armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{Namespace: "default"}},
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
			id:             armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{Namespace: "default"}},
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
			id:             armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{Namespace: "default"}},
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
			id: armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{Namespace: "default"}},
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
			id:             armotypes.ProfileIdentifier{ProfileScope: armotypes.ProfileScope{Namespace: "kube-system"}},
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

			err := processor.sendConsolidatedSlugToChannel(tt.ctx, tt.profile, tt.id)

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
					assert.Equal(t, tt.id.Namespace, slugData.Namespace)
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

func TestUpdateProfileStatusExpired(t *testing.T) {
	processor := ContainerProfileProcessor{}

	profile := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
	}

	ts := []softwarecomposition.TimeSeriesContainers{
		{
			Status:   helpersv1.Learning,
			TsSuffix: "123",
		},
	}

	res, skip, err := processor.updateProfileStatus(context.TODO(), "key", "seriesID", profile, ts, true)
	assert.NoError(t, err)
	assert.False(t, skip)
	assert.Len(t, res, 0) // should be cleared
	assert.Equal(t, helpersv1.Completed, profile.Annotations[helpersv1.StatusMetadataKey])
	assert.Equal(t, helpersv1.Partial, profile.Annotations[helpersv1.CompletionMetadataKey])
}

type mockContainerProfileStorage struct {
	fakeStorage
	deleteCalled bool
	deleteKey    string
}

func (m *mockContainerProfileStorage) DeleteTimeSeriesContainerEntries(ctx context.Context, key string) error {
	m.deleteCalled = true
	m.deleteKey = key
	return nil
}

func TestUpdateProfileStatusExpiredFull(t *testing.T) {
	mockStorage := &mockContainerProfileStorage{}
	processor := ContainerProfileProcessor{
		ContainerProfileStorage: mockStorage,
	}

	profile := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{},
		},
	}

	// For isFull to be true: len(newTimeSeries) == 1, previous report timestamp is zero, status is Completed.
	ts := []softwarecomposition.TimeSeriesContainers{
		{
			Status:                  helpersv1.Completed,
			Completion:              helpersv1.Full,
			TsSuffix:                "123",
			PreviousReportTimestamp: "0001-01-01 00:00:00 +0000 UTC",
		},
	}

	res, skip, err := processor.updateProfileStatus(context.TODO(), "test-key", "seriesID", profile, ts, true)
	assert.NoError(t, err)
	assert.True(t, skip)
	assert.Len(t, res, 0) // should be cleared
	assert.True(t, mockStorage.deleteCalled)
	assert.Equal(t, "test-key", mockStorage.deleteKey)
	assert.Equal(t, helpersv1.Completed, profile.Annotations[helpersv1.StatusMetadataKey])
	assert.Equal(t, helpersv1.Full, profile.Annotations[helpersv1.CompletionMetadataKey])
}
