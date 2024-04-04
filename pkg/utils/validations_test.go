package utils

import (
	"testing"
)

func TestValidateStatusAnnotation(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{
			name:        "valid status - initializing",
			annotations: map[string]string{"kubescape.io/status": "initializing"},
			wantErr:     false,
		},
		{
			name:        "valid status - ready",
			annotations: map[string]string{"kubescape.io/status": "ready"},
			wantErr:     false,
		},
		{
			name:        "valid status - completed",
			annotations: map[string]string{"kubescape.io/status": "completed"},
			wantErr:     false,
		},
		{
			name:        "valid status - incomplete",
			annotations: map[string]string{"kubescape.io/status": "incomplete"},
			wantErr:     false,
		},
		{
			name:        "valid status - unauthorize",
			annotations: map[string]string{"kubescape.io/status": "unauthorize"},
			wantErr:     false,
		},

		{
			name:        "valid status - missing runtime",
			annotations: map[string]string{"kubescape.io/status": "missing-runtime"},
			wantErr:     false,
		},

		{
			name:        "valid status - too large",
			annotations: map[string]string{"kubescape.io/status": "too-large"},
			wantErr:     false,
		},
		{
			name:        "invalid status",
			annotations: map[string]string{"kubescape.io/status": "invalid"},
			wantErr:     true,
		},
		{
			name:        "no status",
			annotations: map[string]string{},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateStatusAnnotation(tt.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateStatusAnnotation() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateCompletionAnnotation(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		wantErr     bool
	}{
		{
			name:        "valid completion - complete",
			annotations: map[string]string{"kubescape.io/completion": "complete"},
			wantErr:     false,
		},
		{
			name:        "valid completion - partial",
			annotations: map[string]string{"kubescape.io/completion": "partial"},
			wantErr:     false,
		},
		{
			name:        "invalid completion",
			annotations: map[string]string{"kubescape.io/completion": "invalid"},
			wantErr:     true,
		},
		{
			name:        "no completion",
			annotations: map[string]string{},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCompletionAnnotation(tt.annotations)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateCompletionAnnotation() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
