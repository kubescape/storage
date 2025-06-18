package common

import (
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
)

func IsComplete(oldAnnotations map[string]string, newAnnotations map[string]string) bool {
	if c, ok := oldAnnotations[helpers.CompletionMetadataKey]; ok {
		if s, ok := oldAnnotations[helpers.StatusMetadataKey]; ok {
			return s == helpers.Completed && c == helpers.Full ||
				s == helpers.Completed && c == helpers.Partial && newAnnotations[helpers.CompletionMetadataKey] == helpers.Partial
		}
	}
	return false
}
