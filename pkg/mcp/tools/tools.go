// Package tools holds the read-only MCP tools served on the mcpservers/mcp
// subresource (designs-and-proposals storage-mcp-endpoint.md). They read only
// through Reader, so they do not depend on how the data is fetched or how the
// caller's access is checked.
package tools

import (
	"context"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Reader is what the tools read from. A denied read returns a Forbidden
// error, as the Kubernetes API does.
type Reader interface {
	ListVulnerabilityManifests(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.VulnerabilityManifestList, error)
	GetVulnerabilityManifest(ctx context.Context, namespace, name string) (*v1beta1.VulnerabilityManifest, error)
	ListSBOMSyfts(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.SBOMSyftList, error)
	GetSBOMSyft(ctx context.Context, namespace, name string) (*v1beta1.SBOMSyft, error)
	ListWorkloadConfigurationScans(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.WorkloadConfigurationScanList, error)
	GetWorkloadConfigurationScan(ctx context.Context, namespace, name string) (*v1beta1.WorkloadConfigurationScan, error)
	ListContainerProfiles(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1beta1.ContainerProfileList, error)
	GetContainerProfile(ctx context.Context, namespace, name string) (*v1beta1.ContainerProfile, error)
}

// Register adds every tool to s.
func Register(s *server.MCPServer, r Reader) {
	s.AddTool(listVulnerabilityManifestsTool, listVulnerabilityManifests(r))
	s.AddTool(listVulnerabilitiesInManifestTool, listVulnerabilitiesInManifest(r))
	s.AddTool(listVulnerabilityMatchesForCVETool, listVulnerabilityMatchesForCVE(r))
	s.AddTool(listSBOMsTool, listSBOMs(r))
	s.AddTool(listSBOMPackagesTool, listSBOMPackages(r))
	s.AddTool(listConfigurationScansTool, listConfigurationScans(r))
	s.AddTool(getConfigurationScanTool, getConfigurationScan(r))
	s.AddTool(listContainerProfilesTool, listContainerProfiles(r))
	s.AddTool(getContainerProfileTool, getContainerProfile(r))
}

// sizeHints say how to ask each tool for less when its answer is above the
// endpoint's size cap. Tools that only list names page with continue and are
// not listed here.
var sizeHints = map[string]string{
	"list_vulnerability_manifests":             "narrow it with level (image or workload) or page with continue",
	"list_vulnerabilities_in_manifest":         "lower limit and page with offset, leave include_details off, or filter with min_severity or fixable_only",
	"list_vulnerability_matches_for_cve":       "lower limit and page with offset",
	"list_sbom_packages":                       "lower limit and page with offset, or filter with name or package_type",
	"get_configuration_security_scan_manifest": "filter with status (for example failed) or min_severity",
	"get_container_profile":                    "set section to get one list of the profile, and page it with limit and offset",
}

// SizeHint says how to ask tool for less, for an answer above the endpoint's
// size cap.
func SizeHint(tool string) string {
	if h, ok := sizeHints[tool]; ok {
		return h
	}
	return "ask for less with the tool's paging or filters"
}

// readOnlyTool is mcp.NewTool with the hints of a tool that only reads
// Kubescape's own data; mcp-go's defaults mark a tool destructive and not
// read-only.
func readOnlyTool(name string, opts ...mcp.ToolOption) mcp.Tool {
	return mcp.NewTool(name, append([]mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(false),
	}, opts...)...)
}

type defaultNamespaceKey struct{}

// WithDefaultNamespace sets the namespace a tool uses when the call gives none.
func WithDefaultNamespace(ctx context.Context, namespace string) context.Context {
	return context.WithValue(ctx, defaultNamespaceKey{}, namespace)
}

func defaultNamespace(ctx context.Context) string {
	ns, _ := ctx.Value(defaultNamespaceKey{}).(string)
	return ns
}
