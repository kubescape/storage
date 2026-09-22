package file

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// A nil snapshot means discovery failed, not that the cluster has no nodes.
type hostResources struct {
	ids    map[string]struct{}
	hashes map[string]struct{}
	// Missing machine IDs make fallback ownership impossible to rule out.
	incomplete bool
}

func newHostResources(nodes []corev1.Node) *hostResources {
	h := &hostResources{ids: make(map[string]struct{}), hashes: make(map[string]struct{})}
	for _, node := range nodes {
		if node.Status.NodeInfo.MachineID == "" {
			h.incomplete = true
		}
		// Node-agent falls back to /etc/machine-id when NODE_NAME is absent.
		for _, id := range []string{node.Name, node.Status.NodeInfo.MachineID} {
			if id == "" {
				continue
			}
			h.ids[id] = struct{}{}
			sum := sha256.Sum256([]byte(id))
			h.hashes[hex.EncodeToString(sum[:16])] = struct{}{}
		}
	}
	return h
}

// hostResourceCleanupDecision runs before pod/image cleanup rules. Unknown
// ownership is retained; only a successful Node snapshot can prove an orphan.
func hostResourceCleanupDecision(kind string, metadata *metav1.ObjectMeta, hosts *hostResources) (handled, remove bool) {
	if metadata == nil {
		return false, false
	}
	if kind == "sbomsyft" {
		// Preserve the existing exemption for legacy artifacts.
		if isHostOrNode(metadata) {
			return true, false
		}
		if label, ok := metadata.Labels["kubescape.io/host"]; ok {
			if hosts == nil || hosts.incomplete {
				return true, false
			}
			// Successful host SBOMs retain a sanitized name and a hash of the
			// raw host ID. Compare the hash without reconstructing the lossy name.
			if _, exists := hosts.ids[label]; exists {
				return true, false
			}
			i := strings.LastIndexByte(label, '-')
			if i <= 0 {
				return true, false
			}
			suffix := label[i+1:]
			if len(suffix) != 32 {
				return true, false
			}
			if _, err := hex.DecodeString(suffix); err != nil {
				return true, false
			}
			_, exists := hosts.hashes[suffix]
			return true, !exists
		}
	}
	if IsContainerProfileKind(kind) && metadata.Labels["kubescape.io/workload-kind"] == "Node" && metadata.Labels["kubescape.io/workload-namespace"] == "host" {
		if hosts == nil || hosts.incomplete {
			return true, false
		}
		wlid := metadata.Annotations["kubescape.io/wlid"]
		_, id, found := strings.Cut(wlid, "/namespace-host/host-")
		if !strings.HasPrefix(wlid, "wlid://cluster-") || !found || id == "" || strings.Contains(id, "/") {
			return true, false
		}
		_, exists := hosts.ids[id]
		return true, !exists
	}
	return false, false
}
