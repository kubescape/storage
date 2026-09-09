package containerprofile

// Backend differential suite (the Phase 4 pattern one level down): the SAME
// hand-written CustomREST (genericrest.Store) over the legacy row+gob-file
// StorageImpl and over the SQLite-native ObjectStore
// (pkg/registry/file/sqliteobject_*.go), driven through identical REST
// Create / Get / Update / Delete / List / Watch sequences. Externally
// observable results must be identical except the two codec deltas the
// storage-level suite pins (file.TestDifferential_*): GET creationTimestamp is
// truncated to whole seconds on the new store, and v1beta1 spec collections
// without omitempty come back as [] instead of null.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/config"
	"github.com/kubescape/storage/pkg/registry/file"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/storage"
)

// newObjectStoreTestStorage mirrors apiserver.go's wiring under
// ContainerProfileSqliteBackend: the legacy default StorageImpl carries the
// kind-ownership guard and serves GetSbom; the ObjectStore serves the
// containerprofile kind.
func newObjectStoreTestStorage(t *testing.T) storage.Interface {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "metadata.sq3")
	pool := file.NewPoolWithOptions(dbPath, file.PoolOptions{DisableAutoCheckpoint: true, BusyTimeout: 5 * time.Second})
	sch := newTestScheme(t)
	legacy := file.NewStorageImpl(afero.NewMemMapFs(), file.DefaultStorageRoot, pool, nil, sch)
	legacy.(*file.StorageImpl).SetForeignKinds(file.IsContainerProfileKind)
	processor := file.NewContainerProfileProcessor(config.Config{DefaultNamespace: "kubescape", MaxContainerProfileSize: 40000}, nil)
	processor.Interval = 0
	gateCtx, gateCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer gateCancel()
	gate, err := file.NewWriteGate(gateCtx, pool)
	require.NoError(t, err)
	legacy.(*file.StorageImpl).SetWriteGate(gate)
	store, err := file.NewObjectStore(pool, dbPath, nil, sch, processor, legacy, gate, file.ObjectStoreOptions{CheckpointInterval: time.Hour})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
		require.NoError(t, gate.Close())
		require.NoError(t, pool.Close())
	})
	return store
}

// backendHarness: the same CustomREST over each backend.
type backendHarness struct {
	oldREST rest.StandardStorage // CustomREST over the legacy StorageImpl
	newREST rest.StandardStorage // CustomREST over the ObjectStore
}

func newBackendHarness(t *testing.T) *backendHarness {
	t.Helper()
	sch := newTestScheme(t)
	optsGetter := newOptsGetter()
	oldStorage, _ := newTestStorage(t)
	oldREST, err := NewCustomREST(sch, oldStorage, optsGetter)
	require.NoError(t, err)
	newREST, err := NewCustomREST(sch, newObjectStoreTestStorage(t), optsGetter)
	require.NoError(t, err)
	return &backendHarness{oldREST: oldREST, newREST: newREST}
}

// canonical strips instance-specific fields (UID, creationTimestamp, checksum)
// and the nil-vs-empty codec delta so the two backends' objects can be
// compared field by field.
func canonical(t *testing.T, obj runtime.Object) *softwarecomposition.ContainerProfile {
	t.Helper()
	out := normalize(t, obj)
	nilifyEmptyCollections(out)
	return out
}

func nilifyEmptyCollections(cp *softwarecomposition.ContainerProfile) {
	if len(cp.Spec.Architectures) == 0 {
		cp.Spec.Architectures = nil
	}
	if len(cp.Spec.Capabilities) == 0 {
		cp.Spec.Capabilities = nil
	}
	if len(cp.Spec.Execs) == 0 {
		cp.Spec.Execs = nil
	}
	if len(cp.Spec.Opens) == 0 {
		cp.Spec.Opens = nil
	}
	if len(cp.Spec.Syscalls) == 0 {
		cp.Spec.Syscalls = nil
	}
	if len(cp.Spec.Endpoints) == 0 {
		cp.Spec.Endpoints = nil
	}
	if len(cp.Spec.PolicyByRuleId) == 0 {
		cp.Spec.PolicyByRuleId = nil
	}
	if len(cp.Spec.IdentifiedCallStacks) == 0 {
		cp.Spec.IdentifiedCallStacks = nil
	}
	if len(cp.Spec.Ingress) == 0 {
		cp.Spec.Ingress = nil
	}
	if len(cp.Spec.Egress) == 0 {
		cp.Spec.Egress = nil
	}
	if len(cp.Spec.LabelSelector.MatchLabels) == 0 {
		cp.Spec.LabelSelector.MatchLabels = nil
	}
	if len(cp.Spec.LabelSelector.MatchExpressions) == 0 {
		cp.Spec.LabelSelector.MatchExpressions = nil
	}
}

func rvOf(t *testing.T, obj runtime.Object) string {
	t.Helper()
	m, err := meta.Accessor(obj)
	require.NoError(t, err)
	return m.GetResourceVersion()
}

func profileWithSpec(name string) *softwarecomposition.ContainerProfile {
	return &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: map[string]string{helpersv1.StatusMetadataKey: helpersv1.Learning}},
		Spec: softwarecomposition.ContainerProfileSpec{
			Architectures: []string{"amd64"},
			// Empty and non-nil on purpose: PreSave keeps it that way, gob drops
			// it (GET → nil) and JSON keeps it (GET → []): the codec delta.
			Capabilities: []string{},
			Execs:        []softwarecomposition.ExecCalls{{Path: "/bin/sh", Args: []string{"/bin/sh"}}},
			ImageTag:     "img:1",
		},
	}
}

func TestBackendDifferential_CreateGetUpdateDelete(t *testing.T) {
	h := newBackendHarness(t)
	ctx := testContext("ns1")
	cp := profileWithSpec("cp-a")

	oldOut := mustCreate(t, ctx, h.oldREST, cp)
	newOut := mustCreate(t, ctx, h.newREST, cp)
	assert.Equal(t, canonical(t, oldOut), canonical(t, newOut))
	assert.Equal(t, rvOf(t, oldOut), rvOf(t, newOut))

	_, err := h.oldREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "old: %v", err)
	_, err = h.newREST.Create(ctx, cp.DeepCopy(), rest.ValidateAllObjectFunc, &metav1.CreateOptions{})
	assert.True(t, apierrors.IsAlreadyExists(err), "new: %v", err)

	oldGot, err := h.oldREST.Get(ctx, "cp-a", &metav1.GetOptions{})
	require.NoError(t, err)
	newGot, err := h.newREST.Get(ctx, "cp-a", &metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, canonical(t, oldGot), canonical(t, newGot))
	// The codec delta, pinned rather than hidden: the store never persisted
	// empty Capabilities, yet GET returns nil on the old store and [] on new.
	assert.Nil(t, oldGot.(*softwarecomposition.ContainerProfile).Spec.Capabilities)
	assert.NotNil(t, newGot.(*softwarecomposition.ContainerProfile).Spec.Capabilities)
	assert.Equal(t, 0, newGot.(*softwarecomposition.ContainerProfile).CreationTimestamp.Nanosecond())

	update := func(r rest.StandardStorage, mutate func(*softwarecomposition.ContainerProfile)) (runtime.Object, bool, error) {
		return r.Update(ctx, "cp-a", rest.DefaultUpdatedObjectInfo(nil, func(_ context.Context, newObj, oldObj runtime.Object) (runtime.Object, error) {
			out := oldObj.(*softwarecomposition.ContainerProfile).DeepCopy()
			mutate(out)
			return out, nil
		}), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
	}
	addLabel := func(c *softwarecomposition.ContainerProfile) {
		if c.Labels == nil {
			c.Labels = map[string]string{}
		}
		c.Labels["k"] = "v"
	}
	oldUpd, oldCreated, err := update(h.oldREST, addLabel)
	require.NoError(t, err)
	newUpd, newCreated, err := update(h.newREST, addLabel)
	require.NoError(t, err)
	assert.Equal(t, oldCreated, newCreated)
	assert.Equal(t, canonical(t, oldUpd), canonical(t, newUpd))
	assert.Equal(t, rvOf(t, oldUpd), rvOf(t, newUpd))
	assert.Equal(t, "2", rvOf(t, newUpd))

	// no-op update: same RV on both (#315)
	oldNoop, _, err := update(h.oldREST, func(*softwarecomposition.ContainerProfile) {})
	require.NoError(t, err)
	newNoop, _, err := update(h.newREST, func(*softwarecomposition.ContainerProfile) {})
	require.NoError(t, err)
	assert.Equal(t, rvOf(t, oldNoop), rvOf(t, newNoop))
	assert.Equal(t, "2", rvOf(t, newNoop))

	// stale resourceVersion → Conflict on both
	staleUpdate := func(r rest.StandardStorage) error {
		_, _, err := r.Update(ctx, "cp-a", rest.DefaultUpdatedObjectInfo(nil, func(_ context.Context, newObj, oldObj runtime.Object) (runtime.Object, error) {
			out := oldObj.(*softwarecomposition.ContainerProfile).DeepCopy()
			out.ResourceVersion = "1"
			out.Labels["k"] = "stale"
			return out, nil
		}), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
		return err
	}
	assert.True(t, apierrors.IsConflict(staleUpdate(h.oldREST)))
	assert.True(t, apierrors.IsConflict(staleUpdate(h.newREST)))

	oldDel, oldImmediate, err := h.oldREST.Delete(ctx, "cp-a", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	newDel, newImmediate, err := h.newREST.Delete(ctx, "cp-a", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	require.NoError(t, err)
	assert.Equal(t, oldImmediate, newImmediate)
	assertEqualDeleteStatus(t, oldDel, newDel, "cp-a")

	_, err = h.oldREST.Get(ctx, "cp-a", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, err = h.newREST.Get(ctx, "cp-a", &metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, _, err = h.oldREST.Delete(ctx, "cp-a", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	assert.True(t, apierrors.IsNotFound(err))
	_, _, err = h.newREST.Delete(ctx, "cp-a", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestBackendDifferential_ListAndContinue(t *testing.T) {
	h := newBackendHarness(t)
	ctx := testContext("ns1")
	for _, n := range []string{"l-a", "l-b", "l-c"} {
		mustCreate(t, ctx, h.oldREST, profileWithSpec(n))
		mustCreate(t, ctx, h.newREST, profileWithSpec(n))
	}
	names := func(obj runtime.Object) []string {
		l := obj.(*softwarecomposition.ContainerProfileList)
		var out []string
		for _, it := range l.Items {
			out = append(out, it.Name)
		}
		return out
	}
	for _, r := range []rest.StandardStorage{h.oldREST, h.newREST} {
		p1, err := r.List(ctx, &metainternalversion.ListOptions{Limit: 2})
		require.NoError(t, err)
		assert.Equal(t, []string{"l-a", "l-b"}, names(p1))
		cont := p1.(*softwarecomposition.ContainerProfileList).Continue
		require.NotEmpty(t, cont)
		p2, err := r.List(ctx, &metainternalversion.ListOptions{Limit: 2, Continue: cont})
		require.NoError(t, err)
		assert.Equal(t, []string{"l-c"}, names(p2))
		full, err := r.List(ctx, &metainternalversion.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec})
		require.NoError(t, err)
		require.Len(t, full.(*softwarecomposition.ContainerProfileList).Items, 3)
		assert.Equal(t, "img:1", full.(*softwarecomposition.ContainerProfileList).Items[0].Spec.ImageTag, "fullSpec list carries the spec")
	}
	oldL, _ := h.oldREST.List(ctx, &metainternalversion.ListOptions{})
	newL, _ := h.newREST.List(ctx, &metainternalversion.ListOptions{})
	require.Len(t, oldL.(*softwarecomposition.ContainerProfileList).Items, 3)
	for i := range oldL.(*softwarecomposition.ContainerProfileList).Items {
		o := &oldL.(*softwarecomposition.ContainerProfileList).Items[i]
		n := &newL.(*softwarecomposition.ContainerProfileList).Items[i]
		assert.Equal(t, canonical(t, o), canonical(t, n))
	}
}

func TestBackendDifferential_Watch(t *testing.T) {
	h := newBackendHarness(t)
	ctx := testContext("ns1")
	events := func(r rest.StandardStorage) []string {
		w, err := r.Watch(ctx, &metainternalversion.ListOptions{ResourceVersion: softwarecomposition.ResourceVersionFullSpec})
		require.NoError(t, err)
		defer w.Stop()
		mustCreate(t, ctx, r, profileWithSpec("w-a"))
		_, _, err = r.Update(ctx, "w-a", rest.DefaultUpdatedObjectInfo(nil, func(_ context.Context, _, oldObj runtime.Object) (runtime.Object, error) {
			out := oldObj.(*softwarecomposition.ContainerProfile).DeepCopy()
			out.Spec.ImageTag = "img:2"
			return out, nil
		}), rest.ValidateAllObjectFunc, rest.ValidateAllObjectUpdateFunc, false, &metav1.UpdateOptions{})
		require.NoError(t, err)
		_, _, err = r.Delete(ctx, "w-a", rest.ValidateAllObjectFunc, &metav1.DeleteOptions{})
		require.NoError(t, err)
		var out []string
		for len(out) < 3 {
			select {
			case ev := <-w.ResultChan():
				cp := ev.Object.(*softwarecomposition.ContainerProfile)
				out = append(out, string(ev.Type)+" "+cp.Name+" rv="+cp.ResourceVersion+" img="+cp.Spec.ImageTag)
			case <-time.After(2 * time.Second):
				t.Fatalf("watch events missing after %v", out)
			}
		}
		return out
	}
	oldEv := events(h.oldREST)
	newEv := events(h.newREST)
	assert.Equal(t, oldEv, newEv)
	assert.Equal(t, string(watch.Added)+" w-a rv=1 img=img:1", newEv[0])
	assert.Equal(t, string(watch.Modified)+" w-a rv=2 img=img:2", newEv[1])
	assert.Equal(t, string(watch.Deleted)+" w-a rv=2 img=", newEv[2], "Deleted carries the metadata-only object on both")
}
