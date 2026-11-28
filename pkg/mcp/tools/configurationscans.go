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

// With summary, limit and offset page through the controls; with control_id,
// through the paths of one control.
const (
	defaultControlPageLimit = 100
	maxControlPageLimit     = 500
)

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
	listLimitOption("manifests", configurationScansPageSize),
)

var getConfigurationScanTool = readOnlyTool(
	"get_configuration_security_scan_manifest",
	mcp.WithDescription("Get details of a specific security configuration scan result. Most controls in a result usually passed; set status to \"failed\" to get only the controls that need fixing. If the answer is too large, set summary to list the controls without their rules, then set control_id to page through one control's failed paths."),
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
	mcp.WithBoolean("summary",
		mcp.Description("Return only each control's ID, name, severity and status, without its rules and paths, a page at a time (optional, default false); works with status and min_severity"),
	),
	mcp.WithString("control_id",
		mcp.Description("Return one control, for example C-0017, and a page of its paths (what failed and how to fix it) instead of the whole result (optional; not with status, min_severity or summary)"),
	),
	mcp.WithNumber("limit",
		mcp.Description("With summary: maximum number of controls to return; with control_id: maximum number of paths (optional, default 100, maximum 500)"),
	),
	mcp.WithNumber("offset",
		mcp.Description("With summary or control_id: number of controls or paths to skip; pass the previous response's next_offset to get the next page (optional, default 0)"),
	),
	objectVersionOption("scan"),
)

// The response keeps the field names of kubescape mcpserver's tool. The
// kubescape:// resource links are only added WithResourceLinks.
type configurationScanEntry struct {
	Namespace    string `json:"namespace"`
	ManifestName string `json:"manifest_name"`
	ResourceURI  string `json:"resource_uri,omitempty"`
}

type listConfigurationScansResult struct {
	ConfigurationManifests struct {
		Manifests []configurationScanEntry `json:"manifests"`
	} `json:"configuration_manifests"`
	Continue           string            `json:"continue,omitempty"`
	AvailableTemplates map[string]string `json:"available_templates,omitempty"`
}

func listConfigurationScans(r Reader, o options) server.ToolHandlerFunc {
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

		limit, toolErr := listLimit(args, configurationScansPageSize)
		if toolErr != nil {
			return toolErr, nil
		}

		list, err := r.ListWorkloadConfigurationScans(ctx, namespace, metav1.ListOptions{Limit: limit, Continue: continueToken})
		if err != nil {
			return readError(err)
		}
		var result listConfigurationScansResult
		result.Continue = list.Continue
		result.ConfigurationManifests.Manifests = make([]configurationScanEntry, 0, len(list.Items))
		for _, s := range list.Items {
			entry := configurationScanEntry{Namespace: s.Namespace, ManifestName: s.Name}
			if o.resourceLinks {
				entry.ResourceURI = fmt.Sprintf("kubescape://configuration-manifests/%s/%s", s.Namespace, s.Name)
			}
			result.ConfigurationManifests.Manifests = append(result.ConfigurationManifests.Manifests, entry)
		}
		if o.resourceLinks {
			result.AvailableTemplates = map[string]string{
				"configuration_manifest_details": "kubescape://configuration-manifests/{namespace}/{manifest_name}",
			}
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
		summary, toolErr := boolArg(args, "summary")
		if toolErr != nil {
			return toolErr, nil
		}
		controlID, toolErr := stringArg(args, "control_id")
		if toolErr != nil {
			return toolErr, nil
		}
		version, toolErr := stringArg(args, "object_version")
		if toolErr != nil {
			return toolErr, nil
		}
		var p page
		switch {
		case controlID != "" && (filter.active() || summary):
			return toolError(ErrCodeInvalidArgument, "control_id returns one control: leave out status, min_severity and summary",
				map[string]any{"argument": "control_id"}), nil
		case controlID != "" || summary:
			if p, toolErr = parsePage(args, defaultControlPageLimit, maxControlPageLimit); toolErr != nil {
				return toolErr, nil
			}
		case args["limit"] != nil || args["offset"] != nil:
			return toolError(ErrCodeInvalidArgument, "limit and offset need summary or control_id",
				map[string]any{"argument": "limit"}), nil
		}
		scan, err := r.GetWorkloadConfigurationScan(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		if toolErr := checkObjectVersion(version, "configuration scan", scan); toolErr != nil {
			return toolErr, nil
		}
		if controlID != "" {
			return controlPathsPage(scan, controlID, p)
		}
		scan.Spec.Controls = filterScannedControls(scan.Spec.Controls, filter)
		if summary {
			return jsonResult(scannedControlsSummary(scan, p))
		}
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

// scannedControlSummary is one control of a scan without its rules.
type scannedControlSummary struct {
	ControlID string `json:"control_id"`
	Name      string `json:"name"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
}

type scannedControlsSummaryResponse struct {
	Namespace     string `json:"namespace"`
	ManifestName  string `json:"manifest_name"`
	ObjectVersion string `json:"object_version,omitempty"`
	pageInfo
	Controls []scannedControlSummary `json:"controls"`
}

// scannedControlsSummary returns a page of the scan's controls without their
// rules, most severe first, then by control ID.
func scannedControlsSummary(scan *v1beta1.WorkloadConfigurationScan, p page) scannedControlsSummaryResponse {
	resp := scannedControlsSummaryResponse{
		Namespace:     scan.Namespace,
		ManifestName:  scan.Name,
		ObjectVersion: objectVersion(scan),
	}
	controls := make([]scannedControlSummary, 0, len(scan.Spec.Controls))
	for id, c := range scan.Spec.Controls {
		controls = append(controls, scannedControlSummary{
			ControlID: id,
			Name:      c.Name,
			Severity:  c.Severity.Severity,
			Status:    c.Status.Status,
		})
	}
	slices.SortFunc(controls, func(a, b scannedControlSummary) int {
		if sa, sb := parseSeverity(a.Severity), parseSeverity(b.Severity); sa != sb {
			return int(sb) - int(sa)
		}
		return strings.Compare(a.ControlID, b.ControlID)
	})
	resp.Controls, resp.pageInfo = paginate(controls, p)
	return resp
}

// controlRuleSummary is one rule of a control without its paths.
type controlRuleSummary struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// controlPathEntry is one path of a control: what failed and how to fix it,
// with the rule that reported it.
type controlPathEntry struct {
	Rule string `json:"rule"`
	v1beta1.RulePath
}

type controlPathsResponse struct {
	Namespace     string               `json:"namespace"`
	ManifestName  string               `json:"manifest_name"`
	ObjectVersion string               `json:"object_version,omitempty"`
	ControlID     string               `json:"control_id"`
	Name          string               `json:"name"`
	Severity      string               `json:"severity"`
	Status        string               `json:"status"`
	Rules         []controlRuleSummary `json:"rules"`
	pageInfo
	Paths []controlPathEntry `json:"paths"`
}

// controlPathsPage returns the control controlID of the scan with a page of
// its paths, in the order of its rules and then of each rule's paths. The ID
// is matched ignoring case; an ID not in the scan is RESOURCE_NOT_FOUND.
func controlPathsPage(scan *v1beta1.WorkloadConfigurationScan, controlID string, p page) (*mcp.CallToolResult, error) {
	id, c, ok := findScannedControl(scan.Spec.Controls, controlID)
	if !ok {
		return toolError(ErrCodeResourceNotFound,
			fmt.Sprintf("control %q is not in configuration scan %s/%s", controlID, scan.Namespace, scan.Name),
			map[string]any{"argument": "control_id"}), nil
	}
	resp := controlPathsResponse{
		Namespace:     scan.Namespace,
		ManifestName:  scan.Name,
		ObjectVersion: objectVersion(scan),
		ControlID:     id,
		Name:          c.Name,
		Severity:      c.Severity.Severity,
		Status:        c.Status.Status,
		Rules:         make([]controlRuleSummary, 0, len(c.Rules)),
	}
	var paths []controlPathEntry
	for _, rule := range c.Rules {
		resp.Rules = append(resp.Rules, controlRuleSummary{Name: rule.Name, Status: rule.Status.Status})
		for _, path := range rule.Paths {
			paths = append(paths, controlPathEntry{Rule: rule.Name, RulePath: path})
		}
	}
	resp.Paths, resp.pageInfo = paginate(paths, p)
	return jsonResult(resp)
}

func findScannedControl(controls map[string]v1beta1.ScannedControl, controlID string) (string, v1beta1.ScannedControl, bool) {
	if c, ok := controls[controlID]; ok {
		return controlID, c, true
	}
	for id, c := range controls {
		if strings.EqualFold(id, controlID) {
			return id, c, true
		}
	}
	return "", v1beta1.ScannedControl{}, false
}
