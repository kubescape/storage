package tools

import (
	"encoding/json"

	"testing"

	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolsAreMarkedReadOnly(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	Register(s, &fakeReader{})
	require.NotEmpty(t, s.ListTools())
	for name, tool := range s.ListTools() {
		a := tool.Tool.Annotations
		require.NotNil(t, a.ReadOnlyHint, name)
		require.NotNil(t, a.DestructiveHint, name)
		require.NotNil(t, a.OpenWorldHint, name)
		assert.True(t, *a.ReadOnlyHint, name)
		assert.False(t, *a.DestructiveHint, name)
		assert.False(t, *a.OpenWorldHint, name)
	}
}

func TestSizeHintsNameRegisteredTools(t *testing.T) {
	s := server.NewMCPServer("test", "0")
	Register(s, &fakeReader{})
	for name := range sizeHints {
		assert.NotNil(t, s.GetTool(name), "size hint for unknown tool %s", name)
		assert.Equal(t, sizeHints[name], SizeHint(name))
	}
	assert.Equal(t, "ask for less with the tool's paging or filters", SizeHint("not_a_tool"))
	// Every tool has its own way to a smaller answer, so a caller is never
	// told something it can't do.
	for _, tool := range Tools(newMemReader()) {
		assert.Contains(t, sizeHints, tool.Tool.Name, "no size hint for %s", tool.Tool.Name)
	}
}

func TestToolsWithResourceLinks(t *testing.T) {
	s := goldenServer(t, WithResourceLinks())
	for _, tc := range []struct {
		tool, namespace, list, items, nameKey, uriPrefix string
		templates                                        map[string]any
	}{
		{"list_vulnerability_manifests", "kubescape", "vulnerability_manifests", "manifests", "manifest_name", "kubescape://vulnerability-manifests/",
			map[string]any{
				"vulnerability_manifest_cve_list":    "kubescape://vulnerability-manifests/{namespace}/{manifest_name}/cve_list",
				"vulnerability_manifest_cve_details": "kubescape://vulnerability-manifests/{namespace}/{manifest_name}/cve_details/{cve_id}",
			}},
		{"list_configuration_security_scan_manifests", "default", "configuration_manifests", "manifests", "manifest_name", "kubescape://configuration-manifests/",
			map[string]any{"configuration_manifest_details": "kubescape://configuration-manifests/{namespace}/{manifest_name}"}},
		{"list_container_profiles", "kubescape", "container_profiles", "profiles", "profile_name", "kubescape://container-profiles/",
			map[string]any{"container_profile_details": "kubescape://container-profiles/{namespace}/{profile_name}"}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			var result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			}
			call := map[string]any{"name": tc.tool, "arguments": map[string]any{"namespace": tc.namespace}}
			require.NoError(t, json.Unmarshal(handle(t, s, "tools/call", call), &result))
			require.Len(t, result.Content, 1)
			var got map[string]any
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &got))
			assert.Equal(t, tc.templates, got["available_templates"])
			items := got[tc.list].(map[string]any)[tc.items].([]any)
			require.NotEmpty(t, items)
			for _, it := range items {
				item := it.(map[string]any)
				assert.Equal(t, tc.uriPrefix+item["namespace"].(string)+"/"+item[tc.nameKey].(string), item["resource_uri"])
			}
		})
	}
}

func TestWithNamespaceDescription(t *testing.T) {
	const desc = "Namespace to list (optional, default all namespaces)"
	before := listSBOMsTool.InputSchema.Properties["namespace"]
	for _, tool := range Tools(&fakeReader{}, WithNamespaceDescription("list_sboms", desc)) {
		ns := tool.Tool.InputSchema.Properties["namespace"].(map[string]any)
		if tool.Tool.Name == "list_sboms" {
			assert.Equal(t, desc, ns["description"])
			assert.Equal(t, "string", ns["type"])
		} else {
			assert.NotEqual(t, desc, ns["description"], tool.Tool.Name)
		}
	}
	assert.Equal(t, before, listSBOMsTool.InputSchema.Properties["namespace"], "the shared tool definition changed")
	assert.NotEqual(t, desc, Tools(&fakeReader{})[3].Tool.InputSchema.Properties["namespace"].(map[string]any)["description"])
}

// toolHandler returns the handler of the tool named name.
func toolHandler(t *testing.T, r Reader, name string, opts ...Option) server.ToolHandlerFunc {
	t.Helper()
	for _, tool := range Tools(r, opts...) {
		if tool.Tool.Name == name {
			return tool.Handler
		}
	}
	t.Fatalf("no tool %s", name)
	return nil
}
