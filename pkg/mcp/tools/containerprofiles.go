package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// containerProfilesPageSize is the page size of list_container_profiles, as
// in kubescape mcpserver.
const containerProfilesPageSize = 100

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
	mcp.WithDescription("Get details of a specific container profile"),
	mcp.WithString("namespace",
		mcp.Description("Namespace of the profile, as returned by list_container_profiles (optional, defaults to the namespace in the endpoint's URL)"),
	),
	mcp.WithString("profile_name",
		mcp.Required(),
		mcp.Description("Name of the container profile to get details for (get this from the list_container_profiles tool)"),
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
		profile, err := r.GetContainerProfile(ctx, namespace, name)
		if err != nil {
			return readError(err)
		}
		return jsonResult(profile)
	}
}
