package utils

import (
	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func ValidateCompletionAnnotation(annotations map[string]string) *field.Error {
	if v, ok := annotations[helpers.CompletionMetadataKey]; ok {
		switch v {
		case helpers.Complete, helpers.Partial:
			return nil
		default:
			return field.Invalid(field.NewPath("metadata").Child("annotations").Child(helpers.CompletionMetadataKey), v, "invalid value")
		}
	}
	return nil
}

func ValidateStatusAnnotation(annotations map[string]string) *field.Error {
	if v, ok := annotations[helpers.StatusMetadataKey]; ok {
		switch v {
		case helpers.Initializing, helpers.Ready, helpers.Completed, helpers.Incomplete, helpers.Unauthorize, helpers.MissingRuntime, helpers.TooLarge:
			return nil
		default:
			return field.Invalid(field.NewPath("metadata").Child("annotations").Child(helpers.StatusMetadataKey), v, "invalid value")
		}
	}

	return nil
}
