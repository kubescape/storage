package tools

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// sbomsPageSize is the page size of list_sboms, as in kubescape mcpserver.
	sbomsPageSize               = 100
	defaultSBOMPackageListLimit = 100
	maxSBOMPackageListLimit     = 500
)

var listSBOMsTool = readOnlyTool(
	"list_sboms",
	mcp.WithDescription("Discover the image SBOMs stored in the cluster, without their content. Use list_sbom_packages to read the packages of one. Kubescape stores them in its own namespace (usually kubescape)."),
	mcp.WithString("namespace",
		mcp.Description("Namespace to list (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("continue",
		mcp.Description("Optional Kubernetes continuation token for pagination"),
	),
)

var listSBOMPackagesTool = readOnlyTool(
	"list_sbom_packages",
	mcp.WithDescription("List the packages in an image SBOM (name, version, type, purl, licenses), sorted by name. Returns a page of results (see limit/offset) with the total count. Use name or package_type to look for specific packages."),
	mcp.WithString("namespace",
		mcp.Description("Namespace of the SBOM (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("sbom_name",
		mcp.Required(),
		mcp.Description("Name of the SBOM, as returned by list_sboms"),
	),
	mcp.WithString("name",
		mcp.Description("Only packages whose name contains this text, case-insensitive (optional)"),
	),
	mcp.WithString("package_type",
		mcp.Description("Only packages of this type, for example deb, rpm, apk, npm, python, go-module, java-archive (optional)"),
	),
	mcp.WithNumber("limit",
		mcp.Description("Maximum number of packages to return (optional, default 100, maximum 500)"),
	),
	mcp.WithNumber("offset",
		mcp.Description("Number of packages to skip; pass the previous response's next_offset to get the next page (optional, default 0)"),
	),
)

// sbomEntry describes one stored SBOM without its content. The field names
// are those of kubescape mcpserver's list_sboms.
type sbomEntry struct {
	Namespace string `json:"namespace"`
	SBOMName  string `json:"sbom_name"`
	ImageID   string `json:"image_id,omitempty"`
	ImageTag  string `json:"image_tag,omitempty"`
	Status    string `json:"status,omitempty"`
}

type sbomListResponse struct {
	SBOMs    []sbomEntry `json:"sboms"`
	Continue string      `json:"continue,omitempty"`
}

func listSBOMs(r Reader) server.ToolHandlerFunc {
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

		list, err := r.ListSBOMSyfts(ctx, namespace, metav1.ListOptions{Limit: sbomsPageSize, Continue: continueToken})
		if err != nil {
			return readError(err)
		}
		result := sbomListResponse{SBOMs: make([]sbomEntry, 0, len(list.Items)), Continue: list.Continue}
		for _, s := range list.Items {
			result.SBOMs = append(result.SBOMs, sbomEntry{
				Namespace: s.Namespace,
				SBOMName:  s.Name,
				ImageID:   s.Annotations[helpersv1.ImageIDMetadataKey],
				ImageTag:  s.Annotations[helpersv1.ImageTagMetadataKey],
				Status:    s.Annotations[helpersv1.StatusMetadataKey],
			})
		}
		return jsonResult(result)
	}
}

func listSBOMPackages(r Reader) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		namespace, toolErr := stringArg(args, "namespace")
		if toolErr != nil {
			return toolErr, nil
		}
		if namespace == "" {
			namespace = defaultNamespace(ctx)
		}
		name, toolErr := requiredStringArg(args, "sbom_name")
		if toolErr != nil {
			return toolErr, nil
		}
		opts, toolErr := parseSBOMPackageListOptions(args)
		if toolErr != nil {
			return toolErr, nil
		}
		sbom, err := r.GetSBOMSyft(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		return jsonResult(filterSBOMPackages(sbom, opts))
	}
}

// sbomPackageEntry is one package of an SBOM. Locations, CPEs and the
// cataloger metadata are left out: they make up most of the document and an
// agent rarely needs them to answer "is this package in the image".
type sbomPackageEntry struct {
	Name     string   `json:"name"`
	Version  string   `json:"version"`
	Type     string   `json:"type"`
	Language string   `json:"language,omitempty"`
	PURL     string   `json:"purl,omitempty"`
	Licenses []string `json:"licenses"`
}

type sbomPackageListResponse struct {
	SBOMName string `json:"sbom_name"`
	// Status is the SBOM's kubescape.io/status annotation. An SBOM that was
	// too large to store has a status and no packages, which is not the same
	// as an image with no packages.
	Status     string             `json:"status,omitempty"`
	Distro     string             `json:"distro,omitempty"`
	Total      int                `json:"total"`
	Returned   int                `json:"returned"`
	Truncated  bool               `json:"truncated"`
	NextOffset *int               `json:"next_offset,omitempty"`
	Packages   []sbomPackageEntry `json:"packages"`
}

type sbomPackageListOptions struct {
	name        string // case-insensitive substring; empty means no filter
	packageType string // exact, case-insensitive; empty means no filter
	limit       int
	offset      int
}

// parseSBOMPackageListOptions reads the optional filter and paging arguments
// of list_sbom_packages.
func parseSBOMPackageListOptions(args map[string]any) (sbomPackageListOptions, *mcp.CallToolResult) {
	opts := sbomPackageListOptions{limit: defaultSBOMPackageListLimit}

	name, toolErr := stringArg(args, "name")
	if toolErr != nil {
		return opts, toolErr
	}
	opts.name = strings.ToLower(strings.TrimSpace(name))

	packageType, toolErr := stringArg(args, "package_type")
	if toolErr != nil {
		return opts, toolErr
	}
	opts.packageType = strings.TrimSpace(packageType)

	limit, set, toolErr := intArg(args, "limit")
	if toolErr != nil {
		return opts, toolErr
	}
	if set {
		if limit < 1 || limit > maxSBOMPackageListLimit {
			return opts, toolError(ErrCodeInvalidArgument,
				fmt.Sprintf("limit must be between 1 and %d", maxSBOMPackageListLimit),
				map[string]any{"argument": "limit"})
		}
		opts.limit = limit
	}

	offset, _, toolErr := intArg(args, "offset")
	if toolErr != nil {
		return opts, toolErr
	}
	if offset < 0 {
		return opts, toolError(ErrCodeInvalidArgument, "offset must not be negative", map[string]any{"argument": "offset"})
	}
	opts.offset = offset
	return opts, nil
}

// filterSBOMPackages filters the SBOM's packages and orders them by name,
// version, type and purl, so that paging with offset is stable. The same
// package found in several places is listed once, with the licenses of all
// its copies. A package is identified by its purl, or by name, version and
// type when it has none, so two Java packages with the same name and version
// but different group IDs are both listed. Copies are grouped before the
// filters run, and a package is listed when any of its copies matches, so a
// filter never drops licenses that only another copy carries. The name and
// type shown are those of the first matching copy.
func filterSBOMPackages(sbom *v1beta1.SBOMSyft, opts sbomPackageListOptions) sbomPackageListResponse {
	doc := sbom.Spec.Syft
	seen := map[[4]string]int{}
	all := []sbomPackageEntry{}
	matched := []bool{}
	for _, p := range doc.Artifacts {
		key := [4]string{p.PURL}
		if p.PURL == "" {
			key = [4]string{"", p.Name, p.Version, p.Type}
		}
		i, ok := seen[key]
		if !ok {
			i = len(all)
			seen[key] = i
			matched = append(matched, false)
			all = append(all, sbomPackageEntry{
				Name:     p.Name,
				Version:  p.Version,
				Type:     p.Type,
				Language: p.Language,
				PURL:     p.PURL,
				Licenses: []string{},
			})
		}
		if (opts.name == "" || strings.Contains(strings.ToLower(p.Name), opts.name)) &&
			(opts.packageType == "" || strings.EqualFold(p.Type, opts.packageType)) && !matched[i] {
			matched[i] = true
			all[i].Name, all[i].Version, all[i].Type, all[i].Language = p.Name, p.Version, p.Type, p.Language
		}
		for _, l := range p.Licenses {
			value := l.SPDXExpression
			if value == "" {
				value = l.Value
			}
			if value != "" && !slices.Contains(all[i].Licenses, value) {
				all[i].Licenses = append(all[i].Licenses, value)
			}
		}
	}
	entries := []sbomPackageEntry{}
	for i, e := range all {
		if matched[i] {
			entries = append(entries, e)
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.PURL < b.PURL
	})

	resp := sbomPackageListResponse{
		SBOMName: sbom.Name,
		Status:   sbom.Annotations[helpersv1.StatusMetadataKey],
		Distro:   strings.TrimSpace(doc.Distro.ID + " " + doc.Distro.VersionID),
		Total:    len(entries),
		Packages: []sbomPackageEntry{},
	}
	if opts.offset < len(entries) {
		end := min(opts.offset+opts.limit, len(entries))
		resp.Packages = entries[opts.offset:end]
		if end < len(entries) {
			resp.Truncated = true
			next := end
			resp.NextOffset = &next
		}
	}
	resp.Returned = len(resp.Packages)
	return resp
}
