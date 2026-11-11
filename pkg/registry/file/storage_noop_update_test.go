package file

import (
	"fmt"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage"
)

func TestGuaranteedUpdate_UnchangedReapply(t *testing.T) {
	for _, mode := range []string{"legacy", "single-writer", "sqlite-native"} {
		t.Run(mode, func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = mode != "legacy"
			t.Cleanup(func() { singleWriterEnabled = old })
			var b *backend
			if mode == "sqlite-native" {
				b = newObjectBackend(t, nil)
			} else {
				b = newLegacyBackend(t, nil)
			}
			defer b.watcher.Stop()
			const key = testCPPrefix + "default/reapply"
			applied := &softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "reapply", Namespace: "default", UID: types.UID("reapply-uid")},
				Spec:       softwarecomposition.ContainerProfileSpec{Execs: []softwarecomposition.ExecCalls{{Path: "/bin/sh"}}},
			}
			require.NoError(t, b.store.Create(t.Context(), key, applied, nil, 0))
			stored := &softwarecomposition.ContainerProfile{}
			require.NoError(t, b.store.Get(t.Context(), key, storage.GetOptions{}, stored))
			require.Len(t, b.drainEvents(t), 1)
			for _, variant := range []string{"managed-fields", "type-meta", "empty-collections"} {
				t.Run(variant, func(t *testing.T) {
					out := &softwarecomposition.ContainerProfile{}
					require.NoError(t, b.store.GuaranteedUpdate(t.Context(), key, out, false, nil,
						func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
							cur := in.(*softwarecomposition.ContainerProfile).DeepCopy()
							switch variant {
							case "managed-fields":
								cur.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "reconciler", Operation: metav1.ManagedFieldsOperationUpdate, Time: &metav1.Time{Time: time.Now()}}}
							case "type-meta":
								cur.TypeMeta = metav1.TypeMeta{Kind: "ContainerProfile", APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1"}
							case "empty-collections":
								cur.Spec.Syscalls = []string{}
								cur.Spec.Execs[0].Args = []string{}
							}
							return cur, nil, nil
						}, nil))
					assert.Equal(t, stored.ResourceVersion, out.ResourceVersion)
					again := &softwarecomposition.ContainerProfile{}
					require.NoError(t, b.store.Get(t.Context(), key, storage.GetOptions{}, again))
					assert.Equal(t, stored, again, "no-op must leave persisted content and checksum unchanged")
					assert.Empty(t, b.drainEvents(t), "no-op must not emit a modified event")
				})
			}
			for _, change := range []string{"spec", "annotation"} {
				before := &softwarecomposition.ContainerProfile{}
				require.NoError(t, b.store.Get(t.Context(), key, storage.GetOptions{}, before))
				out := &softwarecomposition.ContainerProfile{}
				require.NoError(t, b.store.GuaranteedUpdate(t.Context(), key, out, false, nil,
					func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						cur := in.(*softwarecomposition.ContainerProfile).DeepCopy()
						if change == "spec" {
							cur.Spec.Execs = append(cur.Spec.Execs, softwarecomposition.ExecCalls{Path: "/usr/bin/id"})
						} else {
							cur.Annotations["example.com/note"] = "changed"
						}
						return cur, nil, nil
					}, nil))
				assert.NotEqual(t, before.ResourceVersion, out.ResourceVersion, change)
				assert.Len(t, b.drainEvents(t), 1, change)
			}
			wrongUID := types.UID("different-uid")
			for _, preconditions := range []*storage.Preconditions{{UID: &wrongUID}, {ResourceVersion: &stored.ResourceVersion}} {
				err := b.store.GuaranteedUpdate(t.Context(), key, &softwarecomposition.ContainerProfile{}, false, preconditions,
					func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						return in, nil, fmt.Errorf("must not reach update callback")
					}, nil)
				require.Error(t, err)
				assert.True(t, storage.IsInvalidObj(err), "%v", err)
			}
		})
	}
}

func TestSameStoredContent_PreservesInputs(t *testing.T) {
	stored := &softwarecomposition.ContainerProfile{ObjectMeta: metav1.ObjectMeta{Name: "profile", ResourceVersion: "7"}}
	incoming := stored.DeepCopy()
	incoming.TypeMeta = metav1.TypeMeta{Kind: "ContainerProfile", APIVersion: "spdx.softwarecomposition.kubescape.io/v1beta1"}
	incoming.ManagedFields = []metav1.ManagedFieldsEntry{{Manager: "reconciler"}}
	incoming.Spec.Syscalls = []string{}
	beforeStored, beforeIncoming := stored.DeepCopy(), incoming.DeepCopy()
	require.True(t, sameStoredContent(stored, incoming))
	assert.Equal(t, beforeStored, stored)
	assert.Equal(t, beforeIncoming, incoming)
	incoming.ResourceVersion = "6"
	assert.False(t, sameStoredContent(stored, incoming), "resource version must still participate in comparison")
}

// A profile with call stacks of several call IDs must survive an unchanged
// reapply without a write: the deflate that runs on save has to give the same
// call stack list for the same input.
func TestGuaranteedUpdate_UnchangedReapplyWithCallStacks(t *testing.T) {
	for _, mode := range []string{"legacy", "single-writer", "sqlite-native"} {
		t.Run(mode, func(t *testing.T) {
			old := singleWriterEnabled
			singleWriterEnabled = mode != "legacy"
			t.Cleanup(func() { singleWriterEnabled = old })
			var b *backend
			if mode == "sqlite-native" {
				b = newObjectBackend(t, nil)
			} else {
				b = newLegacyBackend(t, nil)
			}
			defer b.watcher.Stop()
			const key = testCPPrefix + "default/reapply-callstacks"
			var stacks []softwarecomposition.IdentifiedCallStack
			for _, id := range []softwarecomposition.CallID{"id-e", "id-b", "id-g", "id-a", "id-h", "id-c", "id-f", "id-d"} {
				stacks = append(stacks, softwarecomposition.IdentifiedCallStack{CallID: id, CallStack: softwarecomposition.CallStack{Root: softwarecomposition.CallStackNode{
					Children: []softwarecomposition.CallStackNode{{Frame: softwarecomposition.StackFrame{FileID: "1", Lineno: string(id)}}},
				}}})
			}
			applied := &softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "reapply-callstacks", Namespace: "default", UID: types.UID("reapply-uid")},
				Spec:       softwarecomposition.ContainerProfileSpec{IdentifiedCallStacks: stacks},
			}
			require.NoError(t, b.store.Create(t.Context(), key, applied, nil, 0))
			stored := &softwarecomposition.ContainerProfile{}
			require.NoError(t, b.store.Get(t.Context(), key, storage.GetOptions{}, stored))
			require.Len(t, b.drainEvents(t), 1)
			for i := 0; i < 10; i++ {
				out := &softwarecomposition.ContainerProfile{}
				require.NoError(t, b.store.GuaranteedUpdate(t.Context(), key, out, false, nil,
					func(in runtime.Object, _ storage.ResponseMeta) (runtime.Object, *uint64, error) {
						return in.(*softwarecomposition.ContainerProfile).DeepCopy(), nil, nil
					}, nil))
				assert.Equal(t, stored.ResourceVersion, out.ResourceVersion, "reapply %d wrote an unchanged profile", i+1)
			}
			assert.Empty(t, b.drainEvents(t), "an unchanged reapply must not emit a modified event")
		})
	}
}
