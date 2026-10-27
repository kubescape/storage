package utils

import (
	"strings"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"k8s.io/apimachinery/pkg/api/validation/path"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// profileScopeAnnotations are the annotations whose values become segments of a
// ContainerProfile's storage key and payload path, or of its time-series rows.
var profileScopeAnnotations = []string{
	helpers.ClusterMetadataKey,
	helpers.CloudAccountIdentifierMetadataKey,
	helpers.RegionMetadataKey,
	helpers.HostIDMetadataKey,
	helpers.ReportSeriesIdMetadataKey,
}

// ValidateProfileScopeAnnotations rejects a scope annotation whose value cannot be one
// segment of a key: "." or "..", or carrying a slash, a percent sign, a backslash or a
// NUL byte. An absent or empty value is left to the key builder, which has a shape for a
// Kubernetes profile without a cluster.
func ValidateProfileScopeAnnotations(annotations map[string]string) field.ErrorList {
	var errs field.ErrorList
	for _, key := range profileScopeAnnotations {
		v := annotations[key]
		if v == "" {
			continue
		}
		if msg := PathSegmentProblem(v); msg != "" {
			errs = append(errs, field.Invalid(field.NewPath("metadata").Child("annotations").Child(key), v, msg))
		}
	}
	return errs
}

// PathSegmentProblem says why v cannot be one segment of a storage key, or nothing.
func PathSegmentProblem(v string) string {
	if msgs := path.IsValidPathSegmentName(v); len(msgs) > 0 {
		return strings.Join(msgs, ", ")
	}
	if strings.ContainsAny(v, "\\\x00") {
		return "may not contain '\\' or a NUL byte"
	}
	return ""
}

func ValidateCompletionAnnotation(annotations map[string]string) *field.Error {
	if v, ok := annotations[helpers.CompletionMetadataKey]; ok {
		switch v {
		case helpers.Full, helpers.Partial:
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
		case helpers.Initializing, helpers.Learning, helpers.Completed, helpers.Failed, helpers.Incomplete, helpers.Unauthorize, helpers.MissingRuntime, helpers.TooLarge, helpers.UnsupportedSchema:
			return nil
		default:
			return field.Invalid(field.NewPath("metadata").Child("annotations").Child(helpers.StatusMetadataKey), v, "invalid value")
		}
	}

	return nil
}
