package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	defaultVulnerabilityListLimit = 100
	maxVulnerabilityListLimit     = 500
)

var listVulnerabilitiesInManifestTool = readOnlyTool(
	"list_vulnerabilities_in_manifest",
	mcp.WithDescription("List the vulnerabilities in a given manifest, one entry per CVE and affected package, most severe first. Returns a page of results (see limit/offset) with the total count; long text fields are left out unless include_details is true."),
	mcp.WithString("namespace",
		mcp.Description("Namespace of the manifest (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("manifest_name",
		mcp.Required(),
		mcp.Description("Name of the manifest to list vulnerabilities from"),
	),
	mcp.WithString("min_severity",
		mcp.Description("Minimum severity to include: Critical, High, Medium, Low, Negligible (optional; by default every severity, including unclassified ones, is returned)"),
	),
	mcp.WithBoolean("fixable_only",
		mcp.Description("Only return vulnerabilities that have a fixed version (optional, default false)"),
	),
	mcp.WithBoolean("include_details",
		mcp.Description("Include description, CVSS, URLs, advisories and data source for each entry (optional, default false; makes the response much larger)"),
	),
	mcp.WithNumber("limit",
		mcp.Description("Maximum number of entries to return (optional, default 100, maximum 500)"),
	),
	mcp.WithNumber("offset",
		mcp.Description("Number of entries to skip; pass the previous response's next_offset to get the next page (optional, default 0)"),
	),
)

var listVulnerabilityMatchesForCVETool = readOnlyTool(
	"list_vulnerability_matches_for_cve",
	mcp.WithDescription("List all vulnerability matches for a given CVE in a given manifest"),
	mcp.WithString("namespace",
		mcp.Description("Namespace of the manifest (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("manifest_name",
		mcp.Required(),
		mcp.Description("Name of the manifest to list vulnerabilities from"),
	),
	mcp.WithString("cve_id",
		mcp.Required(),
		mcp.Description("ID of the CVE to list matches for"),
	),
)

func listVulnerabilitiesInManifest(r Reader) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		namespace, name, toolErr := manifestArgs(ctx, args)
		if toolErr != nil {
			return toolErr, nil
		}
		opts, toolErr := parseVulnerabilityListOptions(args)
		if toolErr != nil {
			return toolErr, nil
		}
		manifest, err := r.GetVulnerabilityManifest(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		return jsonResult(listVulnerabilities(manifest.Spec.Payload.Matches, opts))
	}
}

func listVulnerabilityMatchesForCVE(r Reader) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		namespace, name, toolErr := manifestArgs(ctx, args)
		if toolErr != nil {
			return toolErr, nil
		}
		cveID, toolErr := requiredStringArg(args, "cve_id")
		if toolErr != nil {
			return toolErr, nil
		}
		manifest, err := r.GetVulnerabilityManifest(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		matches := []v1beta1.Match{}
		for _, m := range manifest.Spec.Payload.Matches {
			if m.Vulnerability.ID == cveID {
				matches = append(matches, m)
			}
		}
		return jsonResult(matches)
	}
}

// manifestArgs returns the namespace (the path namespace when not given) and
// the name of the manifest a tool reads.
func manifestArgs(ctx context.Context, args map[string]any) (namespace, name string, toolErr *mcp.CallToolResult) {
	if namespace, toolErr = stringArg(args, "namespace"); toolErr != nil {
		return "", "", toolErr
	}
	if namespace == "" {
		namespace = defaultNamespace(ctx)
	}
	if name, toolErr = requiredStringArg(args, "manifest_name"); toolErr != nil {
		return "", "", toolErr
	}
	return namespace, name, nil
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

// severity is a vulnerability severity in order, so that it can be filtered by
// a threshold whatever the case of grype's severity string.
type severity int

const (
	severityUnknown severity = iota
	severityNegligible
	severityLow
	severityMedium
	severityHigh
	severityCritical
)

func (s severity) String() string {
	switch s {
	case severityCritical:
		return "Critical"
	case severityHigh:
		return "High"
	case severityMedium:
		return "Medium"
	case severityLow:
		return "Low"
	case severityNegligible:
		return "Negligible"
	default:
		return "Unknown"
	}
}

// parseSeverity reads a grype severity, ignoring case. Anything else, including
// an empty string, is severityUnknown, which sorts below every known level.
func parseSeverity(s string) severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return severityCritical
	case "high":
		return severityHigh
	case "medium":
		return severityMedium
	case "low":
		return severityLow
	case "negligible":
		return severityNegligible
	default:
		return severityUnknown
	}
}

// vulnerabilityEntry is one match of a CVE against a package. The package is
// part of the entry: the same CVE often matches several packages, and without
// it those entries are indistinguishable.
type vulnerabilityEntry struct {
	ID          string             `json:"id"`
	Severity    string             `json:"severity"`
	Package     string             `json:"package"`
	Version     string             `json:"version"`
	PackageType string             `json:"package_type"`
	PURL        string             `json:"purl,omitempty"`
	Fix         v1beta1.Fix        `json:"fix"`
	Description string             `json:"description,omitempty"`
	Cvss        []v1beta1.Cvss     `json:"cvss,omitempty"`
	URLs        []string           `json:"urls,omitempty"`
	Advisories  []v1beta1.Advisory `json:"advisories,omitempty"`
	DataSource  string             `json:"data_source,omitempty"`

	severity severity
}

type vulnerabilityListResponse struct {
	Total           int                  `json:"total"`
	Returned        int                  `json:"returned"`
	Truncated       bool                 `json:"truncated"`
	NextOffset      *int                 `json:"next_offset,omitempty"`
	Vulnerabilities []vulnerabilityEntry `json:"vulnerabilities"`
}

type vulnerabilityListOptions struct {
	minSeverity    severity // severityUnknown means no filter
	fixableOnly    bool
	includeDetails bool
	limit          int
	offset         int
}

// parseVulnerabilityListOptions reads the optional filter and paging arguments
// of list_vulnerabilities_in_manifest.
func parseVulnerabilityListOptions(args map[string]any) (vulnerabilityListOptions, *mcp.CallToolResult) {
	opts := vulnerabilityListOptions{limit: defaultVulnerabilityListLimit}

	minSeverity, toolErr := stringArg(args, "min_severity")
	if toolErr != nil {
		return opts, toolErr
	}
	if minSeverity != "" {
		opts.minSeverity = parseSeverity(minSeverity)
		if opts.minSeverity == severityUnknown {
			return opts, toolError(ErrCodeInvalidArgument,
				fmt.Sprintf("min_severity must be one of Critical, High, Medium, Low, Negligible (got %q)", minSeverity),
				map[string]any{"argument": "min_severity", "supported_values": []string{"Critical", "High", "Medium", "Low", "Negligible"}})
		}
	}

	if opts.fixableOnly, toolErr = boolArg(args, "fixable_only"); toolErr != nil {
		return opts, toolErr
	}
	if opts.includeDetails, toolErr = boolArg(args, "include_details"); toolErr != nil {
		return opts, toolErr
	}

	limit, set, toolErr := intArg(args, "limit")
	if toolErr != nil {
		return opts, toolErr
	}
	if set {
		if limit < 1 || limit > maxVulnerabilityListLimit {
			return opts, toolError(ErrCodeInvalidArgument,
				fmt.Sprintf("limit must be between 1 and %d", maxVulnerabilityListLimit),
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

// listVulnerabilities filters the manifest's matches and orders them by
// severity (most severe first, unclassified last), then CVE ID, package,
// version, package type and PURL, so that paging with offset is stable.
//
// Matches of the same CVE in the same package (PURL, name, version and type)
// are reported once, since nothing in the entry would tell them apart: one
// package installed in several places, or the same CVE reported from several
// vulnerability namespaces. Those namespaces can disagree on severity and on
// whether a fix exists, so the reported entry takes the most severe match and
// the best available fix among them, and the filters apply afterwards. The
// result does not depend on the order of the matches. Records without a PURL
// are not merged into packages with a known PURL.
//
// This is the same as kubescape mcpserver's list_vulnerabilities_in_manifest.
func listVulnerabilities(matches []v1beta1.Match, opts vulnerabilityListOptions) vulnerabilityListResponse {
	type candidate struct {
		match    v1beta1.Match
		severity severity
	}
	// better orders duplicates: more severe first, then by vulnerability
	// namespace and data source, which grype uses to tell them apart.
	better := func(a, b candidate) bool {
		if a.severity != b.severity {
			return a.severity > b.severity
		}
		if a.match.Vulnerability.Namespace != b.match.Vulnerability.Namespace {
			return a.match.Vulnerability.Namespace < b.match.Vulnerability.Namespace
		}
		return a.match.Vulnerability.DataSource < b.match.Vulnerability.DataSource
	}
	type group struct {
		best, bestFixed candidate
		hasFixed        bool
	}
	groups := make([]group, 0, len(matches))
	index := make(map[[5]string]int, len(matches))
	for _, m := range matches {
		c := candidate{match: m, severity: parseSeverity(m.Vulnerability.Severity)}
		fixed := hasFix(m.Vulnerability.Fix)
		// Name, version and type alone lose ecosystem namespaces, for example
		// org.first:parser and org.second:parser in Maven, so the PURL is part
		// of the key. Empty PURLs keep the name/version/type grouping.
		key := [5]string{m.Vulnerability.ID, m.Artifact.Name, m.Artifact.Version, string(m.Artifact.Type), m.Artifact.PURL}
		i, ok := index[key]
		if !ok {
			index[key] = len(groups)
			groups = append(groups, group{best: c, bestFixed: c, hasFixed: fixed})
			continue
		}
		g := &groups[i]
		if better(c, g.best) {
			g.best = c
		}
		if fixed && (!g.hasFixed || better(c, g.bestFixed)) {
			g.bestFixed, g.hasFixed = c, true
		}
	}

	entries := make([]vulnerabilityEntry, 0, len(groups))
	for _, g := range groups {
		c := g.best
		if g.hasFixed {
			c.match.Vulnerability.Fix = g.bestFixed.match.Vulnerability.Fix
		}
		m, v := c.match, c.match.Vulnerability
		if c.severity < opts.minSeverity {
			continue
		}
		if opts.fixableOnly && !hasFix(v.Fix) {
			continue
		}
		entry := vulnerabilityEntry{
			ID:          v.ID,
			Severity:    c.severity.String(),
			Package:     m.Artifact.Name,
			Version:     m.Artifact.Version,
			PackageType: string(m.Artifact.Type),
			PURL:        m.Artifact.PURL,
			Fix:         v.Fix,
			severity:    c.severity,
		}
		if entry.Fix.Versions == nil {
			entry.Fix.Versions = []string{}
		}
		if opts.includeDetails {
			entry.Description = v.Description
			entry.Cvss = v.Cvss
			entry.URLs = v.URLs
			entry.Advisories = v.Advisories
			entry.DataSource = v.DataSource
		}
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.severity != b.severity {
			return a.severity > b.severity
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		if a.PackageType != b.PackageType {
			return a.PackageType < b.PackageType
		}
		return a.PURL < b.PURL
	})

	resp := vulnerabilityListResponse{Total: len(entries), Vulnerabilities: []vulnerabilityEntry{}}
	if opts.offset < len(entries) {
		end := min(opts.offset+opts.limit, len(entries))
		resp.Vulnerabilities = entries[opts.offset:end]
		if end < len(entries) {
			resp.Truncated = true
			next := end
			resp.NextOffset = &next
		}
	}
	resp.Returned = len(resp.Vulnerabilities)
	return resp
}

// hasFix reports whether a fixed version is available.
func hasFix(fix v1beta1.Fix) bool {
	return fix.State == "fixed" && len(fix.Versions) > 0
}
