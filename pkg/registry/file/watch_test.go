package file

import (
	"context"
	"testing"
	"time"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/clientset/versioned/scheme"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite/sqlitemigration"
)

const (
	chanWaitTimeout = 100 * time.Millisecond
)

func TestExtractKeysToNotify(t *testing.T) {
	tt := []struct {
		name         string
		inputKey     string
		expectedKeys []string
	}{
		{
			"root key should produce only itself",
			"/",
			[]string{"/"},
		},
		{
			"API resource key should produce root and itself",
			"/spdx.softwarecomposition.kubescape.io",
			[]string{"/", "/spdx.softwarecomposition.kubescape.io"},
		},
		{
			"Full resource key should produce the full lineage",
			"/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds/kubescape/titi",
			[]string{
				"/",
				"/spdx.softwarecomposition.kubescape.io",
				"/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds",
				"/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds/kubescape",
				"/spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds/kubescape/titi",
			},
		},
		{
			"Missing leading slash should produce an error",
			"spdx.softwarecomposition.kubescape.io/sbomsyftfiltereds/kubescape/titi",
			[]string{},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got := extractKeysToNotify(tc.inputKey)
			assert.Equal(t, tc.expectedKeys, got)
		})
	}

}

func TestFileSystemStorageWatchReturnsDistinctWatchers(t *testing.T) {
	type args struct {
		key  string
		opts storage.ListOptions
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "Watch should return new watch objects for the same key for every invocation",
			args: args{
				key: "/spdx.softwarecomposition.kubescape.io/sbomsyfts",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, nil, nil, nil)

			got1, _ := s.Watch(context.TODO(), tt.args.key, tt.args.opts)
			got1chan := got1.ResultChan()

			got2, _ := s.Watch(context.TODO(), tt.args.key, tt.args.opts)
			got2chan := got2.ResultChan()

			assert.NotEqual(t, got1, got2, "Should not return the same watcher object")
			assert.NotEqual(t, got1chan, got2chan, "Channels from the watches should not be the same")
		})
	}
}

func TestFilesystemStorageWatchPublishing(t *testing.T) {
	var (
		keyN = "/spdx.softwarecomposition.kubescape.io/sbomsyfts"
		keyK = "/spdx.softwarecomposition.kubescape.io/sbomsyfts"
		obj  = &v1beta1.SBOMSyft{ObjectMeta: v1.ObjectMeta{
			Name:            "some-sbom",
			ResourceVersion: "1",
			Annotations: map[string]string{
				helpers.SyncChecksumMetadataKey: "58964290770ed17fd375e3c7ef02d0af5d52ca954c65fb2add8c75ff144bf0b1",
			},
		}}
	)
	tt := []struct {
		name                         string
		start, stopBefore, stopAfter map[string]int
		inputObjects                 map[string]*v1beta1.SBOMSyft
		want                         map[string][]watch.Event
	}{{
		name:  "Create should publish to the appropriate single channel",
		start: map[string]int{keyK: 1},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyK + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		want: map[string][]watch.Event{keyK: {{Type: watch.Added, Object: obj}}},
	}, {
		name:  "Create should publish to all watchers on the relevant key",
		start: map[string]int{keyK: 3},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyK + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		want: map[string][]watch.Event{keyK: {
			{Type: watch.Added, Object: obj},
			{Type: watch.Added, Object: obj},
			{Type: watch.Added, Object: obj},
		}},
	}, {
		name:  "Creating on key different than the watch should produce no event",
		start: map[string]int{keyK: 3, keyN: 1},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
	}, {
		name:  "Creating on key not being watched should produce no events",
		start: map[string]int{keyK: 1},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
	}, {
		name:  "Sending to stopped watch should not produce an event",
		start: map[string]int{keyN: 3},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		stopBefore: map[string]int{keyN: 1},
		want: map[string][]watch.Event{keyN: {
			{Type: watch.Added, Object: obj},
			{Type: watch.Added, Object: obj},
		}},
	}, {
		name:  "Stopping watch after send shouldn't deadlock",
		start: map[string]int{keyN: 3},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		stopAfter: map[string]int{keyN: 0},
		want: map[string][]watch.Event{keyN: {
			{Type: watch.Added, Object: obj},
			{Type: watch.Added, Object: obj},
			{Type: watch.Added, Object: obj},
		}},
	}, {
		name:  "Stopping watch twice is ok",
		start: map[string]int{keyN: 3},
		inputObjects: map[string]*v1beta1.SBOMSyft{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		stopBefore: map[string]int{keyN: 1},
		stopAfter:  map[string]int{keyN: 1},
		want: map[string][]watch.Event{keyN: {
			{Type: watch.Added, Object: obj},
			{Type: watch.Added, Object: obj},
		}},
	}}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)
			sch := scheme.Scheme
			require.NoError(t, softwarecomposition.AddToScheme(sch))
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
			defer cancel()
			opts := storage.ListOptions{}

			// Arrange watches
			watchers := map[string][]watch.Interface{}
			for key, watchCount := range tc.start {
				for i := 0; i < watchCount; i++ {
					w, _ := s.Watch(ctx, key, opts)
					watchers[key] = append(watchers[key], w)
				}
			}

			// Primitives to stop the watchers gracefully
			var (
				done = make(chan bool, 1)
				wait = func() {
					select {
					case <-done:
					case <-time.After(chanWaitTimeout):
						t.Errorf("Timed out trying to stop watches")
					}
				}
				stopWatchers = func(ws map[string]int) {
					for key, i := range ws {
						watchers[key][i].Stop()
					}
					done <- true
				}
			)

			go stopWatchers(tc.stopBefore)
			wait()
			{ // Act out the creation operation
				var ttl uint64 = 0
				out := &v1beta1.SBOMSyft{}
				for key, object := range tc.inputObjects {
					_ = s.Create(ctx, key, object, out, ttl)
				}
				time.Sleep(chanWaitTimeout) // Create notifications happen asynchronously
			}
			go stopWatchers(tc.stopAfter)
			wait()

			// Assert the expected events
			for key, wantEvents := range tc.want {
				var gotEvents []watch.Event
				for _, w := range watchers[key] {
					select {
					case ev, ok := <-w.ResultChan():
						// Skip values from closed channels
						if !ok {
							continue
						}
						gotEvents = append(gotEvents, ev)
					case <-time.After(chanWaitTimeout):
						// Timed out, no event received
						continue
					}
				}
				assert.Equal(t, wantEvents, gotEvents)
			}
		})
	}
}

func TestWatchGuaranteedUpdateProducesMatchingEvents(t *testing.T) {
	toto := &v1beta1.SBOMSyft{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "1",
			Annotations:     map[string]string{},
		},
	}

	type args struct {
		key                  string
		ignoreNotFound       bool
		preconditions        *storage.Preconditions
		tryUpdate            storage.UpdateFunc
		cachedExistingObject runtime.Object
	}

	tt := []struct {
		name              string
		inputWatchesByKey map[string]int
		expectedEvents    map[string][]watch.Event
		args              args
	}{
		{
			name: "Successful GuaranteedUpdate should produce a matching Modified event",
			inputWatchesByKey: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomsyfts": 1,
			},
			args: args{
				key:            "/spdx.softwarecomposition.kubescape.io/sbomsyfts/kubescape/toto",
				ignoreNotFound: true,
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					return toto, nil, nil
				},
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomsyfts": {
					{
						Type:   watch.Modified,
						Object: toto,
					},
				},
			},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			pool := NewTestPool(t.TempDir())
			require.NotNil(t, pool)
			defer func(pool *sqlitemigration.Pool) {
				_ = pool.Close()
			}(pool)
			sch := scheme.Scheme
			require.NoError(t, softwarecomposition.AddToScheme(sch))
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot, pool, nil, sch)
			opts := storage.ListOptions{}

			watchers := map[string][]watch.Interface{}
			for key, watchCount := range tc.inputWatchesByKey {
				for i := 0; i < watchCount; i++ {
					wtch, _ := s.Watch(context.TODO(), key, opts)
					watchers[key] = append(watchers[key], wtch)
				}
			}

			destination := &v1beta1.SBOMSyft{}
			ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
			defer cancel()
			_ = s.GuaranteedUpdate(ctx, tc.args.key, destination, tc.args.ignoreNotFound, tc.args.preconditions, tc.args.tryUpdate, tc.args.cachedExistingObject)

			for key, expectedEvents := range tc.expectedEvents {
				var gotEvents []watch.Event
				for _, w := range watchers[key] {
					select {
					case ev := <-w.ResultChan():
						gotEvents = append(gotEvents, ev)
					case <-time.After(chanWaitTimeout):
						// Timed out, no event received
						continue
					}
				}
				assert.Equal(t, expectedEvents, gotEvents)
			}
		})
	}
}
