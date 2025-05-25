package applicationprofile

import (
	"context"
	"testing"

	"github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPrepareForUpdateAnnotations(t *testing.T) {
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
			name: "transition from partial (with status) to complete - accepted",
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
			name: "transition from partial (without status) to complete - accepted",
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
		{
			name: "transition from a final AP - all changes are rejected",
			oldAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "completed",
			},
			newAnnotations: map[string]string{
				helpers.CompletionMetadataKey: "partial",
				helpers.StatusMetadataKey:     "initializing",
			},
			expected: map[string]string{
				helpers.CompletionMetadataKey: "complete",
				helpers.StatusMetadataKey:     "completed",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ApplicationProfileStrategy{}

			obj := &softwarecomposition.ApplicationProfile{ObjectMeta: metav1.ObjectMeta{Annotations: tt.newAnnotations}}
			old := &softwarecomposition.ApplicationProfile{ObjectMeta: metav1.ObjectMeta{Annotations: tt.oldAnnotations}}

			s.PrepareForUpdate(context.TODO(), obj, old)
			assert.Equal(t, tt.expected, obj.Annotations)
		})
	}
}

func TestPrepareForUpdateFullObj(t *testing.T) {
	tests := []struct {
		name     string
		old      *softwarecomposition.ApplicationProfile
		new      *softwarecomposition.ApplicationProfile
		expected *softwarecomposition.ApplicationProfile
	}{
		{
			name: "transition from initializing to ready - changes are accepted",
			old: &softwarecomposition.ApplicationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "initializing",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
					},
				},
			},
			new: &softwarecomposition.ApplicationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "ready",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
						{
							Name:         "container2",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
					},
				},
			},
			expected: &softwarecomposition.ApplicationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "ready",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
						{
							Name:         "container2",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
					},
				},
			},
		},
		{
			name: "transition from a final AP - all changes are rejected",
			old: &softwarecomposition.ApplicationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "completed",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
					},
				},
			},
			new: &softwarecomposition.ApplicationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "partial",
						helpers.StatusMetadataKey:     "initializing",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
						{
							Name:         "container2",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
					},
				},
			},
			expected: &softwarecomposition.ApplicationProfile{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						helpers.CompletionMetadataKey: "complete",
						helpers.StatusMetadataKey:     "completed",
					},
				},
				Spec: softwarecomposition.ApplicationProfileSpec{
					Containers: []softwarecomposition.ApplicationProfileContainer{
						{
							Name:         "container1",
							Capabilities: []string{},
							Execs: []softwarecomposition.ExecCalls{
								{Path: "/usr/bin/ls", Args: []string{"-l", "/tmp"}},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ApplicationProfileStrategy{}
			s.PrepareForUpdate(context.TODO(), tt.new, tt.old)
			assert.Equal(t, tt.expected, tt.new)
		})
	}
}
