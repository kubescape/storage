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
	chanWaitTimeout = 500 * time.Millisecond
)

func TestExtractKeysToNotify(t *testing.T) {
	tt := []struct {
		name          string
		inputKey      string
		expectedKeys  []string
		expectedError error
	}{
		{
			"root key should produce only itself",
			"/",
			[]string{"/"},
			nil,
		},
		{
			"API resource key should produce root and itself",
			"/spdx.softwarecomposition.kubescape.io",
			[]string{"/", "/spdx.softwarecomposition.kubescape.io"},
			nil,
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
			nil,
		},
		{
			"Missing leading slash should produce an error",
			"spdx.softwarecomposition.kubescape.io/sbomspdxv2p3filtereds/kubescape/titi",
			[]string{},
			errInvalidKey,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			got, err := exractKeysToNotify(tc.inputKey)

			assert.Equal(t, tc.expectedKeys, got)
			assert.ErrorIs(t, err, tc.expectedError)
		})
	}

}

func TestFileSystemStorageWatchReturnsDistinctWatchers(t *testing.T) {
	type args struct {
		ctx  context.Context
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

			got1, _ := s.Watch(tt.args.ctx, tt.args.key, tt.args.opts)
			got1chan := got1.ResultChan()

			got2, _ := s.Watch(tt.args.ctx, tt.args.key, tt.args.opts)
			got2chan := got2.ResultChan()

			assert.NotEqual(t, got1, got2, "Should not return the same watcher object")
			assert.NotEqual(t, got1chan, got2chan, "Channels from the watches should not be the same")
		})
	}
}

func TestFilesystemStoragePublishesToMatchingWatch(t *testing.T) {
	tt := []struct {
		name              string
		inputWatchesByKey map[string]int
		inputObjects      map[string]*v1beta1.SBOMSPDXv2p3
		expectedEvents    map[string][]watch.Event
	}{
		{
			name: "Create should publish to the appropriate single channel",
			inputWatchesByKey: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": 1,
			},
			inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/some-sbom": {
					ObjectMeta: v1.ObjectMeta{
						Name:            "some-sbom",
						ResourceVersion: "1",
					},
				},
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": {
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
				},
			},
		},
		{
			name: "Create should publish to all watchers on the relevant key",
			inputWatchesByKey: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": 3,
			},
			inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape/some-sbom": {
					ObjectMeta: v1.ObjectMeta{
						Name:            "some-sbom",
						ResourceVersion: "1",
					},
				},
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": {
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
				},
			},
		},
		{
			name: "Creating on key different than the watch should produce no event",
			inputWatchesByKey: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape":     3,
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape": 1,
			},
			inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape/some-sbom": {
					ObjectMeta: v1.ObjectMeta{
						Name:            "some-sbom",
						ResourceVersion: "1",
					},
				},
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape": {
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
				},
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": {},
			},
		},
		{
			name: "Creating on key not being watched should produce no events",
			inputWatchesByKey: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/kubescape": 1,
			},
			inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape/some-sbom": {
					ObjectMeta: v1.ObjectMeta{
						Name:            "some-sbom",
						ResourceVersion: "1",
					},
				},
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape": {},
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			ctx := context.Background()
			opts := storage.ListOptions{}

			watchSlicesByKey := map[string][]watch.Interface{}
			for key, watchCount := range tc.inputWatchesByKey {
				for i := 0; i < watchCount; i++ {
					watch, _ := s.Watch(ctx, key, opts)
					currentWatchSlice := watchSlicesByKey[key]
					currentWatchSlice = append(currentWatchSlice, watch)
					watchSlicesByKey[key] = currentWatchSlice
				}
			}

			var ttl uint64 = 0
			var out runtime.Object
			for key, object := range tc.inputObjects {
				s.Create(ctx, key, object, out, ttl)
			}

			for key, expectedEvents := range tc.expectedEvents {
				watches := watchSlicesByKey[key]

				gotEvents := []watch.Event{}
				for idx := range watches {
					select {
					case gotEvent := <-watches[idx].ResultChan():
						gotEvents = append(gotEvents, gotEvent)
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

func TestFilesystemStorageWatchStop(t *testing.T) {
	tt := []struct {
		name                    string
		inputWatchesByKey       map[string]int
		inputStopWatchesAtIndex map[string]int
		inputObjects            map[string]*v1beta1.SBOMSPDXv2p3
		expectedEvents          map[string][]watch.Event
	}{
		{
			name: "Sending to stopped watch should not produce an event",
			inputWatchesByKey: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape": 3,
			},
			inputObjects: map[string]*v1beta1.SBOMSPDXv2p3{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape/some-sbom": {
					ObjectMeta: v1.ObjectMeta{
						Name:            "some-sbom",
						ResourceVersion: "1",
					},
				},
			},
			inputStopWatchesAtIndex: map[string]int{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape": 1,
			},
			expectedEvents: map[string][]watch.Event{
				"/spdx.softwarecomposition.kubescape.io/sbomspdxv2p3s/not-kubescape": {
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
					{
						Type: watch.Added,
						Object: &v1beta1.SBOMSPDXv2p3{
							ObjectMeta: v1.ObjectMeta{
								Name:            "some-sbom",
								ResourceVersion: "1",
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			s := NewStorageImpl(afero.NewMemMapFs(), DefaultStorageRoot)
			ctx := context.Background()
			opts := storage.ListOptions{}

			// Arrange watches
			watchSlicesByKey := map[string][]watch.Interface{}
			for key, watchCount := range tc.inputWatchesByKey {
				for i := 0; i < watchCount; i++ {
					watch, _ := s.Watch(ctx, key, opts)
					currentWatchSlice := watchSlicesByKey[key]
					currentWatchSlice = append(currentWatchSlice, watch)
					watchSlicesByKey[key] = currentWatchSlice
				}
			}

			// Arrange stopping of some watches
			for key, watchIdx := range tc.inputStopWatchesAtIndex {
				watchSlice := watchSlicesByKey[key]
				watchSlice[watchIdx].Stop()
			}

			// Act out the creation operation
			var ttl uint64 = 0
			var out runtime.Object
			for key, object := range tc.inputObjects {
				s.Create(ctx, key, object, out, ttl)
			}

			// Assert the expected events
			for key, expectedEvents := range tc.expectedEvents {
				watches := watchSlicesByKey[key]

				gotEvents := []watch.Event{}
				for idx := range watches {
					select {
					case gotEvent, ok := <-watches[idx].ResultChan():
						// Skip values from closed channels
						if !ok {
							continue
						}
						gotEvents = append(gotEvents, gotEvent)
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
