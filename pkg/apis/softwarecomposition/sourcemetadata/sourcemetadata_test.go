package sourcemetadata

import (
	"reflect"
	"testing"

	"github.com/anchore/syft/syft/source"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Hand-maintained copy of syft's internal/sourcemetadata/names.go.
func TestReflectTypeFromJSONName(t *testing.T) {
	tests := []struct {
		input string
		want  any
	}{
		{"directory", source.DirectoryMetadata{}},
		{"dir", source.DirectoryMetadata{}}, // alias
		{"file", source.FileMetadata{}},
		{"image", source.ImageMetadata{}},
		{"Image", source.ImageMetadata{}}, // lookup is case-insensitive
		{"snap", source.SnapMetadata{}},   // added by the v1.55.0 sync
		{"oci-model", source.OCIModelMetadata{}},
		{"not-a-real-source-type", nil},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ReflectTypeFromJSONName(tt.input)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got, "expected %s to resolve", tt.input)
			assert.Equal(t, reflect.TypeOf(tt.want), got)
		})
	}
}
