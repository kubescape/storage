package file

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHostResourceCleanupDecision(t *testing.T) {
	const nodeName = "worker.with.dots.and-a-name-longer-than-thirty-characters"
	const machineID = "0123456789abcdef0123456789abcdef"
	nodes := []corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: nodeName}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{MachineID: machineID}}}}
	snapshot := newHostResources(nodes)
	sbom := func(id string) *metav1.ObjectMeta {
		sum := sha256.Sum256([]byte(id))
		return &metav1.ObjectMeta{Labels: map[string]string{"kubescape.io/host": fmt.Sprintf("truncated-sanitized-name-%x", sum[:16])}}
	}
	profile := func(id string) *metav1.ObjectMeta {
		return &metav1.ObjectMeta{Labels: map[string]string{"kubescape.io/workload-kind": "Node", "kubescape.io/workload-namespace": "host"}, Annotations: map[string]string{"kubescape.io/wlid": "wlid://cluster-unknown/namespace-host/host-" + id}}
	}
	for _, tc := range []struct {
		name, kind      string
		meta            *metav1.ObjectMeta
		hosts           *hostResources
		handled, remove bool
	}{
		{"hashed node name", "sbomsyft", sbom(nodeName), snapshot, true, false},
		{"hashed machine id", "sbomsyft", sbom(machineID), snapshot, true, false},
		{"orphan sbom", "sbomsyft", sbom("deleted"), snapshot, true, true},
		{"node profile", ContainerProfileKind, profile(nodeName), snapshot, true, false},
		{"machine id profile", ContainerProfileKind, profile(machineID), snapshot, true, false},
		{"orphan profile", ContainerProfileKind, profile("deleted"), snapshot, true, true},
		{"empty cluster", "sbomsyft", sbom(nodeName), newHostResources(nil), true, true},
		{"discovery unavailable", "sbomsyft", sbom(nodeName), nil, true, false},
		{"profile discovery unavailable", ContainerProfileKind, profile(nodeName), nil, true, false},
		{"incomplete identity", "sbomsyft", sbom(machineID), newHostResources([]corev1.Node{{ObjectMeta: metav1.ObjectMeta{Name: nodeName}}}), true, false},
		{"missing owner", ContainerProfileKind, profile(""), snapshot, true, false},
		{"malformed owner", ContainerProfileKind, profile("foo/bar"), snapshot, true, false},
		{"malformed host label", "sbomsyft", &metav1.ObjectMeta{Labels: map[string]string{"kubescape.io/host": "unknown"}}, snapshot, true, false},
		{"legacy host", "sbomsyft", &metav1.ObjectMeta{Labels: map[string]string{"kubescape.io/sbom-type": "host"}}, snapshot, true, false},
		{"ordinary image", "sbomsyft", &metav1.ObjectMeta{}, snapshot, false, false},
		{"ordinary profile", ContainerProfileKind, &metav1.ObjectMeta{Labels: map[string]string{"kubescape.io/workload-kind": "Pod"}}, snapshot, false, false},
		{"other kind", "applicationprofiles", profile(nodeName), snapshot, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handled, remove := hostResourceCleanupDecision(tc.kind, tc.meta, tc.hosts)
			require.Equal(t, tc.handled, handled)
			require.Equal(t, tc.remove, remove)
		})
	}
}
