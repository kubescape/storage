package file

import (
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/kubescape/storage/pkg/generated/openapi"
	"github.com/stretchr/testify/require"
	"k8s.io/kube-openapi/pkg/schemaconv"
	"k8s.io/kube-openapi/pkg/validation/spec"
	smdschema "sigs.k8s.io/structured-merge-diff/v6/schema"
	"sigs.k8s.io/structured-merge-diff/v6/typed"
)

func generatedDefinitions() map[string]*spec.Schema {
	defs := openapi.GetOpenAPIDefinitions(func(name string) spec.Ref { return spec.MustCreateRef(name) })
	models := make(map[string]*spec.Schema, len(defs))
	for name, def := range defs {
		s := def.Schema
		models[name] = &s
	}
	return models
}

func generatedProfileParser(t *testing.T) *typed.Parser {
	t.Helper()
	typeSchema, err := schemaconv.ToSchemaFromOpenAPI(generatedDefinitions(), false)
	require.NoError(t, err)
	return &typed.Parser{Schema: smdschema.Schema{Types: typeSchema.Types}}
}

func repeatedPathLists() map[string]interface{} {
	return map[string]interface{}{
		"execs": []interface{}{
			map[string]interface{}{"path": "/bin/busybox", "args": []interface{}{"/bin/busybox", "sh", "-c", "id"}},
			map[string]interface{}{"path": "/bin/busybox", "args": []interface{}{"/bin/busybox", "ls", "-la"}},
			map[string]interface{}{"path": "/bin/busybox", "args": []interface{}{"/bin/busybox", "cat", "/etc/hostname"}},
		},
		"opens": []interface{}{
			map[string]interface{}{"path": "/etc/hosts", "flags": []interface{}{"O_RDONLY"}},
			map[string]interface{}{"path": "/etc/hosts", "flags": []interface{}{"O_RDONLY", "O_CLOEXEC"}},
		},
	}
}

func profileWithSpec(spec map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"apiVersion": "spdx.softwarecomposition.kubescape.io/v1beta1",
		"kind":       "ContainerProfile",
		"metadata":   map[string]interface{}{"name": "arc-runner-dind", "namespace": "arc-systems"},
		"spec":       spec,
	}
}

func TestContainerProfile_RepeatedExecAndOpenPaths_SMDConversion(t *testing.T) {
	parser := generatedProfileParser(t)
	value, err := parser.Type(v1beta1.ContainerProfile{}.OpenAPIModelName()).FromUnstructured(profileWithSpec(repeatedPathLists()))
	require.NoError(t, err)
	require.Equal(t, profileWithSpec(repeatedPathLists()), value.AsValue().Unstructured())
}

func TestContainerProfile_RepeatedPathsInContainerGroups_SMDConversion(t *testing.T) {
	parser := generatedProfileParser(t)
	container := repeatedPathLists()
	container["name"] = "dind"
	groups := map[string]interface{}{
		"containers":          []interface{}{container},
		"initContainers":      []interface{}{container},
		"ephemeralContainers": []interface{}{container},
	}
	value, err := parser.Type(v1beta1.ContainerProfile{}.OpenAPIModelName()).FromUnstructured(profileWithSpec(groups))
	require.NoError(t, err)
	require.Equal(t, profileWithSpec(groups), value.AsValue().Unstructured())
}

func TestContainerProfile_ExecAndOpenListsAreAtomic(t *testing.T) {
	defs := generatedDefinitions()
	for _, model := range []string{v1beta1.ContainerProfileSpec{}.OpenAPIModelName(), v1beta1.ContainerProfileContainer{}.OpenAPIModelName()} {
		def, ok := defs[model]
		require.True(t, ok, model)
		for _, field := range []string{"execs", "opens"} {
			prop, ok := def.Properties[field]
			require.True(t, ok, "%s.%s", model, field)
			require.Equal(t, "atomic", prop.Extensions["x-kubernetes-list-type"], "%s.%s", model, field)
			_, keyed := prop.Extensions["x-kubernetes-patch-merge-key"]
			require.False(t, keyed, "%s.%s", model, field)
			_, merging := prop.Extensions["x-kubernetes-patch-strategy"]
			require.False(t, merging, "%s.%s", model, field)
		}
	}
}
