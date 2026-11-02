package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// configurationScansPageSize is the page size of
// list_configuration_security_scan_manifests, as in kubescape mcpserver.
const configurationScansPageSize = 100

// controlStatusFilterValues are the control statuses
// get_configuration_security_scan_manifest can filter on.
var controlStatusFilterValues = []string{"failed", "passed", "skipped"}

var listConfigurationScansTool = readOnlyTool(
	"list_configuration_security_scan_manifests",
	mcp.WithDescription("Discover available security configuration scan results at workload level (this returns a list of manifests, not the scan results themselves, to get the scan results, use the get_configuration_security_scan_manifest tool)"),
	mcp.WithString("namespace",
		mcp.Description("Namespace to list (optional, defaults to the namespace in the endpoint's URL). A workload's scan is stored in the workload's namespace; scans of cluster-scoped resources are in Kubescape's own namespace (usually kubescape)"),
	),
	mcp.WithString("continue",
		mcp.Description("Optional Kubernetes continuation token for pagination"),
	),
)

var getConfigurationScanTool = readOnlyTool(
	"get_configuration_security_scan_manifest",
	mcp.WithDescription("Get details of a specific security configuration scan result. Most controls in a result usually passed; set status to \"failed\" to get only the controls that need fixing."),
	mcp.WithString("namespace",
		mcp.Description("Namespace of the manifest, as returned by list_configuration_security_scan_manifests (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("manifest_name",
		mcp.Required(),
		mcp.Description("Name of the configuration manifest to get details for (get this from the list_configuration_security_scan_manifests tool)"),
	),
	mcp.WithString("status",
		mcp.Description("Only return controls with these statuses, comma-separated: failed, passed, skipped (optional; by default every control is returned)"),
	),
	mcp.WithString("min_severity",
		mcp.Description("Only return controls at or above this severity: Critical, High, Medium or Low (optional)"),
	),
)

// The response keeps the field names of kubescape mcpserver's tool. It leaves
// out the kubescape:// resource links, which this endpoint does not serve.
type configurationScanEntry struct {
	Namespace    string `json:"namespace"`
	ManifestName string `json:"manifest_name"`
}

type listConfigurationScansResult struct {
	ConfigurationManifests struct {
		Manifests []configurationScanEntry `json:"manifests"`
	} `json:"configuration_manifests"`
	Continue string `json:"continue,omitempty"`
}

func listConfigurationScans(r Reader) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		namespace, toolErr := stringArg(args, "namespace")
		if toolErr != nil {
			return toolErr, nil
		}
		if namespace == "" {
			namespace = defaultNamespace(ctx)
		}
		continueToken, toolErr := stringArg(args, "continue")
		if toolErr != nil {
			return toolErr, nil
		}

		list, err := r.ListWorkloadConfigurationScans(ctx, namespace, metav1.ListOptions{Limit: configurationScansPageSize, Continue: continueToken})
		if err != nil {
			return readError(err)
		}
		var result listConfigurationScansResult
		result.Continue = list.Continue
		result.ConfigurationManifests.Manifests = make([]configurationScanEntry, 0, len(list.Items))
		for _, s := range list.Items {
			result.ConfigurationManifests.Manifests = append(result.ConfigurationManifests.Manifests,
				configurationScanEntry{Namespace: s.Namespace, ManifestName: s.Name})
		}
		return jsonResult(result)
	}
}

func getConfigurationScan(r Reader) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		namespace, name, toolErr := manifestArgs(ctx, args)
		if toolErr != nil {
			return toolErr, nil
		}
		filter, toolErr := parseScannedControlFilter(args)
		if toolErr != nil {
			return toolErr, nil
		}
		scan, err := r.GetWorkloadConfigurationScan(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		scan.Spec.Controls = filterScannedControls(scan.Spec.Controls, filter)
		return jsonResult(scan)
	}
}

// scannedControlFilter selects the controls of a WorkloadConfigurationScan.
// The zero value keeps every control.
type scannedControlFilter struct {
	statuses    map[string]bool // nil means every status
	minSeverity severity        // severityUnknown means every severity
}

func (f scannedControlFilter) active() bool {
	return f.statuses != nil || f.minSeverity != severityUnknown
}

// parseScannedControlFilter reads the optional status and min_severity
// arguments of get_configuration_security_scan_manifest.
func parseScannedControlFilter(args map[string]any) (scannedControlFilter, *mcp.CallToolResult) {
	var f scannedControlFilter

	status, toolErr := stringArg(args, "status")
	if toolErr != nil {
		return f, toolErr
	}
	if status != "" {
		f.statuses = map[string]bool{}
		for _, s := range strings.Split(status, ",") {
			s = strings.ToLower(strings.TrimSpace(s))
			if !slices.Contains(controlStatusFilterValues, s) {
				return f, toolError(ErrCodeInvalidArgument,
					fmt.Sprintf("status must be a comma-separated list of %s (got %q)", strings.Join(controlStatusFilterValues, ", "), status),
					map[string]any{"argument": "status", "supported_values": controlStatusFilterValues})
			}
			f.statuses[s] = true
		}
	}

	minSeverity, toolErr := stringArg(args, "min_severity")
	if toolErr != nil || minSeverity == "" {
		return f, toolErr
	}
	f.minSeverity = parseSeverity(minSeverity)
	// Controls are rated Low to Critical; grype's Negligible does not apply.
	if f.minSeverity == severityUnknown || f.minSeverity == severityNegligible {
		return f, toolError(ErrCodeInvalidArgument,
			fmt.Sprintf("min_severity must be one of Critical, High, Medium, Low (got %q)", minSeverity),
			map[string]any{"argument": "min_severity", "supported_values": []string{"Critical", "High", "Medium", "Low"}})
	}
	return f, nil
}

// filterScannedControls returns the controls the filter selects. It never
// modifies the given map.
func filterScannedControls(controls map[string]v1beta1.ScannedControl, f scannedControlFilter) map[string]v1beta1.ScannedControl {
	if !f.active() {
		return controls
	}
	out := make(map[string]v1beta1.ScannedControl, len(controls))
	for id, c := range controls {
		if f.statuses != nil && !f.statuses[strings.ToLower(c.Status.Status)] {
			continue
		}
		if parseSeverity(c.Severity.Severity) < f.minSeverity {
			continue
		}
		out[id] = c
	}
	return out
}
