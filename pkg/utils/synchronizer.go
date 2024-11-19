package utils

import (
	"encoding/hex"
	"fmt"

	"github.com/SergJa/jsonhash"
	"go.uber.org/multierr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func CanonicalHash(in []byte) (string, error) {
	hash, err := jsonhash.CalculateJsonHash(in, []string{
		".status.conditions", // avoid Pod.status.conditions.lastProbeTime: null
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash[:]), nil
}

func RemoveManagedFields(d metav1.Object) {
	// Remove managed fields
	d.SetManagedFields(nil)
	// Remove last-applied-configuration annotation
	ann := d.GetAnnotations()
	delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
	d.SetAnnotations(ann)
}

func RemoveSpecificFields(d *unstructured.Unstructured, fields [][]string) error {
	var errs error
	for _, f := range fields {
		err := unstructured.SetNestedField(d.Object, nil, f...)
		if err != nil {
			errs = multierr.Append(errs, fmt.Errorf("failed to remove field %s: %w", f, err))
		}
	}
	return errs
}
