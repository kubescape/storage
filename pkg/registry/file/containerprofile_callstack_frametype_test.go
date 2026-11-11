package file

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apiserver/pkg/storage"
)

// The frame type of a call stack frame is part of the stored profile: saving a
// profile, which unifies its call stacks, must not reset it.
func TestCreate_KeepsCallStackFrameType(t *testing.T) {
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
			const key = testCPPrefix + "default/frame-type"
			frame := softwarecomposition.StackFrame{FileID: "10425069705252389217", Lineno: "645761", FrameType: 3}
			profile := &softwarecomposition.ContainerProfile{
				ObjectMeta: metav1.ObjectMeta{Name: "frame-type", Namespace: "default", UID: types.UID("frame-type-uid")},
				Spec: softwarecomposition.ContainerProfileSpec{IdentifiedCallStacks: []softwarecomposition.IdentifiedCallStack{{
					CallID:    "call",
					CallStack: softwarecomposition.CallStack{Root: softwarecomposition.CallStackNode{Children: []softwarecomposition.CallStackNode{{Frame: frame}}}},
				}}},
			}
			require.NoError(t, b.store.Create(t.Context(), key, profile, nil, 0))
			stored := &softwarecomposition.ContainerProfile{}
			require.NoError(t, b.store.Get(t.Context(), key, storage.GetOptions{}, stored))
			require.Len(t, stored.Spec.IdentifiedCallStacks, 1)
			require.Len(t, stored.Spec.IdentifiedCallStacks[0].CallStack.Root.Children, 1)
			assert.Equal(t, frame, stored.Spec.IdentifiedCallStacks[0].CallStack.Root.Children[0].Frame)
		})
	}
}
