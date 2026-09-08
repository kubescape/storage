package file

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"zombiezen.com/go/sqlite"
)

const reportedWorkloadName = "serviceaccount-kube-apiserver-serving-clustertrustbundle-publisher-clusterrole-system-controller-kube-apiserver-serving-clustertrustbundle-publisher-clusterrolebinding-system-controller-kube-apiserver-serving-clustertrustbundle-publisher"

func TestMakeTempPayloadPath(t *testing.T) {
	const writerSuffix = ".t.1788802270245700726.18446744073709551615"
	for _, suffix := range []string{".t", writerSuffix} {
		for _, tc := range []struct {
			name string
			base string
		}{
			{"short", "deployment-nginx.g"},
			{"boundary", strings.Repeat("a", 255-len(suffix)-len(GobExt)) + GobExt},
			{"overflow", strings.Repeat("a", 256-len(suffix)-len(GobExt)) + GobExt},
			{"multibyte", strings.Repeat("é", 126) + GobExt},
		} {
			t.Run(tc.name+suffix, func(t *testing.T) {
				finalPath := filepath.Join(t.TempDir(), tc.base)
				path := makeTempPayloadPath(finalPath, suffix)
				require.LessOrEqual(t, len(filepath.Base(path)), 255)
				require.Equal(t, filepath.Dir(finalPath), filepath.Dir(path))
				require.True(t, strings.HasSuffix(path, suffix))
				require.False(t, IsPayloadFile(path))
				require.Equal(t, path, makeTempPayloadPath(finalPath, suffix))
				if len(tc.base)+len(suffix) <= 255 {
					require.Equal(t, finalPath+suffix, path)
				} else {
					require.NotEqual(t, finalPath+suffix, path)
				}
			})
		}
	}
	dir := t.TempDir()
	prefix := strings.Repeat("a", 252)
	require.NotEqual(t,
		makeTempPayloadPath(filepath.Join(dir, prefix+"b.g"), writerSuffix),
		makeTempPayloadPath(filepath.Join(dir, prefix+"c.g"), writerSuffix))
}

func TestSingleWriter_LongTempPathUnique(t *testing.T) {
	w := &singleWriter{}
	finalPath := filepath.Join(t.TempDir(), strings.Repeat("a", 253)+GobExt)
	const count = 100
	paths := make(chan string, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			paths <- w.nextTempPath(finalPath)
		}()
	}
	wg.Wait()
	close(paths)
	seen := make(map[string]bool)
	for path := range paths {
		require.LessOrEqual(t, len(filepath.Base(path)), 255)
		require.Equal(t, filepath.Dir(finalPath), filepath.Dir(path))
		require.False(t, IsPayloadFile(path))
		require.False(t, seen[path], "duplicate temporary path: %s", path)
		seen[path] = true
	}
	w.tmpSeq = ^uint64(0) - 1
	path := w.nextTempPath(finalPath)
	require.LessOrEqual(t, len(filepath.Base(path)), 255)
	require.True(t, strings.HasSuffix(path, ".18446744073709551615"))
}

// Use the real filesystem: MemMapFs does not enforce the filename limit.
func TestStorageLongNames(t *testing.T) {
	for _, singleWriterMode := range []bool{false, true} {
		for _, name := range []string{reportedWorkloadName, strings.Repeat("a", 253)} {
			t.Run(fmt.Sprintf("singleWriter=%t/nameBytes=%d", singleWriterMode, len(name)), func(t *testing.T) {
				old := singleWriterEnabled
				singleWriterEnabled = singleWriterMode
				t.Cleanup(func() { singleWriterEnabled = old })
				root := t.TempDir()
				pool := NewTestPool(t.TempDir())
				require.NotNil(t, pool)
				defer pool.Close()
				sch := runtime.NewScheme()
				install.Install(sch)
				si := NewStorageImpl(afero.NewOsFs(), root, pool, nil, sch).(*StorageImpl)
				ctx := context.Background()
				prefix := "/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape"
				key := prefix + "/" + name
				obj := &softwarecomposition.WorkloadConfigurationScanSummary{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kubescape", Labels: map[string]string{"state": "original"}},
				}
				obj.Spec.Severities.High = 1
				require.NoError(t, si.Create(ctx, key, obj, &softwarecomposition.WorkloadConfigurationScanSummary{}, 0))

				assertStored := func(state string, high int64) {
					t.Helper()
					got := &softwarecomposition.WorkloadConfigurationScanSummary{}
					require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got))
					require.Equal(t, name, got.Name)
					require.Equal(t, state, got.Labels["state"])
					require.Equal(t, high, got.Spec.Severities.High)
					for _, rv := range []string{"", softwarecomposition.ResourceVersionFullSpec} {
						list := &softwarecomposition.WorkloadConfigurationScanSummaryList{}
						require.NoError(t, si.GetList(ctx, prefix, storage.ListOptions{
							Recursive: true, Predicate: storage.Everything, ResourceVersion: rv,
						}, list))
						require.Len(t, list.Items, 1)
						require.Equal(t, name, list.Items[0].Name)
						require.Equal(t, state, list.Items[0].Labels["state"])
						if rv == softwarecomposition.ResourceVersionFullSpec {
							require.Equal(t, high, list.Items[0].Spec.Severities.High)
						}
					}
					entries, err := os.ReadDir(filepath.Join(root, prefix))
					require.NoError(t, err)
					require.Len(t, entries, 1, "only the permanent payload may remain")
					require.Equal(t, name+GobExt, entries[0].Name())
				}
				assertStored("original", 1)
				update := func() error {
					return si.GuaranteedUpdate(ctx, key, &softwarecomposition.WorkloadConfigurationScanSummary{}, false, nil,
						func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
							cur := input.(*softwarecomposition.WorkloadConfigurationScanSummary).DeepCopy()
							cur.Labels["state"] = "updated"
							cur.Spec.Severities.High = 2
							return cur, nil, nil
						}, nil)
				}
				injected := errors.New("injected persistence failure")
				si.writeMetadataFn = func(*sqlite.Conn, string, runtime.Object) error { return injected }
				require.ErrorIs(t, update(), injected)
				si.writeMetadataFn = writeMetadata
				assertStored("original", 1)
				si.renamePayloadFn = func(string, string) error { return injected }
				require.ErrorIs(t, update(), injected)
				si.renamePayloadFn = nil
				assertStored("original", 1)
				require.NoError(t, update())
				assertStored("updated", 2)

				// A fresh storage instance must find the unchanged permanent path.
				si = NewStorageImpl(afero.NewOsFs(), root, pool, nil, sch).(*StorageImpl)
				assertStored("updated", 2)
				require.NoError(t, si.Delete(ctx, key, &softwarecomposition.WorkloadConfigurationScanSummary{}, nil, nil, nil, storage.DeleteOptions{}))
				require.True(t, storage.IsNotFound(si.Get(ctx, key, storage.GetOptions{}, &softwarecomposition.WorkloadConfigurationScanSummary{})))
				list := &softwarecomposition.WorkloadConfigurationScanSummaryList{}
				require.NoError(t, si.GetList(ctx, prefix, storage.ListOptions{Recursive: true, Predicate: storage.Everything}, list))
				require.Empty(t, list.Items)
				entries, err := os.ReadDir(filepath.Join(root, prefix))
				require.NoError(t, err)
				require.Empty(t, entries)
			})
		}
	}
}
