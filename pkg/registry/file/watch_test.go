package file

import (
	"context"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/storage"
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
			"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/titi",
			[]string{
				"/",
				"/spdx.softwarecomposition.kubescape.io",
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds",
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape",
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/titi",
			},
		},
		{
			"Missing leading slash should produce an error",
			"spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/titi",
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
				key: "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)

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
		keyN = "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape"
		keyK = "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape"
		obj  = &v1beta1.SBOMSPDXv2p3{ObjectMeta: v1.ObjectMeta{
			Name:            "some-sbom",
			ResourceVersion: "1",
		}}
	)
	tt := []struct {
		name                         string
		start, stopBefore, stopAfter map[string]int
		inputObjects                 map[string]*v1beta1.SBOMSPDXv2p3
		want                         map[string][]watch.Event
	}{{
		name:  "Create should publish to the appropriate single channel",
		start: map[string]int{keyK: 1},
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
			keyK + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		want: map[string][]watch.Event{keyK: {{Type: watch.Added, Object: obj}}},
	}, {
		name:  "Create should publish to all watchers on the relevant key",
		start: map[string]int{keyK: 3},
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
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
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		want: map[string][]watch.Event{keyN: {{Type: watch.Added, Object: obj}}, keyK: {}},
	}, {
		name:  "Creating on key not being watched should produce no events",
		start: map[string]int{keyK: 1},
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
			keyN + "/some-sbom": {ObjectMeta: v1.ObjectMeta{Name: "some-sbom"}},
		},
		want: map[string][]watch.Event{keyN: {}},
	}, {
		name:  "Sending to stopped watch should not produce an event",
		start: map[string]int{keyN: 3},
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
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
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
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
		inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
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
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			ctx := context.Background()
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
				out := &v1beta1.SBOMSPDXv2p3{}
				for key, object := range tc.inputObjects {
					_ = s.Create(ctx, key, object, out, ttl)
				}
				time.Sleep(chanWaitTimeout) // Create notifications happen asynchronously
			}
			go stopWatchers(tc.stopAfter)
			wait()

			// Assert the expected events
			for key, wantEvents := range tc.want {
				gotEvents := []watch.Event{}
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
	toto := &v1beta1.SBOMSPDXv2p3{
		ObjectMeta: v1.ObjectMeta{
			Name:            "toto",
			ResourceVersion: "1",
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
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": 1,
			},
			args: args{
				key:            "/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/toto",
				ignoreNotFound: true,
				tryUpdate: func(input runtime.Object, res storage.ResponseMeta) (runtime.Object, *uint64, error) {
					return toto, nil, nil
				},
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": {
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
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			opts := storage.ListOptions{}

			watchers := map[string][]watch.Interface{}
			for key, watchCount := range tc.inputWatchesByKey {
				for i := 0; i < watchCount; i++ {
					watch, _ := s.Watch(context.TODO(), key, opts)
					watchers[key] = append(watchers[key], watch)
				}
			}

			destination := &v1beta1.SBOMSPDXv2p3{}
			_ = s.GuaranteedUpdate(context.TODO(), tc.args.key, destination, tc.args.ignoreNotFound, tc.args.preconditions, tc.args.tryUpdate, tc.args.cachedExistingObject)

			for key, expectedEvents := range tc.expectedEvents {
				gotEvents := []watch.Event{}
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
