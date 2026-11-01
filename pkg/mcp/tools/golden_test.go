package tools

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The golden files pin what an agent sees: the tool list with its argument
// schemas, and the answers to fixed calls on the objects in testdata, which
// are cut from a real grype scan and syft SBOM of nginx (kubevuln's testdata)
// from configuration scans that kubescape stored on a kind cluster, and from
// the container profile in storage's network policy testdata. kubescape
// mcpserver serves the same tools, so its tests can read these files from the
// storage module to check that both answer the same way.
//
// After an intended change, rewrite them with
//
//	go test ./pkg/mcp/tools -run TestGolden -update
var update = flag.Bool("update", false, "rewrite testdata/golden from the current tools")

// goldenNamespace is the namespace in the endpoint's URL for every call.
const goldenNamespace = "kagent"

type goldenCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// goldenResult is one call and the tool's answer. Response is the tool's JSON
// text, unpacked.
type goldenResult struct {
	Call     goldenCall      `json:"call"`
	IsError  bool            `json:"isError"`
	Response json.RawMessage `json:"response"`
}

var goldenCalls = []struct {
	file string
	call goldenCall
}{
	{"list_vulnerability_manifests/both", goldenCall{"list_vulnerability_manifests", map[string]any{"namespace": "kubescape"}}},
	{"list_vulnerability_manifests/image", goldenCall{"list_vulnerability_manifests", map[string]any{"namespace": "kubescape", "level": "image"}}},
	{"list_vulnerability_manifests/workload", goldenCall{"list_vulnerability_manifests", map[string]any{"namespace": "kubescape", "level": "workload"}}},
	{"list_vulnerability_manifests/path_namespace", goldenCall{"list_vulnerability_manifests", map[string]any{}}},
	{"list_vulnerability_manifests/forbidden", goldenCall{"list_vulnerability_manifests", map[string]any{"namespace": restrictedNamespace}}},
	{"list_vulnerability_manifests/invalid_level", goldenCall{"list_vulnerability_manifests", map[string]any{"namespace": "kubescape", "level": "all"}}},

	{"list_vulnerabilities_in_manifest/default", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9"}}},
	{"list_vulnerabilities_in_manifest/high_fixable", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9", "min_severity": "High", "fixable_only": true}}},
	{"list_vulnerabilities_in_manifest/details", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9", "include_details": true, "limit": 1}}},
	{"list_vulnerabilities_in_manifest/second_page", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9", "limit": 3, "offset": 3}}},
	{"list_vulnerabilities_in_manifest/workload", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": "kubescape", "manifest_name": "deployment-web-nginx-8b1f2c"}}},
	{"list_vulnerabilities_in_manifest/not_found", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"manifest_name": "nginx-32fdf9"}}},
	{"list_vulnerabilities_in_manifest/forbidden", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": restrictedNamespace, "manifest_name": "redis-a1c3e5"}}},
	{"list_vulnerabilities_in_manifest/invalid_severity", goldenCall{"list_vulnerabilities_in_manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9", "min_severity": "Severe"}}},

	{"list_vulnerability_matches_for_cve/two_packages", goldenCall{"list_vulnerability_matches_for_cve", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9", "cve_id": "CVE-2017-12424"}}},
	{"list_vulnerability_matches_for_cve/no_match", goldenCall{"list_vulnerability_matches_for_cve", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9", "cve_id": "CVE-2021-44228"}}},
	{"list_vulnerability_matches_for_cve/no_cve", goldenCall{"list_vulnerability_matches_for_cve", map[string]any{"namespace": "kubescape", "manifest_name": "nginx-32fdf9"}}},

	{"list_sboms/namespace", goldenCall{"list_sboms", map[string]any{"namespace": "kubescape"}}},
	{"list_sboms/path_namespace", goldenCall{"list_sboms", map[string]any{}}},
	{"list_sboms/forbidden", goldenCall{"list_sboms", map[string]any{"namespace": restrictedNamespace}}},

	{"list_sbom_packages/default", goldenCall{"list_sbom_packages", map[string]any{"namespace": "kubescape", "sbom_name": "nginx-32fdf9"}}},
	{"list_sbom_packages/name", goldenCall{"list_sbom_packages", map[string]any{"namespace": "kubescape", "sbom_name": "nginx-32fdf9", "name": "NGINX"}}},
	{"list_sbom_packages/package_type", goldenCall{"list_sbom_packages", map[string]any{"namespace": "kubescape", "sbom_name": "nginx-32fdf9", "package_type": "java-archive"}}},
	{"list_sbom_packages/second_page", goldenCall{"list_sbom_packages", map[string]any{"namespace": "kubescape", "sbom_name": "nginx-32fdf9", "limit": 5, "offset": 5}}},
	{"list_sbom_packages/too_large", goldenCall{"list_sbom_packages", map[string]any{"namespace": "kubescape", "sbom_name": "redis-a1c3e5"}}},
	{"list_sbom_packages/not_found", goldenCall{"list_sbom_packages", map[string]any{"sbom_name": "nginx-32fdf9"}}},
	{"list_sbom_packages/forbidden", goldenCall{"list_sbom_packages", map[string]any{"namespace": restrictedNamespace, "sbom_name": "redis-a1c3e5"}}},
	{"list_sbom_packages/invalid_limit", goldenCall{"list_sbom_packages", map[string]any{"namespace": "kubescape", "sbom_name": "nginx-32fdf9", "limit": 501}}},

	{"list_configuration_security_scan_manifests/namespace", goldenCall{"list_configuration_security_scan_manifests", map[string]any{"namespace": "default"}}},
	{"list_configuration_security_scan_manifests/path_namespace", goldenCall{"list_configuration_security_scan_manifests", map[string]any{}}},
	{"list_configuration_security_scan_manifests/forbidden", goldenCall{"list_configuration_security_scan_manifests", map[string]any{"namespace": restrictedNamespace}}},

	{"get_configuration_security_scan_manifest/default", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"namespace": "default", "manifest_name": "deployment-web"}}},
	{"get_configuration_security_scan_manifest/failed", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"namespace": "default", "manifest_name": "deployment-web", "status": "failed"}}},
	{"get_configuration_security_scan_manifest/failed_high", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"namespace": "default", "manifest_name": "deployment-web", "status": "failed", "min_severity": "High"}}},
	{"get_configuration_security_scan_manifest/not_found", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"manifest_name": "deployment-web"}}},
	{"get_configuration_security_scan_manifest/forbidden", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"namespace": restrictedNamespace, "manifest_name": "serviceaccount-default"}}},
	{"get_configuration_security_scan_manifest/invalid_status", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"namespace": "default", "manifest_name": "deployment-web", "status": "failed,excepted"}}},
	{"get_configuration_security_scan_manifest/invalid_severity", goldenCall{"get_configuration_security_scan_manifest", map[string]any{"namespace": "default", "manifest_name": "deployment-web", "min_severity": "Negligible"}}},

	{"list_container_profiles/namespace", goldenCall{"list_container_profiles", map[string]any{"namespace": "kubescape"}}},
	{"list_container_profiles/path_namespace", goldenCall{"list_container_profiles", map[string]any{}}},
	{"list_container_profiles/forbidden", goldenCall{"list_container_profiles", map[string]any{"namespace": restrictedNamespace}}},

	{"get_container_profile/default", goldenCall{"get_container_profile", map[string]any{"namespace": "kubescape", "profile_name": "replicaset-operator-55df98fc6d"}}},
	{"get_container_profile/not_found", goldenCall{"get_container_profile", map[string]any{"profile_name": "replicaset-operator-55df98fc6d"}}},
	{"get_container_profile/forbidden", goldenCall{"get_container_profile", map[string]any{"namespace": restrictedNamespace, "profile_name": "replicaset-operator-55df98fc6d"}}},
	{"get_container_profile/no_name", goldenCall{"get_container_profile", map[string]any{"namespace": "kubescape"}}},
}

func goldenServer(t *testing.T) *server.MCPServer {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "vulnerabilitymanifests.json"))
	require.NoError(t, err)
	var manifests []v1beta1.VulnerabilityManifest
	require.NoError(t, json.Unmarshal(b, &manifests))
	r := newMemReader(manifests...)
	b, err = os.ReadFile(filepath.Join("testdata", "sbomsyfts.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &r.sboms))
	b, err = os.ReadFile(filepath.Join("testdata", "workloadconfigurationscans.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &r.scans))
	b, err = os.ReadFile(filepath.Join("testdata", "containerprofiles.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(b, &r.profiles))
	s := server.NewMCPServer("kubescape-storage", "v1beta1")
	Register(s, r)
	return s
}

// handle sends one JSON-RPC request to s and returns its result.
func handle(t *testing.T, s *server.MCPServer, method string, params any) json.RawMessage {
	t.Helper()
	msg, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	b, err := json.Marshal(s.HandleMessage(WithDefaultNamespace(context.Background(), goldenNamespace), msg))
	require.NoError(t, err)
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	require.NoError(t, json.Unmarshal(b, &resp))
	require.Nil(t, resp.Error, string(b))
	return resp.Result
}

// checkGolden compares got with testdata/golden/name.json, or writes it there
// with -update.
func checkGolden(t *testing.T, name string, got any) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	path := filepath.Join("testdata", "golden", name+".json")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, append(b, '\n'), 0o644))
		return
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "run with -update to create it")
	assert.JSONEq(t, string(want), string(b))
}

func TestGoldenToolList(t *testing.T) {
	var result struct {
		Tools json.RawMessage `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(handle(t, goldenServer(t), "tools/list", map[string]any{}), &result))
	checkGolden(t, "tools", result.Tools)
}

func TestGoldenToolCalls(t *testing.T) {
	s := goldenServer(t)
	for _, gc := range goldenCalls {
		t.Run(gc.file, func(t *testing.T) {
			var result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			require.NoError(t, json.Unmarshal(handle(t, s, "tools/call", gc.call), &result))
			require.Len(t, result.Content, 1)
			text := []byte(result.Content[0].Text)
			require.True(t, json.Valid(text), "the tool answered with text that is not JSON: %s", text)
			checkGolden(t, gc.file, goldenResult{Call: gc.call, IsError: result.IsError, Response: text})
		})
	}
}
