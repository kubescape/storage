package endpoint

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tests in this file check that a result above the default size cap
// (256 KiB) can still be read in full through the tool's paging, through the
// whole endpoint and its limits.

func callTool(t *testing.T, te *testEndpoint, tool, args string) (string, bool) {
	t.Helper()
	cert := te.frontCA.clientCert(t, frontProxyName)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	return toolResult(t, te.call(t, "kagent", cert, agent, body))
}

func TestCVEMatchesAboveTheSizeCapCanBePaged(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	const n = 300
	matches := make([]softwarecomposition.Match, 0, n+1)
	for i := range n {
		matches = append(matches, softwarecomposition.Match{
			Vulnerability: softwarecomposition.Vulnerability{VulnerabilityMetadata: softwarecomposition.VulnerabilityMetadata{
				ID: "CVE-2026-1000", Severity: "High", Description: strings.Repeat("d", 1500)}},
			Artifact: softwarecomposition.GrypePackage{Name: fmt.Sprintf("pkg-%03d", i), Version: "1.0", Type: "deb"},
		})
	}
	matches = append(matches, softwarecomposition.Match{
		Vulnerability: softwarecomposition.Vulnerability{VulnerabilityMetadata: softwarecomposition.VulnerabilityMetadata{ID: "CVE-2026-2000"}},
		Artifact:      softwarecomposition.GrypePackage{Name: "other"},
	})
	te.lister.items[0].Spec.Payload.Matches = matches
	whole, err := json.Marshal(matches[:n])
	require.NoError(t, err)
	require.Greater(t, len(whole), 256<<10, "the matches of the CVE are above the default cap")

	var names []string
	offset := 0
	for pages := 0; ; pages++ {
		require.Less(t, pages, n, "paging ends")
		text, isError := callTool(t, te, "list_vulnerability_matches_for_cve",
			fmt.Sprintf(`{"namespace":"kubescape","manifest_name":"nginx-1.21","cve_id":"CVE-2026-1000","offset":%d}`, offset))
		require.False(t, isError, text)
		var page struct {
			Total      int  `json:"total"`
			NextOffset *int `json:"next_offset"`
			Matches    []struct {
				Artifact struct {
					Name string `json:"name"`
				} `json:"artifact"`
			} `json:"matches"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &page))
		assert.Equal(t, n, page.Total)
		for _, m := range page.Matches {
			names = append(names, m.Artifact.Name)
		}
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	require.Len(t, names, n, "every match is returned once")
	for i, name := range names {
		assert.Equal(t, fmt.Sprintf("pkg-%03d", i), name)
	}

	// The largest page is still under the cap.
	text, isError := callTool(t, te, "list_vulnerability_matches_for_cve",
		`{"namespace":"kubescape","manifest_name":"nginx-1.21","cve_id":"CVE-2026-1000","limit":100}`)
	require.False(t, isError, text)
}

func TestContainerProfileAboveTheSizeCapCanBePaged(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	const n = 20000
	opens := make([]softwarecomposition.OpenCalls, n)
	for i := range opens {
		opens[i] = softwarecomposition.OpenCalls{Path: fmt.Sprintf("/var/lib/app/data/file-%05d", i), Flags: []string{"O_RDONLY"}}
	}
	te.profiles.items[0].Spec.Opens = opens

	// The whole profile is above the cap; the error says how to get it.
	text, isError := callTool(t, te, "get_container_profile", `{"namespace":"kubescape","profile_name":"replicaset-web-7d4b9c-nginx"}`)
	require.True(t, isError)
	var toolErr tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeResponseTooLarge, toolErr.Code)
	assert.Equal(t, tools.SizeHint("get_container_profile"), toolErr.Details["hint"])
	assert.Contains(t, toolErr.Message, "set section")

	// Every open is returned, a section page at a time.
	var got int
	offset := 0
	for pages := 0; ; pages++ {
		require.Less(t, pages, n, "paging ends")
		text, isError := callTool(t, te, "get_container_profile",
			fmt.Sprintf(`{"namespace":"kubescape","profile_name":"replicaset-web-7d4b9c-nginx","section":"opens","limit":500,"offset":%d}`, offset))
		require.False(t, isError, text)
		var page struct {
			Total      int  `json:"total"`
			NextOffset *int `json:"next_offset"`
			Entries    []struct {
				Path string `json:"path"`
			} `json:"entries"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &page))
		assert.Equal(t, n, page.Total)
		for _, e := range page.Entries {
			require.Equal(t, fmt.Sprintf("/var/lib/app/data/file-%05d", got), e.Path)
			got++
		}
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	assert.Equal(t, n, got)
}

// Under a small size cap the first page of a list can already be too large,
// before there is any continue token. The error's hint (a lower limit) must
// lead to every item.
func TestListAboveASmallSizeCapCanBePaged(t *testing.T) {
	te := newTestEndpoint(t, "kubescape", func(c *Config) { c.MaxResponseBytes = 4 << 10 })
	const n = 101
	te.sboms.items = make([]softwarecomposition.SBOMSyft, n)
	for i := range te.sboms.items {
		te.sboms.items[i].Namespace = "kubescape"
		te.sboms.items[i].Name = fmt.Sprintf("docker.io-library-image-%03d-0123456789abcdef", i)
	}

	text, isError := callTool(t, te, "list_sboms", `{"namespace":"kubescape"}`)
	require.True(t, isError)
	var toolErr tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeResponseTooLarge, toolErr.Code)
	assert.Equal(t, "lower limit and page with continue", toolErr.Details["hint"])

	var names []string
	cont := ""
	for pages := 0; ; pages++ {
		require.Less(t, pages, n, "paging ends")
		text, isError := callTool(t, te, "list_sboms", fmt.Sprintf(`{"namespace":"kubescape","limit":20,"continue":%q}`, cont))
		require.False(t, isError, text)
		var page struct {
			SBOMs []struct {
				Name string `json:"sbom_name"`
			} `json:"sboms"`
			Continue string `json:"continue"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &page))
		for _, s := range page.SBOMs {
			names = append(names, s.Name)
		}
		if page.Continue == "" {
			break
		}
		cont = page.Continue
	}
	require.Len(t, names, n, "every SBOM is returned once")
	for i, name := range names {
		assert.Equal(t, fmt.Sprintf("docker.io-library-image-%03d-0123456789abcdef", i), name)
	}
}

// One control can have more paths than fit under the cap even with every
// filter applied. The hint leads to the summary and then to that control's
// paths, a page at a time.
func TestConfigurationScanControlAboveTheSizeCapCanBePaged(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	const n = 4000
	paths := make([]softwarecomposition.RulePath, n)
	for i := range paths {
		paths[i] = softwarecomposition.RulePath{FailedPath: fmt.Sprintf("rules[%d].verbs[0]", i), FixPath: strings.Repeat("f", 60)}
	}
	te.scans.items[0].Spec.Controls["C-0035"] = softwarecomposition.ScannedControl{
		ControlID: "C-0035", Name: "Administrative Roles",
		Severity: softwarecomposition.ControlSeverity{Severity: "High"},
		Status:   softwarecomposition.ScannedControlStatus{Status: "failed"},
		Rules:    []softwarecomposition.ScannedControlRule{{Name: "rule-list-all-cluster-admins", Status: softwarecomposition.RuleStatus{Status: "failed"}, Paths: paths}},
	}

	text, isError := callTool(t, te, "get_configuration_security_scan_manifest",
		`{"namespace":"kubescape","manifest_name":"deployment-web","status":"failed","min_severity":"High"}`)
	require.True(t, isError)
	var toolErr tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeResponseTooLarge, toolErr.Code)
	assert.Contains(t, toolErr.Details["hint"], "set summary")

	text, isError = callTool(t, te, "get_configuration_security_scan_manifest",
		`{"namespace":"kubescape","manifest_name":"deployment-web","status":"failed","min_severity":"High","summary":true}`)
	require.False(t, isError, text)
	assert.Contains(t, text, `"control_id":"C-0035"`)

	got := 0
	offset := 0
	for pages := 0; ; pages++ {
		require.Less(t, pages, n, "paging ends")
		text, isError := callTool(t, te, "get_configuration_security_scan_manifest",
			fmt.Sprintf(`{"namespace":"kubescape","manifest_name":"deployment-web","control_id":"C-0035","limit":500,"offset":%d}`, offset))
		require.False(t, isError, text)
		var page struct {
			Total      int  `json:"total"`
			NextOffset *int `json:"next_offset"`
			Paths      []struct {
				FailedPath string `json:"failedPath"`
			} `json:"paths"`
		}
		require.NoError(t, json.Unmarshal([]byte(text), &page))
		assert.Equal(t, n, page.Total)
		for _, p := range page.Paths {
			require.Equal(t, fmt.Sprintf("rules[%d].verbs[0]", got), p.FailedPath)
			got++
		}
		if page.NextOffset == nil {
			break
		}
		offset = *page.NextOffset
	}
	assert.Equal(t, n, got)
}
