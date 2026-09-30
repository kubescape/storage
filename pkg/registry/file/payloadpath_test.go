package file

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
				require.True(t, isLegacyStagingFile(filepath.Base(path)))
				require.Equal(t, path, makeTempPayloadPath(finalPath, suffix))
				digest := sha256.Sum256([]byte(tc.base))
				require.Equal(t, fmt.Sprintf("%x%s%s", digest, GobExt, suffix), filepath.Base(path))
				require.NotEqual(t, finalPath+suffix, path)
			})
		}
	}
	dir := t.TempDir()
	longBase := strings.Repeat("a", 253) + GobExt
	digest := sha256.Sum256([]byte(longBase))
	shortBase := fmt.Sprintf("%x%s", digest, GobExt)
	for _, suffix := range []string{".t", writerSuffix} {
		require.NotEqual(t,
			makeTempPayloadPath(filepath.Join(dir, longBase), suffix),
			makeTempPayloadPath(filepath.Join(dir, shortBase), suffix),
			"a long basename and an object named after its digest must stage separately")
	}
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
		for _, gateEnabled := range []bool{false, true} {
			for _, name := range []string{reportedWorkloadName, strings.Repeat("a", 253)} {
				t.Run(fmt.Sprintf("singleWriter=%t/gate=%t/nameBytes=%d", singleWriterMode, gateEnabled, len(name)), func(t *testing.T) {
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
					ctx := t.Context()
					var gate *WriteGate
					if gateEnabled {
						armUngatedWriteCheck(t, pool)
						var err error
						gate, err = NewWriteGate(ctx, pool)
						require.NoError(t, err)
						defer func() { require.NoError(t, gate.Close()) }()
						si.SetWriteGate(gate)
						si.SetForeignKinds(IsContainerProfileKind)
					}
					prefix := "/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape"
					key := prefix + "/" + name
					obj := &softwarecomposition.WorkloadConfigurationScanSummary{
						ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "kubescape", Labels: map[string]string{"state": "original"}},
					}
					obj.Spec.Severities.High = 1
					if gateEnabled && !singleWriterMode {
						// The shared gate deliberately refuses the legacy writer mode.
						err := si.Create(ctx, key, obj, &softwarecomposition.WorkloadConfigurationScanSummary{}, 0)
						require.ErrorContains(t, err, "the shared write gate requires the single-writer path")
						entries, err := os.ReadDir(root)
						require.NoError(t, err)
						require.Empty(t, entries, "refused configuration must not stage a payload")
						return
					}
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
					si.SetWriteGate(gate)
					if gateEnabled {
						si.SetForeignKinds(IsContainerProfileKind)
					}
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
}

// stagingOverlapFs pauses only successfully opened staging handles, so a failed
// O_DIRECT attempt cannot consume a position in the coordinated write sequence.
type stagingOverlapFs struct {
	afero.Fs
	ctx         context.Context
	opens       atomic.Int32
	longStaged  chan struct{}
	shortStaged chan struct{}
	longDone    chan struct{}
}

func (fs *stagingOverlapFs) OpenFile(name string, flag int, perm os.FileMode) (afero.File, error) {
	f, err := fs.Fs.OpenFile(name, flag, perm)
	if err != nil || flag&os.O_CREATE == 0 || !isLegacyStagingFile(filepath.Base(name)) {
		return f, err
	}
	return &stagingOverlapFile{File: f, fs: fs, ordinal: fs.opens.Add(1)}, nil
}

type stagingOverlapFile struct {
	afero.File
	fs      *stagingOverlapFs
	ordinal int32
}

func (f *stagingOverlapFile) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	var release <-chan struct{}
	switch f.ordinal {
	case 1:
		close(f.fs.longStaged)
		release = f.fs.shortStaged
	case 2:
		close(f.fs.shortStaged)
		release = f.fs.longDone
	default:
		return nil
	}
	select {
	case <-release:
		return nil
	case <-f.fs.ctx.Done():
		return f.fs.ctx.Err()
	}
}

func TestStorage_LegacyStagingCollision(t *testing.T) {
	old := singleWriterEnabled
	singleWriterEnabled = false
	t.Cleanup(func() { singleWriterEnabled = old })
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait(); pool.Close() }()
	root := t.TempDir()
	fs := &stagingOverlapFs{Fs: afero.NewOsFs(), ctx: ctx,
		longStaged: make(chan struct{}), shortStaged: make(chan struct{}), longDone: make(chan struct{})}
	sch := runtime.NewScheme()
	install.Install(sch)
	si := NewStorageImpl(fs, root, pool, nil, sch).(*StorageImpl)
	longName := strings.Repeat("a", 253)
	digest := sha256.Sum256([]byte(longName + GobExt))
	shortName := fmt.Sprintf("%x", digest)
	require.Equal(t, "43fc0112db9666cee67bfe959e41939958a4ec9ae427b5b9402d83da186ae889", shortName)
	const prefix = "/spdx.softwarecomposition.kubescape.io/workloadconfigurationscansummaries/kubescape"
	names := []string{longName, shortName}
	results := []chan error{make(chan error, 1), make(chan error, 1)}
	outputs := []*softwarecomposition.WorkloadConfigurationScanSummary{{}, {}}
	start := func(i int) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if i == 0 {
				defer close(fs.longDone)
			}
			obj := &softwarecomposition.WorkloadConfigurationScanSummary{
				ObjectMeta: metav1.ObjectMeta{Name: names[i], Namespace: "kubescape", Labels: map[string]string{"writer": fmt.Sprint(i)}},
			}
			obj.Spec.Severities.High = int64(i + 1)
			results[i] <- si.Create(ctx, prefix+"/"+names[i], obj, outputs[i], 0)
		}()
	}
	start(0)
	select {
	case <-fs.longStaged:
	case <-ctx.Done():
		t.Fatal("long payload did not stage:", ctx.Err())
	}
	start(1)
	// Collect both results before asserting, so a failing first write still
	// releases and joins the second writer.
	errs := make([]error, 2)
	for i := range results {
		select {
		case errs[i] = <-results[i]:
		case <-ctx.Done():
			t.Fatal("overlapping creates did not complete:", ctx.Err())
		}
	}
	for i, err := range errs {
		require.NoError(t, err, "writer %d", i)
	}
	require.Equal(t, int32(2), fs.opens.Load())
	fresh := NewStorageImpl(afero.NewOsFs(), root, pool, nil, sch).(*StorageImpl)
	for _, reader := range []*StorageImpl{si, fresh} {
		for i, name := range names {
			got := &softwarecomposition.WorkloadConfigurationScanSummary{}
			require.NoError(t, reader.Get(ctx, prefix+"/"+name, storage.GetOptions{}, got))
			require.Equal(t, name, got.Name)
			require.Equal(t, fmt.Sprint(i), got.Labels["writer"])
			require.Equal(t, int64(i+1), got.Spec.Severities.High)
			require.Equal(t, outputs[i].ResourceVersion, got.ResourceVersion)
		}
		list := &softwarecomposition.WorkloadConfigurationScanSummaryList{}
		require.NoError(t, reader.GetList(ctx, prefix, storage.ListOptions{Recursive: true, Predicate: storage.Everything, ResourceVersion: softwarecomposition.ResourceVersionFullSpec}, list))
		require.Len(t, list.Items, 2)
		for _, item := range list.Items {
			i := 0
			if item.Name == shortName {
				i = 1
			}
			require.Equal(t, names[i], item.Name)
			require.Equal(t, int64(i+1), item.Spec.Severities.High)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, prefix))
	require.NoError(t, err)
	require.Len(t, entries, 2)
	for _, entry := range entries {
		require.Contains(t, []string{longName + GobExt, shortName + GobExt}, entry.Name())
		require.False(t, isLegacyStagingFile(entry.Name()))
	}
}
