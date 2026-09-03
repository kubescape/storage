package file

import (
	"context"
	"testing"
	"time"

	"github.com/armosec/armoapi-go/armotypes"
	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/kubescape/storage/pkg/utils"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestPreSave_CompletedImmutability_RealGuaranteedUpdate drives the consolidated
// completed-immutability guard through the REAL StorageImpl.GuaranteedUpdate and a
// REAL SQLite pool, rather than a fake whose metadata read aliases the locking
// read. GuaranteedUpdate holds the per-key write lock while it invokes
// processor.PreSave; the guard therefore MUST read stored metadata through the
// no-lock variant (GetContainerProfileMetadataNoLock). If it used the locking
// read it would block on the write lock it is nested inside until the lock
// timeout, the guard would be skipped, and a Completed profile could regress to
// Learning. This test proves the guard reverts the regression without deadlock.
//
// The stored-TooLarge case pins the orthogonal GuaranteedUpdate short-circuit:
// a stored TooLarge profile is immutable, so an incoming patch is dropped
// entirely (never even reaching the consolidated guard) and the stored status
// stays TooLarge.
func TestPreSave_CompletedImmutability_RealGuaranteedUpdate(t *testing.T) {
	const (
		ns   = "kubescape"
		name = "replicaset-nginx-abc123-nginx-1a2b-3c4d"
	)

	tests := []struct {
		name         string
		storedStatus string
		incoming     string
		wantStatus   string
	}{
		{
			name:         "stored Completed cannot regress to Learning",
			storedStatus: helpersv1.Completed,
			incoming:     helpersv1.Learning,
			wantStatus:   helpersv1.Completed, // reverted by the no-lock guard
		},
		{
			name:         "stored TooLarge is immutable, patch dropped",
			storedStatus: helpersv1.TooLarge,
			incoming:     helpersv1.Learning,
			wantStatus:   helpersv1.TooLarge, // GuaranteedUpdate short-circuits
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, key := newGuardTestStorage(t, ns, name)

			// A per-test timeout: were the guard to take the locking read, RLock
			// on the write-locked key would block for lockTimeout and this bounds
			// the failure instead of hanging the suite forever.
			ctx, cancel := context.WithTimeout(context.TODO(), 30*time.Second)
			defer cancel()

			// Seed the stored consolidated profile.
			seed := newGuardProfile(ns, name, tt.storedStatus)
			require.NoError(t, s.Create(ctx, key, seed, &softwarecomposition.ContainerProfile{}, 0))

			// Patch it with an incoming status via the real GuaranteedUpdate path,
			// which acquires the write lock and calls processor.PreSave under it.
			out := &softwarecomposition.ContainerProfile{}
			done := make(chan error, 1)
			go func() {
				done <- s.GuaranteedUpdate(ctx, key, out, false, nil,
					func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
						if cur.Annotations == nil {
							cur.Annotations = map[string]string{}
						}
						cur.Annotations[helpersv1.StatusMetadataKey] = tt.incoming
						return cur, nil, nil
					}, nil)
			}()

			select {
			case err := <-done:
				require.NoError(t, err, "GuaranteedUpdate must not error")
			case <-time.After(20 * time.Second):
				t.Fatal("GuaranteedUpdate deadlocked - the guard likely used the locking metadata read inside the held write lock")
			}

			// Read the persisted profile back and assert its status.
			got := &softwarecomposition.ContainerProfile{}
			require.NoError(t, s.Get(ctx, key, storage.GetOptions{}, got))
			assert.Equal(t, tt.wantStatus, got.Annotations[helpersv1.StatusMetadataKey],
				"persisted status after real GuaranteedUpdate")
		})
	}
}

// newGuardTestStorage builds a StorageImpl backed by a real SQLite pool and a
// real ContainerProfileProcessor (HostType Kubernetes), and returns the storage
// plus the consolidated key the processor's guard derives for (ns, name).
func newGuardTestStorage(t *testing.T, ns, name string) (StorageQuerier, string) {
	t.Helper()
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	t.Cleanup(func() { _ = pool.Close() })

	sch := scheme.Scheme
	require.NoError(t, softwarecomposition.AddToScheme(sch))

	processor := &ContainerProfileProcessor{
		HostType:                armotypes.HostTypeKubernetes,
		MaxContainerProfileSize: 40000,
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
	// goroutine.
	processor.SetStorage(NewContainerProfileStorageImpl(s, pool))

	key := BuildContainerProfileKey(armotypes.ProfileIdentifier{
		ProfileScope: armotypes.ProfileScope{
			HostType:  armotypes.HostTypeKubernetes,
			Namespace: ns,
		},
		Name: name,
	}, ContainerProfileKind)
	return s, key
}

func newGuardProfile(ns, name, status string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Annotations: map[string]string{
				helpersv1.StatusMetadataKey: status,
			},
		},
	}
}

// TestPreSave_TimeSeries_NoDeadlockUnderHeldWriteLock verifies that when a
// time-series profile arrives while the consolidated profile key is write-locked
// (e.g. by a concurrent update or consolidation), PreSave reads metadata via
// GetContainerProfileMetadataNoLock and does NOT block on the held write lock.
func TestPreSave_TimeSeries_NoDeadlockUnderHeldWriteLock(t *testing.T) {
	const (
		ns       = "kubescape"
		baseName = "replicaset-nginx-abc123-nginx-1a2b-3c4d"
		tsName   = "replicaset-nginx-abc123-nginx-1a2b-3c4d-series1"
	)

	s, consolidatedKey := newGuardTestStorage(t, ns, baseName)

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()

	// Seed the stored consolidated profile.
	seed := newGuardProfile(ns, baseName, helpersv1.Learning)
	require.NoError(t, s.Create(ctx, consolidatedKey, seed, &softwarecomposition.ContainerProfile{}, 0))

	// Acquire the write lock on the consolidated key to simulate a concurrent writer/consolidation.
	impl := s.(*StorageImpl)
	require.NoError(t, impl.locks.Lock(ctx, consolidatedKey))
	defer impl.locks.Unlock(consolidatedKey)

	// Ingest a time-series profile for the same workload while the consolidated key is write-locked.
	// PreSave checks the consolidated profile's metadata. If it used the locking read, this would
	// block on impl.locks.Lock above until lockTimeout. With GetContainerProfileMetadataNoLock,
	// it reads SQLite metadata without deadlock.
	tsProfile := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tsName,
			Namespace: ns,
			Annotations: map[string]string{
				helpersv1.ReportSeriesIdMetadataKey: "series1",
				helpersv1.StatusMetadataKey:         helpersv1.Learning,
			},
		},
	}

	done := make(chan error, 1)
	go func() {
		// Embed a pooled connection in ctx for PreSave
		poolCtx, poolCancel := poolContext()
		defer poolCancel()
		conn, err := impl.pool.Take(poolCtx)
		if err != nil {
			done <- err
			return
		}
		defer impl.pool.Put(conn)
		presaveCtx := context.WithValue(ctx, connKey, conn)
		done <- impl.processor.PreSave(presaveCtx, tsProfile)
	}()

	select {
	case err := <-done:
		require.NoError(t, err, "PreSave must succeed without error")
	case <-time.After(2 * time.Second):
		t.Fatal("PreSave deadlocked or blocked on the held write lock of the consolidated key")
	}
}

