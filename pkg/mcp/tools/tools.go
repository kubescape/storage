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
