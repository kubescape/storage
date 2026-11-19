package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func callNamed(t *testing.T, r Reader, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := toolHandler(t, r, tool)(context.Background(), req)
	require.NoError(t, err)
	return res
}

func meta(namespace, name, uid, resourceVersion string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Namespace: namespace, Name: name, UID: types.UID(uid), ResourceVersion: resourceVersion}
}

// The case reported on storage#451: with a page size of 1, read A from a
// manifest with A and B, the manifest is rescanned to B and C, and the next
// page (offset 1) gives C with a total of 2, so B is silently missed. With
// the first page's object_version, the next call says the manifest changed.
func TestObjectVersionDetectsARescanBetweenPages(t *testing.T) {
	r := newMemReader(v1beta1.VulnerabilityManifest{
		ObjectMeta: meta("kubescape", "nginx", "uid-1", "7"),
		Spec: v1beta1.VulnerabilityManifestSpec{Payload: v1beta1.GrypeDocument{Matches: []v1beta1.Match{
			vulnMatch("CVE-2026-0001", "High", "a", "1", "not-fixed"),
			vulnMatch("CVE-2026-0002", "High", "b", "1", "not-fixed"),
		}}},
	})
	args := map[string]any{"namespace": "kubescape", "manifest_name": "nginx", "limit": float64(1)}
	var first vulnerabilityListResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, callNamed(t, r, "list_vulnerabilities_in_manifest", args))), &first))
	assert.Equal(t, "uid-1/7", first.ObjectVersion)
	require.Equal(t, "CVE-2026-0001", first.Vulnerabilities[0].ID)

	// The rescan replaces the matches; storage raises the resourceVersion.
	r.manifests[0].ResourceVersion = "8"
	r.manifests[0].Spec.Payload.Matches = []v1beta1.Match{
		vulnMatch("CVE-2026-0002", "High", "b", "1", "not-fixed"),
		vulnMatch("CVE-2026-0003", "High", "c", "1", "not-fixed"),
	}

	next := map[string]any{"namespace": "kubescape", "manifest_name": "nginx", "limit": float64(1), "offset": float64(*first.NextOffset), "object_version": first.ObjectVersion}
	te := toolErrorOf(t, callNamed(t, r, "list_vulnerabilities_in_manifest", next))
	assert.Equal(t, ErrCodeObjectChanged, te.Code)
	assert.Equal(t, "uid-1/8", te.Details["object_version"])
	assert.Contains(t, te.Message, "start again from offset 0")

	// Without object_version the call still answers, with the new version.
	delete(next, "object_version")
	var page vulnerabilityListResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, callNamed(t, r, "list_vulnerabilities_in_manifest", next))), &page))
	assert.Equal(t, "uid-1/8", page.ObjectVersion)
}

// Every tool that pages through one object returns its object_version and
// checks the one it is given: the same version answers, an updated object or
// one deleted and created again (new UID, same resourceVersion) is
// OBJECT_CHANGED.
func TestObjectVersionOnEveryPagedTool(t *testing.T) {
	newReader := func() *memReader {
		return &memReader{
			manifests: []v1beta1.VulnerabilityManifest{{ObjectMeta: meta("kubescape", "nginx", "uid-1", "1"),
				Spec: v1beta1.VulnerabilityManifestSpec{Payload: v1beta1.GrypeDocument{Matches: []v1beta1.Match{
					vulnMatch("CVE-2026-0001", "High", "a", "1", "not-fixed"),
				}}}}},
			sboms: []v1beta1.SBOMSyft{{ObjectMeta: meta("kubescape", "nginx", "uid-1", "1"),
				Spec: v1beta1.SBOMSyftSpec{Syft: v1beta1.SyftDocument{Artifacts: []v1beta1.SyftPackage{sbomPackage("a", "1", "deb")}}}}},
			scans: []v1beta1.WorkloadConfigurationScan{{ObjectMeta: meta("default", "deployment-web", "uid-1", "1"),
				Spec: v1beta1.WorkloadConfigurationScanSpec{Controls: map[string]v1beta1.ScannedControl{
					"C-0016": scannedControl("C-0016", "failed", "Medium"),
				}}}},
			profiles: []v1beta1.ContainerProfile{{ObjectMeta: meta("kubescape", "web", "uid-1", "1"),
				Spec: v1beta1.ContainerProfileSpec{Syscalls: []string{"accept4"}}}},
		}
	}
	bump := map[string]func(r *memReader, uid, rv string){
		"manifest": func(r *memReader, uid, rv string) {
			r.manifests[0].UID, r.manifests[0].ResourceVersion = types.UID(uid), rv
		},
		"sbom": func(r *memReader, uid, rv string) { r.sboms[0].UID, r.sboms[0].ResourceVersion = types.UID(uid), rv },
		"scan": func(r *memReader, uid, rv string) { r.scans[0].UID, r.scans[0].ResourceVersion = types.UID(uid), rv },
		"profile": func(r *memReader, uid, rv string) {
			r.profiles[0].UID, r.profiles[0].ResourceVersion = types.UID(uid), rv
		},
	}
	calls := []struct {
		tool   string
		object string
		args   map[string]any
	}{
		{"list_vulnerabilities_in_manifest", "manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx"}},
		{"list_vulnerability_matches_for_cve", "manifest", map[string]any{"namespace": "kubescape", "manifest_name": "nginx", "cve_id": "CVE-2026-0001"}},
		{"list_sbom_packages", "sbom", map[string]any{"namespace": "kubescape", "sbom_name": "nginx"}},
		{"get_configuration_security_scan_manifest", "scan", map[string]any{"namespace": "default", "manifest_name": "deployment-web", "summary": true}},
		{"get_configuration_security_scan_manifest", "scan", map[string]any{"namespace": "default", "manifest_name": "deployment-web", "control_id": "C-0016"}},
		{"get_container_profile", "profile", map[string]any{"namespace": "kubescape", "profile_name": "web", "section": "syscalls"}},
	}
	with := func(args map[string]any, version string) map[string]any {
		out := map[string]any{"object_version": version}
		for k, v := range args {
			out[k] = v
		}
		return out
	}
	for _, c := range calls {
		t.Run(c.tool, func(t *testing.T) {
			r := newReader()
			res := callNamed(t, r, c.tool, c.args)
			require.False(t, res.IsError, resultText(t, res))
			var page struct {
				ObjectVersion string `json:"object_version"`
			}
			require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &page))
			assert.Equal(t, "uid-1/1", page.ObjectVersion)

			res = callNamed(t, r, c.tool, with(c.args, "uid-1/1"))
			assert.False(t, res.IsError, resultText(t, res))

			bump[c.object](r, "uid-1", "2")
			assert.Equal(t, ErrCodeObjectChanged, toolErrorOf(t, callNamed(t, r, c.tool, with(c.args, "uid-1/1"))).Code, "updated")

			bump[c.object](r, "uid-2", "1")
			assert.Equal(t, ErrCodeObjectChanged, toolErrorOf(t, callNamed(t, r, c.tool, with(c.args, "uid-1/1"))).Code, "deleted and created again")

			bad := with(c.args, "")
			bad["object_version"] = float64(1)
			assert.Equal(t, ErrCodeInvalidArgument, toolErrorOf(t, callNamed(t, r, c.tool, bad)).Code, "not a string")
		})
	}
}

// When the entry's severity and its fix come from two records, the fix's
// data source is named too (storage#451).
func TestListVulnerabilities_FixDataSourceWhenTheFixComesFromAnotherRecord(t *testing.T) {
	high := vulnMatchFrom("debian:12", vulnMatch("CVE-2026-0001", "High", "openssl", "3.0.1", "not-fixed"))
	fixed := vulnMatchFrom("nvd:cpe", vulnMatch("CVE-2026-0001", "Medium", "openssl", "3.0.1", "fixed", "3.0.2"))
	resp := listVulnerabilities([]v1beta1.Match{high, fixed}, vulnerabilityListOptions{includeDetails: true, limit: 10})
	require.Len(t, resp.Vulnerabilities, 1)
	e := resp.Vulnerabilities[0]
	assert.Equal(t, "High", e.Severity)
	assert.Equal(t, []string{"3.0.2"}, e.Fix.Versions)
	assert.Equal(t, "https://example.com/debian:12", e.DataSource)
	assert.Equal(t, "https://example.com/nvd:cpe", e.FixDataSource)

	// One record for both: no second source.
	both := vulnMatchFrom("debian:12", vulnMatch("CVE-2026-0001", "High", "openssl", "3.0.1", "fixed", "3.0.2"))
	resp = listVulnerabilities([]v1beta1.Match{both, fixed}, vulnerabilityListOptions{includeDetails: true, limit: 10})
	assert.Empty(t, resp.Vulnerabilities[0].FixDataSource)

	// Without details neither source is shown.
	resp = listVulnerabilities([]v1beta1.Match{high, fixed}, vulnerabilityListOptions{limit: 10})
	assert.Empty(t, resp.Vulnerabilities[0].DataSource)
	assert.Empty(t, resp.Vulnerabilities[0].FixDataSource)
}
