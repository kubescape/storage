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
func Register(s *server.MCPServer, r Reader, opts ...Option) {
	s.AddTools(Tools(r, opts...)...)
}

// Tools returns every tool with its handler, for a server that adds them
// itself, for example to wrap the handlers.
func Tools(r Reader, opts ...Option) []server.ServerTool {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	tools := []server.ServerTool{
		{Tool: listVulnerabilityManifestsTool, Handler: listVulnerabilityManifests(r, o)},
		{Tool: listVulnerabilitiesInManifestTool, Handler: listVulnerabilitiesInManifest(r)},
		{Tool: listVulnerabilityMatchesForCVETool, Handler: listVulnerabilityMatchesForCVE(r)},
		{Tool: listSBOMsTool, Handler: listSBOMs(r)},
		{Tool: listSBOMPackagesTool, Handler: listSBOMPackages(r)},
		{Tool: listConfigurationScansTool, Handler: listConfigurationScans(r, o)},
		{Tool: getConfigurationScanTool, Handler: getConfigurationScan(r)},
		{Tool: listContainerProfilesTool, Handler: listContainerProfiles(r, o)},
		{Tool: getContainerProfileTool, Handler: getContainerProfile(r)},
	}
	for i := range tools {
		if d, ok := o.namespaceDescriptions[tools[i].Tool.Name]; ok {
			tools[i].Tool = withNamespaceDescription(tools[i].Tool, d)
		}
	}
	return tools
}

// Option changes the tools for a server other than storage's endpoint, such as
// kubescape mcpserver.
type Option func(*options)

type options struct {
	resourceLinks         bool
	namespaceDescriptions map[string]string
}

// WithResourceLinks adds kubescape:// resource links to the answers of
// list_vulnerability_manifests, list_configuration_security_scan_manifests and
// list_container_profiles: a resource_uri for each item and the
// available_templates. Only for a server that serves those resource templates;
// storage's endpoint does not.
func WithResourceLinks() Option {
	return func(o *options) { o.resourceLinks = true }
}

// WithNamespaceDescription replaces the description of tool's namespace
// argument, for a server whose default namespace is not the one in the
// endpoint's URL.
func WithNamespaceDescription(tool, description string) Option {
	return func(o *options) {
		if o.namespaceDescriptions == nil {
			o.namespaceDescriptions = map[string]string{}
		}
		o.namespaceDescriptions[tool] = description
	}
}

// withNamespaceDescription returns a copy of tool with description on its
// namespace argument. The shared tool definition is left as it is.
func withNamespaceDescription(tool mcp.Tool, description string) mcp.Tool {
	ns, ok := tool.InputSchema.Properties["namespace"].(map[string]any)
	if !ok {
		return tool
	}
	props := make(map[string]any, len(tool.InputSchema.Properties))
	for k, v := range tool.InputSchema.Properties {
		props[k] = v
	}
	nsCopy := make(map[string]any, len(ns))
	for k, v := range ns {
		nsCopy[k] = v
	}
	nsCopy["description"] = description
	props["namespace"] = nsCopy
	tool.InputSchema.Properties = props
	return tool
}

// sizeHints say how to ask each tool for less when its answer is above the
// endpoint's size cap. Each one must be a way to a smaller answer from where
// the caller is, including the first page.
var sizeHints = map[string]string{
	"list_vulnerability_manifests":               "narrow it with level (image or workload), or lower limit and page with continue",
	"list_vulnerabilities_in_manifest":           "lower limit and page with offset, leave include_details off, or filter with min_severity or fixable_only",
	"list_vulnerability_matches_for_cve":         "lower limit and page with offset",
	"list_sboms":                                 "lower limit and page with continue",
	"list_sbom_packages":                         "lower limit and page with offset, or filter with name or package_type",
	"list_configuration_security_scan_manifests": "lower limit and page with continue",
	"get_configuration_security_scan_manifest":   "filter with status (for example failed) or min_severity, set summary to page through the controls without their rules, or set control_id to page through one control's paths with limit and offset",
	"list_container_profiles":                    "lower limit and page with continue",
	"get_container_profile":                      "set section to get one list of the profile, and page it with limit and offset",
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
