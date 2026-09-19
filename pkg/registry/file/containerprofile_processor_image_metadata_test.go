package file

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/armosec/armoapi-go/armotypes"
	"github.com/kubescape/go-logger"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPreSaveWithoutImageMetadata(t *testing.T) {
	if scenario := os.Getenv("STORAGE_TEST_PRESAVE_IMAGE_METADATA"); scenario != "" {
		logger.InitLogger("pretty")
		require.NoError(t, logger.L().SetLevel("debug"))
		processor := &ContainerProfileProcessor{
			HostType:                armotypes.HostTypeKubernetes,
			MaxContainerProfileSize: 40000,
			ContainerProfileStorage: &fakeStorage{},
		}
		profile := &softwarecomposition.ContainerProfile{
			ObjectMeta: metav1.ObjectMeta{Name: "container-without-image"},
		}
		if scenario == "partial" {
			profile.Spec.ImageTag = "nginx:latest"
		}
		require.NoError(t, processor.PreSave(context.Background(), profile))
		assert.NotNil(t, profile.Annotations)
		return
	}

	for _, tc := range []struct {
		name           string
		wantDiagnostic bool
	}{
		{name: "empty image metadata"},
		{name: "partial", wantDiagnostic: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := "empty"
			if tc.wantDiagnostic {
				scenario = "partial"
			}
			command := exec.Command(os.Args[0], "-test.run=^TestPreSaveWithoutImageMetadata$")
			command.Env = append(os.Environ(), "STORAGE_TEST_PRESAVE_IMAGE_METADATA="+scenario)
			output, err := command.CombinedOutput()
			require.NoError(t, err, "%s", output)
			if tc.wantDiagnostic {
				assert.Contains(t, string(output), "ContainerProfileProcessor.PreSave - failed to get sbom name")
				assert.Contains(t, string(output), "imageTag: nginx:latest")
			} else {
				assert.NotContains(t, string(output), "ContainerProfileProcessor.PreSave")
			}
		})
	}
}
