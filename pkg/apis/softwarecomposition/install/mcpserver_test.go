package install

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestMCPServerIsRegistered(t *testing.T) {
	scheme := runtime.NewScheme()
	Install(scheme)

	for _, gv := range []schema.GroupVersion{softwarecomposition.SchemeGroupVersion, v1beta1.SchemeGroupVersion} {
		for _, kind := range []string{"MCPServer", "MCPServerList"} {
			_, err := scheme.New(gv.WithKind(kind))
			assert.NoError(t, err, "%s %s", gv, kind)
		}
	}
}

func TestMCPServerConversionKeepsMetadata(t *testing.T) {
	scheme := runtime.NewScheme()
	Install(scheme)

	external := &v1beta1.MCPServer{ObjectMeta: metav1.ObjectMeta{
		Name:      "default",
		Namespace: "agents",
		Labels:    map[string]string{"app": "kagent"},
	}}
	internal := &softwarecomposition.MCPServer{}
	require.NoError(t, scheme.Convert(external, internal, nil))
	assert.Equal(t, external.ObjectMeta, internal.ObjectMeta)

	back := &v1beta1.MCPServer{}
	require.NoError(t, scheme.Convert(internal, back, nil))
	assert.Equal(t, external, back)
}
