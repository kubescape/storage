package tools

import (
	"context"
	"fmt"
	"slices"
	"strings"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// containerProfilesPageSize is the page size of list_container_profiles, as
// in kubescape mcpserver.
const containerProfilesPageSize = 100

const (
	defaultProfileSectionLimit = 100
	maxProfileSectionLimit     = 500
)

// profileSections are the lists of a container profile that
// get_container_profile can page through, by their JSON names in the profile.
var profileSections = []string{"execs", "opens", "syscalls", "capabilities", "endpoints", "identifiedCallStacks", "ingress", "egress"}

var listContainerProfilesTool = readOnlyTool(
	"list_container_profiles",
	mcp.WithDescription("Discover available container profiles at workload level (this returns a list of profiles, not the profile results themselves, to get the profile results, use the get_container_profile tool)"),
	mcp.WithString("namespace",
		mcp.Description("Namespace to list (optional, defaults to the namespace in the endpoint's URL). A container's profile is stored in its workload's namespace"),
	),
	mcp.WithString("continue",
		mcp.Description("Optional Kubernetes continuation token for pagination"),
	),
)

var getContainerProfileTool = readOnlyTool(
	"get_container_profile",
	mcp.WithDescription("Get details of a specific container profile. A busy container's profile can be large: set section to page through one of its lists (what it executed, opened, called, and its network traffic) instead of getting the whole profile."),
	mcp.WithString("namespace",
		mcp.Description("Namespace of the profile, as returned by list_container_profiles (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("profile_name",
		mcp.Required(),
		mcp.Description("Name of the container profile to get details for (get this from the list_container_profiles tool)"),
	),
	mcp.WithString("section",
		mcp.Description("Return only this list of the profile, a page at a time (optional; by default the whole profile is returned)"),
		mcp.Enum(profileSections...),
	),
	mcp.WithNumber("limit",
		mcp.Description("With section: maximum number of entries to return (optional, default 100, maximum 500)"),
	),
	mcp.WithNumber("offset",
		mcp.Description("With section: number of entries to skip; pass the previous response's next_offset to get the next page (optional, default 0)"),
	),
)

// The response keeps the field names of kubescape mcpserver's tool. It leaves
// out the kubescape:// resource links, which this endpoint does not serve.
type containerProfileEntry struct {
	Namespace   string `json:"namespace"`
	ProfileName string `json:"profile_name"`
}

type listContainerProfilesResult struct {
	ContainerProfiles struct {
		Profiles []containerProfileEntry `json:"profiles"`
	} `json:"container_profiles"`
	Continue string `json:"continue,omitempty"`
}

func listContainerProfiles(r Reader) server.ToolHandlerFunc {
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

		list, err := r.ListContainerProfiles(ctx, namespace, metav1.ListOptions{Limit: containerProfilesPageSize, Continue: continueToken})
		if err != nil {
			return readError(err)
		}
		var result listContainerProfilesResult
		result.Continue = list.Continue
		result.ContainerProfiles.Profiles = make([]containerProfileEntry, 0, len(list.Items))
		for _, p := range list.Items {
			result.ContainerProfiles.Profiles = append(result.ContainerProfiles.Profiles,
				containerProfileEntry{Namespace: p.Namespace, ProfileName: p.Name})
		}
		return jsonResult(result)
	}
}

func getContainerProfile(r Reader) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		namespace, toolErr := stringArg(args, "namespace")
		if toolErr != nil {
			return toolErr, nil
		}
		if namespace == "" {
			namespace = defaultNamespace(ctx)
		}
		name, toolErr := requiredStringArg(args, "profile_name")
		if toolErr != nil {
			return toolErr, nil
		}
		section, toolErr := stringArg(args, "section")
		if toolErr != nil {
			return toolErr, nil
		}
		var p page
		if section != "" {
			if !slices.Contains(profileSections, section) {
				return toolError(ErrCodeInvalidArgument,
					fmt.Sprintf("section must be one of %s (got %q)", strings.Join(profileSections, ", "), section),
					map[string]any{"argument": "section", "supported_values": profileSections}), nil
			}
			if p, toolErr = parsePage(args, defaultProfileSectionLimit, maxProfileSectionLimit); toolErr != nil {
				return toolErr, nil
			}
		} else if args["limit"] != nil || args["offset"] != nil {
			return toolError(ErrCodeInvalidArgument, "limit and offset need a section",
				map[string]any{"argument": "section", "supported_values": profileSections}), nil
		}
		profile, err := r.GetContainerProfile(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		if section == "" {
			return jsonResult(profile)
		}
		return jsonResult(profileSectionPage(profile, section, p))
	}
}

type containerProfileSectionResponse struct {
	Namespace   string `json:"namespace"`
	ProfileName string `json:"profile_name"`
	// Status is the profile's kubescape.io/status annotation, for example
	// "too-large" for a profile above storage's maxContainerProfileSize.
	Status  string `json:"status,omitempty"`
	Section string `json:"section"`
	pageInfo
	Entries any `json:"entries"`
}

// profileSectionPage returns a page of one list of the profile, in the order
// it is stored in.
func profileSectionPage(profile *v1beta1.ContainerProfile, section string, p page) containerProfileSectionResponse {
	resp := containerProfileSectionResponse{
		Namespace:   profile.Namespace,
		ProfileName: profile.Name,
		Status:      profile.Annotations[helpersv1.StatusMetadataKey],
		Section:     section,
	}
	spec := profile.Spec
	switch section {
	case "execs":
		resp.Entries, resp.pageInfo = paginate(spec.Execs, p)
	case "opens":
		resp.Entries, resp.pageInfo = paginate(spec.Opens, p)
	case "syscalls":
		resp.Entries, resp.pageInfo = paginate(spec.Syscalls, p)
	case "capabilities":
		resp.Entries, resp.pageInfo = paginate(spec.Capabilities, p)
	case "endpoints":
		resp.Entries, resp.pageInfo = paginate(spec.Endpoints, p)
	case "identifiedCallStacks":
		resp.Entries, resp.pageInfo = paginate(spec.IdentifiedCallStacks, p)
	case "ingress":
		resp.Entries, resp.pageInfo = paginate(spec.Ingress, p)
	case "egress":
		resp.Entries, resp.pageInfo = paginate(spec.Egress, p)
	}
	return resp
}
