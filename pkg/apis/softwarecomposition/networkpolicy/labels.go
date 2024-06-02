package networkpolicy

var ignoreLabels map[string]bool

func init() {

	ignoreLabels = map[string]bool{
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
func IsIgnoredLabel(label string) bool {
	return ignoreLabels[label]
}
