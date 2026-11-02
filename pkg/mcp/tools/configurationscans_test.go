package tools

import (
	"context"
	"encoding/json"
	"sort"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func scannedControl(id, status, severity string) v1beta1.ScannedControl {
	return v1beta1.ScannedControl{
		ControlID: id,
		Name:      "control " + id,
		Severity:  v1beta1.ControlSeverity{Severity: severity},
		Status:    v1beta1.ScannedControlStatus{Status: status},
		Rules: []v1beta1.ScannedControlRule{{
			Name:   "rule-" + id,
			Status: v1beta1.RuleStatus{Status: status},
		}},
	}
}

func configScanFixture() v1beta1.WorkloadConfigurationScan {
	return v1beta1.WorkloadConfigurationScan{
		ObjectMeta: metav1.ObjectMeta{Name: "deployment-nginx", Namespace: "kubescape", Labels: map[string]string{"kubescape.io/workload-kind": "Deployment"}},
		Spec: v1beta1.WorkloadConfigurationScanSpec{
			Controls: map[string]v1beta1.ScannedControl{
				"C-0001": scannedControl("C-0001", "failed", "Critical"),
				"C-0002": scannedControl("C-0002", "failed", "Medium"),
				"C-0003": scannedControl("C-0003", "passed", "High"),
				"C-0004": scannedControl("C-0004", "passed", "Low"),
				"C-0005": scannedControl("C-0005", "skipped", "High"),
				"C-0006": scannedControl("C-0006", "failed", ""),
			},
			RelatedObjects: []v1beta1.WorkloadScanRelatedObject{{Kind: "ServiceAccount", Name: "nginx", Namespace: "default"}},
		},
	}
}

func configScanReader() *memReader {
	r := newMemReader()
	r.scans = []v1beta1.WorkloadConfigurationScan{
		configScanFixture(),
		{ObjectMeta: metav1.ObjectMeta{Name: "serviceaccount-default", Namespace: "kubescape"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "deployment-web", Namespace: "default"}},
	}
	return r
}

// callConfigScanTool calls tool on r with the path namespace kubescape.
func callConfigScanTool(t *testing.T, r Reader, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	handlers := map[string]func(Reader) server.ToolHandlerFunc{
		"list_configuration_security_scan_manifests": listConfigurationScans,
		"get_configuration_security_scan_manifest":   getConfigurationScan,
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := handlers[tool](r)(WithDefaultNamespace(context.Background(), "kubescape"), req)
	require.NoError(t, err)
	return res
}

func getConfigScan(t *testing.T, args map[string]any) v1beta1.WorkloadConfigurationScan {
	t.Helper()
	args["manifest_name"] = "deployment-nginx"
	res := callConfigScanTool(t, configScanReader(), "get_configuration_security_scan_manifest", args)
	require.False(t, res.IsError, resultText(t, res))
	var got v1beta1.WorkloadConfigurationScan
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &got))
	return got
}

func controlIDs(controls map[string]v1beta1.ScannedControl) []string {
	ids := make([]string, 0, len(controls))
	for id := range controls {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func TestGetConfigurationScanNoFilterReturnsEverything(t *testing.T) {
	got := getConfigScan(t, map[string]any{})
	assert.Equal(t, configScanFixture().Spec, got.Spec)

	got = getConfigScan(t, map[string]any{"status": nil, "min_severity": nil})
	assert.Equal(t, configScanFixture().Spec, got.Spec, "null arguments are treated as absent")
}

func TestGetConfigurationScanFilters(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"failed only", map[string]any{"status": "failed"}, []string{"C-0001", "C-0002", "C-0006"}},
		{"several statuses, any case and spacing", map[string]any{"status": " Failed , SKIPPED"}, []string{"C-0001", "C-0002", "C-0005", "C-0006"}},
		{"passed only", map[string]any{"status": "passed"}, []string{"C-0003", "C-0004"}},
		{"min severity drops lower and unrated controls", map[string]any{"min_severity": "High"}, []string{"C-0001", "C-0003", "C-0005"}},
		{"min severity is case-insensitive", map[string]any{"min_severity": "critical"}, []string{"C-0001"}},
		{"status and min severity together", map[string]any{"status": "failed", "min_severity": "Medium"}, []string{"C-0001", "C-0002"}},
		{"nothing matches", map[string]any{"status": "skipped", "min_severity": "Critical"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getConfigScan(t, tt.args)
			assert.Equal(t, tt.want, controlIDs(got.Spec.Controls))
			want := configScanFixture()
			for id, c := range got.Spec.Controls {
				assert.Equal(t, want.Spec.Controls[id], c, "a kept control is returned unchanged")
			}
			assert.Equal(t, want.Name, got.Name)
			assert.Equal(t, want.Labels, got.Labels)
			assert.Equal(t, want.Spec.RelatedObjects, got.Spec.RelatedObjects)
		})
	}
}

func TestGetConfigurationScanNothingMatchesIsEmptyObject(t *testing.T) {
	res := callConfigScanTool(t, configScanReader(), "get_configuration_security_scan_manifest",
		map[string]any{"manifest_name": "deployment-nginx", "status": "skipped", "min_severity": "Critical"})
	var raw struct {
		Spec struct {
			Controls json.RawMessage `json:"controls"`
		} `json:"spec"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &raw))
	assert.JSONEq(t, `{}`, string(raw.Spec.Controls))
}

func TestGetConfigurationScanInvalidArguments(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		wantError string
	}{
		{"no manifest_name", map[string]any{}, "manifest_name is required"},
		{"unknown status", map[string]any{"manifest_name": "deployment-nginx", "status": "failed,broken"}, "status must be a comma-separated list of failed, passed, skipped"},
		{"empty status item", map[string]any{"manifest_name": "deployment-nginx", "status": "failed,"}, "status must be a comma-separated list of failed, passed, skipped"},
		{"status not a string", map[string]any{"manifest_name": "deployment-nginx", "status": []any{"failed"}}, "status must be a string"},
		{"unknown min severity", map[string]any{"manifest_name": "deployment-nginx", "min_severity": "Severe"}, "min_severity must be one of Critical, High, Medium, Low"},
		{"negligible does not apply to controls", map[string]any{"manifest_name": "deployment-nginx", "min_severity": "Negligible"}, "min_severity must be one of Critical, High, Medium, Low"},
		{"min severity not a string", map[string]any{"manifest_name": "deployment-nginx", "min_severity": 3}, "min_severity must be a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The reader has nothing: a bad argument is rejected before any read.
			res := callConfigScanTool(t, &fakeReader{}, "get_configuration_security_scan_manifest", tt.args)
			require.True(t, res.IsError)
			assert.Equal(t, ErrCodeInvalidArgument, toolErrorOf(t, res).Code)
			assert.Contains(t, resultText(t, res), tt.wantError)
		})
	}
}

func TestGetConfigurationScanReadErrors(t *testing.T) {
	r := configScanReader()
	res := callConfigScanTool(t, r, "get_configuration_security_scan_manifest", map[string]any{"namespace": "default", "manifest_name": "deployment-nginx"})
	assert.Equal(t, ErrCodeResourceNotFound, toolErrorOf(t, res).Code)

	res = callConfigScanTool(t, r, "get_configuration_security_scan_manifest", map[string]any{"namespace": restrictedNamespace, "manifest_name": "deployment-nginx"})
	assert.Equal(t, ErrCodeRBACDenied, toolErrorOf(t, res).Code)
}

func TestListConfigurationScans(t *testing.T) {
	r := configScanReader()
	res := callConfigScanTool(t, r, "list_configuration_security_scan_manifests", map[string]any{})
	require.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"configuration_manifests":{"manifests":[
		{"namespace":"kubescape","manifest_name":"deployment-nginx"},
		{"namespace":"kubescape","manifest_name":"serviceaccount-default"}
	]}}`, resultText(t, res), "the namespace in the endpoint's URL by default")

	res = callConfigScanTool(t, r, "list_configuration_security_scan_manifests", map[string]any{"namespace": "default"})
	assert.JSONEq(t, `{"configuration_manifests":{"manifests":[{"namespace":"default","manifest_name":"deployment-web"}]}}`, resultText(t, res))

	res = callConfigScanTool(t, r, "list_configuration_security_scan_manifests", map[string]any{"namespace": restrictedNamespace})
	assert.Equal(t, ErrCodeRBACDenied, toolErrorOf(t, res).Code)

	res = callConfigScanTool(t, r, "list_configuration_security_scan_manifests", map[string]any{"continue": 3})
	assert.Equal(t, ErrCodeInvalidArgument, toolErrorOf(t, res).Code)
}

func TestListConfigurationScansContinue(t *testing.T) {
	r := &fakeReader{scans: &v1beta1.WorkloadConfigurationScanList{
		ListMeta: metav1.ListMeta{Continue: "next-page"},
		Items:    []v1beta1.WorkloadConfigurationScan{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "deployment-web"}}},
	}}
	res := callConfigScanTool(t, r, "list_configuration_security_scan_manifests", map[string]any{"namespace": "default", "continue": "this-page"})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, "default", r.namespace)
	assert.Equal(t, metav1.ListOptions{Limit: 100, Continue: "this-page"}, r.opts)
	assert.JSONEq(t, `{"configuration_manifests":{"manifests":[{"namespace":"default","manifest_name":"deployment-web"}]},"continue":"next-page"}`, resultText(t, res))
}
