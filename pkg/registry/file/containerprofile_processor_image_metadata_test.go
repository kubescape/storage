package file

import (
	"context"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPreSaveWithoutImageMetadata(t *testing.T) {
	processor := &ContainerProfileProcessor{
		HostType:                armotypes.HostTypeKubernetes,
		MaxContainerProfileSize: 40000,
		ContainerProfileStorage: &fakeStorage{},
	}
	profile := &softwarecomposition.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "container-without-image"},
	}

	require.NoError(t, processor.PreSave(context.Background(), profile))
	assert.NotNil(t, profile.Annotations)
}

func TestSBOMNameForImageInfo(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		imageID string
		want    string
		wantErr bool
	}{
		{name: "missing metadata", image: "", imageID: "", want: ""},
		{name: "whitespace metadata", image: "  ", imageID: "\t", want: ""},
		{name: "partial metadata remains invalid", image: "nginx:latest", imageID: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sbomNameForImageInfo(tt.image, tt.imageID)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
