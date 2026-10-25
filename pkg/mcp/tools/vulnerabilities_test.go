package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func vulnMatch(id, severity, pkg, version, fixState string, fixVersions ...string) v1beta1.Match {
	return v1beta1.Match{
		Vulnerability: v1beta1.Vulnerability{
			VulnerabilityMetadata: v1beta1.VulnerabilityMetadata{
				ID:          id,
				Severity:    severity,
				Description: "description of " + id,
				DataSource:  "https://example.com/" + id,
				URLs:        []string{"https://example.com/" + id},
			},
			Fix: v1beta1.Fix{State: fixState, Versions: fixVersions},
		},
		Artifact: v1beta1.GrypePackage{Name: pkg, Version: version, Type: "deb"},
	}
}

func vulnReader() *memReader {
	return newMemReader(
		v1beta1.VulnerabilityManifest{
			ObjectMeta: metav1.ObjectMeta{Name: "nginx", Namespace: "kubescape"},
			Spec: v1beta1.VulnerabilityManifestSpec{Payload: v1beta1.GrypeDocument{Matches: []v1beta1.Match{
				vulnMatch("CVE-2026-0003", "Medium", "zlib1g", "1.2.11", "not-fixed"),
				vulnMatch("CVE-2026-0001", "Critical", "libssl1.1", "1.1.1n", "fixed", "1.1.1w"),
				vulnMatch("CVE-2026-0002", "High", "libc6", "2.31", "fixed", "2.31-13+deb11u5"),
				// Same CVE in a second package: must stay a separate entry.
				vulnMatch("CVE-2026-0001", "Critical", "openssl", "1.1.1n", "fixed", "1.1.1w"),
				// Same CVE, package and version again: reported once.
				vulnMatch("CVE-2026-0002", "High", "libc6", "2.31", "fixed", "2.31-13+deb11u5"),
				vulnMatch("CVE-2026-0004", "", "bash", "5.1", "unknown"),
			}}},
		},
		v1beta1.VulnerabilityManifest{ObjectMeta: metav1.ObjectMeta{Name: "clean", Namespace: "kubescape"}},
	)
}

// callVulnTool calls tool with the path namespace kubescape.
func callVulnTool(t *testing.T, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	handlers := map[string]func(Reader) server.ToolHandlerFunc{
		"list_vulnerabilities_in_manifest":   listVulnerabilitiesInManifest,
		"list_vulnerability_matches_for_cve": listVulnerabilityMatchesForCVE,
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := handlers[tool](vulnReader())(WithDefaultNamespace(context.Background(), "kubescape"), req)
	require.NoError(t, err)
	return res
}

func callVulnList(t *testing.T, args map[string]any) vulnerabilityListResponse {
	t.Helper()
	if _, ok := args["manifest_name"]; !ok {
		args["manifest_name"] = "nginx"
	}
	res := callVulnTool(t, "list_vulnerabilities_in_manifest", args)
	require.False(t, res.IsError, resultText(t, res))
	var resp vulnerabilityListResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &resp))
	return resp
}

func entryKeys(resp vulnerabilityListResponse) []string {
	keys := make([]string, 0, len(resp.Vulnerabilities))
	for _, v := range resp.Vulnerabilities {
		keys = append(keys, v.ID+"/"+v.Package)
	}
	return keys
}

func TestListVulnerabilitiesInManifest_DefaultsKeepPackageAndOrderBySeverity(t *testing.T) {
	resp := callVulnList(t, map[string]any{})
	assert.Equal(t, 5, resp.Total)
	assert.Equal(t, 5, resp.Returned)
	assert.False(t, resp.Truncated)
	assert.Nil(t, resp.NextOffset)
	assert.Equal(t, []string{
		"CVE-2026-0001/libssl1.1",
		"CVE-2026-0001/openssl",
		"CVE-2026-0002/libc6",
		"CVE-2026-0003/zlib1g",
		"CVE-2026-0004/bash",
	}, entryKeys(resp))

	first := resp.Vulnerabilities[0]
	assert.Equal(t, "Critical", first.Severity)
	assert.Equal(t, "1.1.1n", first.Version)
	assert.Equal(t, "deb", first.PackageType)
	assert.Equal(t, v1beta1.Fix{State: "fixed", Versions: []string{"1.1.1w"}}, first.Fix)
	assert.Empty(t, first.Description, "long text fields are left out by default")
	assert.Empty(t, first.URLs)
	assert.Empty(t, first.DataSource)

	assert.Equal(t, "Unknown", resp.Vulnerabilities[4].Severity, "an empty severity is reported as Unknown")
	assert.Equal(t, []string{}, resp.Vulnerabilities[4].Fix.Versions, "fix versions are [] rather than null")
}

func TestListVulnerabilitiesInManifest_IncludeDetails(t *testing.T) {
	resp := callVulnList(t, map[string]any{"include_details": true, "limit": float64(1)})
	require.Len(t, resp.Vulnerabilities, 1)
	v := resp.Vulnerabilities[0]
	assert.Equal(t, "description of CVE-2026-0001", v.Description)
	assert.Equal(t, []string{"https://example.com/CVE-2026-0001"}, v.URLs)
	assert.Equal(t, "https://example.com/CVE-2026-0001", v.DataSource)
}

func TestListVulnerabilitiesInManifest_Filters(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"min severity", map[string]any{"min_severity": "High"},
			[]string{"CVE-2026-0001/libssl1.1", "CVE-2026-0001/openssl", "CVE-2026-0002/libc6"}},
		{"min severity is case-insensitive", map[string]any{"min_severity": " medium "},
			[]string{"CVE-2026-0001/libssl1.1", "CVE-2026-0001/openssl", "CVE-2026-0002/libc6", "CVE-2026-0003/zlib1g"}},
		{"min severity drops unclassified", map[string]any{"min_severity": "Negligible"},
			[]string{"CVE-2026-0001/libssl1.1", "CVE-2026-0001/openssl", "CVE-2026-0002/libc6", "CVE-2026-0003/zlib1g"}},
		{"fixable only", map[string]any{"fixable_only": true},
			[]string{"CVE-2026-0001/libssl1.1", "CVE-2026-0001/openssl", "CVE-2026-0002/libc6"}},
		{"min severity and fixable only", map[string]any{"min_severity": "Critical", "fixable_only": true},
			[]string{"CVE-2026-0001/libssl1.1", "CVE-2026-0001/openssl"}},
		{"nothing left after filtering", map[string]any{"min_severity": "Medium", "fixable_only": true, "offset": float64(3)}, []string{}},
		{"null arguments are ignored", map[string]any{"min_severity": nil, "fixable_only": nil, "limit": nil, "offset": nil}, []string{
			"CVE-2026-0001/libssl1.1", "CVE-2026-0001/openssl", "CVE-2026-0002/libc6", "CVE-2026-0003/zlib1g", "CVE-2026-0004/bash"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, entryKeys(callVulnList(t, tt.args)))
		})
	}
}

func TestListVulnerabilitiesInManifest_Paging(t *testing.T) {
	page1 := callVulnList(t, map[string]any{"limit": float64(2)})
	assert.Equal(t, 5, page1.Total)
	assert.Equal(t, 2, page1.Returned)
	assert.True(t, page1.Truncated)
	require.NotNil(t, page1.NextOffset)
	assert.Equal(t, 2, *page1.NextOffset)

	page2 := callVulnList(t, map[string]any{"limit": float64(2), "offset": float64(*page1.NextOffset)})
	assert.Equal(t, []string{"CVE-2026-0002/libc6", "CVE-2026-0003/zlib1g"}, entryKeys(page2))
	require.NotNil(t, page2.NextOffset)

	page3 := callVulnList(t, map[string]any{"limit": float64(2), "offset": float64(*page2.NextOffset)})
	assert.Equal(t, []string{"CVE-2026-0004/bash"}, entryKeys(page3))
	assert.False(t, page3.Truncated)
	assert.Nil(t, page3.NextOffset)

	past := callVulnList(t, map[string]any{"offset": float64(10)})
	assert.Equal(t, 5, past.Total)
	assert.Equal(t, 0, past.Returned)
	assert.NotNil(t, past.Vulnerabilities)
	assert.False(t, past.Truncated)
}

func TestListVulnerabilitiesInManifest_EmptyManifest(t *testing.T) {
	res := callVulnTool(t, "list_vulnerabilities_in_manifest", map[string]any{"manifest_name": "clean"})
	assert.JSONEq(t, `{"total":0,"returned":0,"truncated":false,"vulnerabilities":[]}`, resultText(t, res))
}

func TestVulnerabilityTools_InvalidArguments(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		args      map[string]any
		wantError string
	}{
		{"unknown min severity", "list_vulnerabilities_in_manifest", map[string]any{"min_severity": "Severe"}, "min_severity must be one of Critical, High, Medium, Low, Negligible"},
		{"min severity Unknown", "list_vulnerabilities_in_manifest", map[string]any{"min_severity": "Unknown"}, "min_severity must be one of Critical, High, Medium, Low, Negligible"},
		{"min severity not a string", "list_vulnerabilities_in_manifest", map[string]any{"min_severity": 3}, "min_severity must be a string"},
		{"fixable_only not a boolean", "list_vulnerabilities_in_manifest", map[string]any{"fixable_only": "true"}, "fixable_only must be a boolean"},
		{"include_details not a boolean", "list_vulnerabilities_in_manifest", map[string]any{"include_details": 1}, "include_details must be a boolean"},
		{"limit zero", "list_vulnerabilities_in_manifest", map[string]any{"limit": float64(0)}, "limit must be between 1 and 500"},
		{"limit too large", "list_vulnerabilities_in_manifest", map[string]any{"limit": float64(501)}, "limit must be between 1 and 500"},
		{"limit not an integer", "list_vulnerabilities_in_manifest", map[string]any{"limit": 2.5}, "limit must be an integer"},
		{"limit as a string", "list_vulnerabilities_in_manifest", map[string]any{"limit": "10"}, "limit must be an integer"},
		{"negative offset", "list_vulnerabilities_in_manifest", map[string]any{"offset": float64(-1)}, "offset must not be negative"},
		{"no manifest name", "list_vulnerabilities_in_manifest", map[string]any{"manifest_name": ""}, "manifest_name is required"},
		{"no CVE", "list_vulnerability_matches_for_cve", map[string]any{}, "cve_id is required"},
		{"CVE not a string", "list_vulnerability_matches_for_cve", map[string]any{"cve_id": 2017}, "cve_id must be a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := tt.args["manifest_name"]; !ok {
				tt.args["manifest_name"] = "nginx"
			}
			te := toolErrorOf(t, callVulnTool(t, tt.tool, tt.args))
			assert.Equal(t, ErrCodeInvalidArgument, te.Code)
			assert.Contains(t, te.Message, tt.wantError)
		})
	}
}

func TestVulnerabilityTools_ManifestNotFound(t *testing.T) {
	for _, tool := range []string{"list_vulnerabilities_in_manifest", "list_vulnerability_matches_for_cve"} {
		te := toolErrorOf(t, callVulnTool(t, tool, map[string]any{"manifest_name": "redis", "cve_id": "CVE-2026-0001"}))
		assert.Equal(t, ErrCodeResourceNotFound, te.Code, tool)
		assert.Contains(t, te.Message, `"redis" not found`, tool)
	}
}

func TestListVulnerabilityMatchesForCVE(t *testing.T) {
	res := callVulnTool(t, "list_vulnerability_matches_for_cve", map[string]any{"manifest_name": "nginx", "cve_id": "CVE-2026-0001"})
	var matches []v1beta1.Match
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &matches))
	require.Len(t, matches, 2)
	assert.Equal(t, "libssl1.1", matches[0].Artifact.Name)
	assert.Equal(t, "openssl", matches[1].Artifact.Name)
	assert.Equal(t, "description of CVE-2026-0001", matches[0].Vulnerability.Description, "matches are returned whole")

	res = callVulnTool(t, "list_vulnerability_matches_for_cve", map[string]any{"manifest_name": "nginx", "cve_id": "CVE-0000-0000"})
	assert.Equal(t, "[]", resultText(t, res))
}

func TestListVulnerabilities_SamePackageNameWithDifferentTypesStaysSeparate(t *testing.T) {
	deb := vulnMatch("CVE-2026-0010", "High", "requests", "2.25.1", "not-fixed")
	python := vulnMatch("CVE-2026-0010", "High", "requests", "2.25.1", "not-fixed")
	python.Artifact.Type = "python"
	resp := listVulnerabilities([]v1beta1.Match{python, deb}, vulnerabilityListOptions{limit: 10})
	require.Len(t, resp.Vulnerabilities, 2)
	assert.Equal(t, "deb", resp.Vulnerabilities[0].PackageType)
	assert.Equal(t, "python", resp.Vulnerabilities[1].PackageType)
}

// Grype keeps matches of one CVE in one package from different vulnerability
// namespaces apart, and they can disagree on severity. The most severe one is
// reported, whatever the order of the matches and whatever the filter.
func TestListVulnerabilities_DuplicateKeepsMostSevere(t *testing.T) {
	medium := vulnMatch("CVE-2026-0020", "Medium", "openssl", "3.0.2", "not-fixed")
	critical := vulnMatch("CVE-2026-0020", "Critical", "openssl", "3.0.2", "fixed", "3.0.7")
	for name, matches := range map[string][]v1beta1.Match{
		"medium first":   {medium, critical},
		"critical first": {critical, medium},
	} {
		t.Run(name, func(t *testing.T) {
			for _, opts := range []vulnerabilityListOptions{
				{limit: 10},
				{limit: 10, minSeverity: severityCritical},
				{limit: 10, fixableOnly: true},
			} {
				resp := listVulnerabilities(matches, opts)
				require.Len(t, resp.Vulnerabilities, 1)
				assert.Equal(t, "Critical", resp.Vulnerabilities[0].Severity)
				assert.Equal(t, []string{"3.0.7"}, resp.Vulnerabilities[0].Fix.Versions)
			}
		})
	}
}

func TestListVulnerabilities_EmptyPackageTypeIsStillSerialized(t *testing.T) {
	m := vulnMatch("CVE-2026-0030", "Low", "busybox", "1.36", "unknown")
	m.Artifact.Type = ""
	out, err := json.Marshal(listVulnerabilities([]v1beta1.Match{m}, vulnerabilityListOptions{limit: 10}))
	require.NoError(t, err)
	assert.Contains(t, string(out), `"package_type":""`)
}

// permutations returns every ordering of matches.
func permutations(matches []v1beta1.Match) [][]v1beta1.Match {
	if len(matches) <= 1 {
		return [][]v1beta1.Match{append([]v1beta1.Match(nil), matches...)}
	}
	var out [][]v1beta1.Match
	for i := range matches {
		rest := append(append([]v1beta1.Match(nil), matches[:i]...), matches[i+1:]...)
		for _, p := range permutations(rest) {
			out = append(out, append([]v1beta1.Match{matches[i]}, p...))
		}
	}
	return out
}

func vulnMatchFrom(namespace string, m v1beta1.Match) v1beta1.Match {
	m.Vulnerability.Namespace = namespace
	m.Vulnerability.DataSource = "https://example.com/" + namespace
	return m
}

// Duplicates of one CVE in one package can come from several vulnerability
// namespaces that disagree on severity and on whether a fix exists. The entry
// keeps the most severe record and the best available fix, whatever the order.
func TestListVulnerabilities_DuplicatesAreOrderIndependent(t *testing.T) {
	tests := []struct {
		name         string
		matches      []v1beta1.Match
		wantSeverity string
		wantFix      v1beta1.Fix
		wantSource   string
	}{
		{
			name: "equal severity, only one has a fix",
			matches: []v1beta1.Match{
				vulnMatchFrom("debian", vulnMatch("CVE-2026-0040", "High", "openssl", "3.0.2", "not-fixed")),
				vulnMatchFrom("nvd", vulnMatch("CVE-2026-0040", "High", "openssl", "3.0.2", "fixed", "3.0.7")),
			},
			wantSeverity: "High",
			wantFix:      v1beta1.Fix{State: "fixed", Versions: []string{"3.0.7"}},
			wantSource:   "https://example.com/debian",
		},
		{
			name: "fix only on the less severe record",
			matches: []v1beta1.Match{
				vulnMatchFrom("nvd", vulnMatch("CVE-2026-0041", "Critical", "openssl", "3.0.2", "unknown")),
				vulnMatchFrom("debian", vulnMatch("CVE-2026-0041", "High", "openssl", "3.0.2", "fixed", "3.0.7")),
			},
			wantSeverity: "Critical",
			wantFix:      v1beta1.Fix{State: "fixed", Versions: []string{"3.0.7"}},
			wantSource:   "https://example.com/nvd",
		},
		{
			name: "equal severity, no fix anywhere",
			matches: []v1beta1.Match{
				vulnMatchFrom("nvd", vulnMatch("CVE-2026-0042", "Low", "bash", "5.1", "wont-fix")),
				vulnMatchFrom("debian", vulnMatch("CVE-2026-0042", "Low", "bash", "5.1", "not-fixed")),
			},
			wantSeverity: "Low",
			wantFix:      v1beta1.Fix{State: "not-fixed", Versions: []string{}},
			wantSource:   "https://example.com/debian",
		},
		{
			name: "fixes from several records: the most severe fixed one wins",
			matches: []v1beta1.Match{
				vulnMatchFrom("a", vulnMatch("CVE-2026-0043", "Medium", "curl", "7.74", "fixed", "7.80")),
				vulnMatchFrom("b", vulnMatch("CVE-2026-0043", "High", "curl", "7.74", "not-fixed")),
				vulnMatchFrom("c", vulnMatch("CVE-2026-0043", "High", "curl", "7.74", "fixed", "7.88")),
			},
			wantSeverity: "High",
			wantFix:      v1beta1.Fix{State: "fixed", Versions: []string{"7.88"}},
			wantSource:   "https://example.com/b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixable := tt.wantFix.State == "fixed"
			for _, order := range permutations(tt.matches) {
				all := listVulnerabilities(order, vulnerabilityListOptions{limit: 10, includeDetails: true})
				require.Len(t, all.Vulnerabilities, 1)
				got := all.Vulnerabilities[0]
				assert.Equal(t, tt.wantSeverity, got.Severity)
				assert.Equal(t, tt.wantFix, got.Fix)
				assert.Equal(t, tt.wantSource, got.DataSource)

				onlyFixable := listVulnerabilities(order, vulnerabilityListOptions{limit: 10, fixableOnly: true})
				if fixable {
					assert.Equal(t, 1, onlyFixable.Total)
				} else {
					assert.Equal(t, 0, onlyFixable.Total)
				}
			}
		})
	}
}
