package file

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/storage"
	"k8s.io/client-go/kubernetes/scheme"
	"zombiezen.com/go/sqlite"
)

// TestSaveObject_MetadataFailureLeavesPayloadUntouched pins a torn-write bug: a write
// whose metadata step fails must not have replaced the payload — GET keeps
// serving the previous object and no divergence forms.
func TestSaveObject_MetadataFailureLeavesPayloadUntouched(t *testing.T) {
	pool := NewTestPool(t.TempDir())
	require.NotNil(t, pool)
	defer func() { _ = pool.Close() }()
	sch := scheme.Scheme
	install.Install(sch)
	si := NewStorageImpl(afero.NewMemMapFs(), "/", pool, nil, sch).(*StorageImpl)

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()
	key := "/spdx.softwarecomposition.kubescape.io/containerprofile/ns1/atomic-a"

	mk := func(state string) *softwarecomposition.ContainerProfile {
		return &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "atomic-a", Namespace: "ns1",
				Labels: map[string]string{"test.kubescape.io/state": state}},
		}
	}
	require.NoError(t, si.Create(ctx, key, mk("rogue"), &softwarecomposition.ContainerProfile{}, 0))

	// Sabotage the metadata step exactly where a lock/interrupt bites in
	// production — after the payload stage.
	si.writeMetadataFn = func(*sqlite.Conn, string, runtime.Object) error {
		return errors.New("sqlite: step: interrupted (injected)")
	}

	out := &softwarecomposition.ContainerProfile{}
	uerr := si.GuaranteedUpdate(ctx, key, out, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			cur.Labels["test.kubescape.io/state"] = "healed"
			return cur, nil, nil
		}, nil)
	require.Error(t, uerr, "update with a failing metadata step must fail")

	// Repair the seam and verify the payload was untouched: GET still serves
	// the pre-update object, and a normal update now succeeds and is visible.
	si.writeMetadataFn = writeMetadata
	got := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got))
	require.Equal(t, "rogue", got.Labels["test.kubescape.io/state"],
		"a failed write must not leak a new payload (a torn-write bug torn write)")

	require.NoError(t, si.GuaranteedUpdate(ctx, key, out, false, nil,
		func(input runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
			cur := input.(*softwarecomposition.ContainerProfile).DeepCopy()
			cur.Labels["test.kubescape.io/state"] = "healed"
			return cur, nil, nil
		}, nil))
	got2 := &softwarecomposition.ContainerProfile{}
	require.NoError(t, si.Get(ctx, key, storage.GetOptions{}, got2))
	require.Equal(t, "healed", got2.Labels["test.kubescape.io/state"], "recovered write must land normally")
}
