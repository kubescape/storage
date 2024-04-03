package applicationprofile

import (
	"context"
	"reflect"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrepareForUpdate(t *testing.T) {
	tests := []struct {
		name           string
		oldAnnotations map[string]string
		newAnnotations map[string]string
		expected       map[string]string
	}{
		{
			name: "transition from complete (with status) to partial - rejected",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "initializing",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "ready",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "initializing",
			},
		},
		{
			name: "transition from complete to partial - accepted",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "initializing",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "ready",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "ready",
			},
		},
		{
			name: "transition from partial to complete - accepted",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "ready",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "ready",
			},
		},
		{
			name: "transition from complete (without status) to partial - rejected",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "initializing",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := applicationProfileStrategy{}

			obj := &softwarecomposition.ApplicationProfile{ObjectMeta: metav1.ObjectMeta{Annotations: tt.newAnnotations}}
			old := &softwarecomposition.ApplicationProfile{ObjectMeta: metav1.ObjectMeta{Annotations: tt.oldAnnotations}}

			s.PrepareForUpdate(context.Background(), obj, old)
			if !reflect.DeepEqual(obj.Annotations, tt.expected) {
				t.Errorf("PrepareForUpdate() = %v, want %v", obj.Annotations, tt.expected)
			}
		})
	}
}
