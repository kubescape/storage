package networkpolicy

import "sync"

type syncMap struct {
	mut sync.RWMutex
	m   map[string]bool
}

var ignoreLabels syncMap

func init() {
	ignoreLabels.mut.RLock()
	if len(ignoreLabels.m) > 0 {
		ignoreLabels.mut.RUnlock()
		return
	}
	ignoreLabels.mut.RUnlock()

	ignoreLabels.mut.Lock()
	defer ignoreLabels.mut.Unlock()
	ignoreLabels.m = map[string]bool{
		"app.kubernetes.io/name":                      false,
		"app.kubernetes.io/part-of":                   false,
		"app.kubernetes.io/component":                 false,
		"app.kubernetes.io/instance":                  true,
		"app.kubernetes.io/version":                   true,
		"app.kubernetes.io/managed-by":                true,
		"app.kubernetes.io/created-by":                true,
		"app.kubernetes.io/owner":                     true,
		"app.kubernetes.io/revision":                  true,
		"statefulset.kubernetes.io/pod-name":          true,
		"scheduler.alpha.kubernetes.io/node-selector": true,
		"pod-template-hash":                           true,
		"controller-revision-hash":                    true,
		"pod-template-generation":                     true,
		"helm.sh/chart":                               true,
	}
}

// IsIgnoredLabel returns true if the label is ignored
func isIgnoredLabel(label string) bool {
	ignoreLabels.mut.RLock()
	defer ignoreLabels.mut.RUnlock()
	return ignoreLabels.m[label]
}
