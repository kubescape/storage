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
	listLimitOption("profiles", containerProfilesPageSize),
)

var getContainerProfileTool = readOnlyTool(
	"get_container_profile",
	mcp.WithDescription("Get details of a specific container profile. A busy container's profile can be large: set section to page through one of its lists (what it executed, opened, called, and its network traffic) instead of getting the whole profile. A profile written by a user can describe several containers: the section answer lists them in containers, and container selects one."),
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
	mcp.WithString("container",
		mcp.Description("With section, for a profile that describes several containers: the container to read, by name, or as group/name (containers, initContainers or ephemeralContainers) when a name is in two groups (optional)"),
	),
	mcp.WithNumber("limit",
		mcp.Description("With section: maximum number of entries to return (optional, default 100, maximum 500)"),
	),
	mcp.WithNumber("offset",
		mcp.Description("With section: number of entries to skip; pass the previous response's next_offset to get the next page (optional, default 0)"),
	),
	objectVersionOption("profile"),
)

// The response keeps the field names of kubescape mcpserver's tool. The
// kubescape:// resource links are only added WithResourceLinks.
type containerProfileEntry struct {
	Namespace   string `json:"namespace"`
	ProfileName string `json:"profile_name"`
	ResourceURI string `json:"resource_uri,omitempty"`
}

type listContainerProfilesResult struct {
	ContainerProfiles struct {
		Profiles []containerProfileEntry `json:"profiles"`
	} `json:"container_profiles"`
	Continue           string            `json:"continue,omitempty"`
	AvailableTemplates map[string]string `json:"available_templates,omitempty"`
}

func listContainerProfiles(r Reader, o options) server.ToolHandlerFunc {
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

		limit, toolErr := listLimit(args, containerProfilesPageSize)
		if toolErr != nil {
			return toolErr, nil
		}

		list, err := r.ListContainerProfiles(ctx, namespace, metav1.ListOptions{Limit: limit, Continue: continueToken})
		if err != nil {
			return readError(err)
		}
		var result listContainerProfilesResult
		result.Continue = list.Continue
		result.ContainerProfiles.Profiles = make([]containerProfileEntry, 0, len(list.Items))
		for _, p := range list.Items {
			entry := containerProfileEntry{Namespace: p.Namespace, ProfileName: p.Name}
			if o.resourceLinks {
				entry.ResourceURI = fmt.Sprintf("kubescape://container-profiles/%s/%s", p.Namespace, p.Name)
			}
			result.ContainerProfiles.Profiles = append(result.ContainerProfiles.Profiles, entry)
		}
		if o.resourceLinks {
			result.AvailableTemplates = map[string]string{
				"container_profile_details": "kubescape://container-profiles/{namespace}/{profile_name}",
			}
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
		version, toolErr := stringArg(args, "object_version")
		if toolErr != nil {
			return toolErr, nil
		}
		container, toolErr := stringArg(args, "container")
		if toolErr != nil {
			return toolErr, nil
		}
		if container != "" && section == "" {
			return toolError(ErrCodeInvalidArgument, "container needs a section",
				map[string]any{"argument": "section", "supported_values": profileSections}), nil
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
		if toolErr := checkObjectVersion(version, "container profile", profile); toolErr != nil {
			return toolErr, nil
		}
		if section == "" {
			return jsonResult(profile)
		}
		resp, toolErr := profileSectionPage(profile, section, container, p)
		if toolErr != nil {
			return toolErr, nil
		}
		return jsonResult(resp)
	}
}

type containerProfileSectionResponse struct {
	Namespace     string `json:"namespace"`
	ProfileName   string `json:"profile_name"`
	ObjectVersion string `json:"object_version,omitempty"`
	// Status is the profile's kubescape.io/status annotation, for example
	// "too-large" for a profile above storage's maxContainerProfileSize.
	Status  string `json:"status,omitempty"`
	Section string `json:"section"`
	// Container is the container read, for a profile that describes several.
	Container string `json:"container,omitempty"`
	// Containers are the containers a user-authored profile describes, so
	// that the caller can select one with container.
	Containers []profileContainerRef `json:"containers,omitempty"`
	pageInfo
	Entries any `json:"entries"`
}

// profileContainerRef names one container of a user-authored profile.
type profileContainerRef struct {
	Name  string `json:"name"`
	Group string `json:"group"`
}

// profileContainerGroups are the groups of a user-authored profile, in the
// order a container name is looked up in.
var profileContainerGroups = []string{"containers", "initContainers", "ephemeralContainers"}

func containerGroup(spec *v1beta1.ContainerProfileSpec, group string) []v1beta1.ContainerProfileContainer {
	switch group {
	case "containers":
		return spec.Containers
	case "initContainers":
		return spec.InitContainers
	case "ephemeralContainers":
		return spec.EphemeralContainers
	}
	return nil
}

func profileContainerRefs(spec *v1beta1.ContainerProfileSpec) []profileContainerRef {
	var refs []profileContainerRef
	for _, group := range profileContainerGroups {
		for _, c := range containerGroup(spec, group) {
			refs = append(refs, profileContainerRef{Name: c.Name, Group: group})
		}
	}
	return refs
}

// findProfileContainer returns the container of a user-authored profile that
// container names: a name, or group/name. A name in no group, or in two, is
// INVALID_ARGUMENT with the values that would work.
func findProfileContainer(spec *v1beta1.ContainerProfileSpec, container string) (*v1beta1.ContainerProfileContainer, *mcp.CallToolResult) {
	refs := profileContainerRefs(spec)
	qualified := make([]string, len(refs))
	for i, ref := range refs {
		qualified[i] = ref.Group + "/" + ref.Name
	}
	if len(refs) == 0 {
		return nil, toolError(ErrCodeInvalidArgument, "this profile describes one container: leave out container",
			map[string]any{"argument": "container"})
	}
	group, name, qualifiedArg := strings.Cut(container, "/")
	if !qualifiedArg {
		group, name = "", container
	}
	var found []*v1beta1.ContainerProfileContainer
	for _, g := range profileContainerGroups {
		if group != "" && g != group {
			continue
		}
		list := containerGroup(spec, g)
		for i := range list {
			if list[i].Name == name {
				found = append(found, &list[i])
			}
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, toolError(ErrCodeInvalidArgument,
			fmt.Sprintf("container %q is not in this profile; it describes %s", container, strings.Join(qualified, ", ")),
			map[string]any{"argument": "container", "supported_values": qualified})
	default:
		return nil, toolError(ErrCodeInvalidArgument,
			fmt.Sprintf("container %q is in more than one group; use group/name: %s", container, strings.Join(qualified, ", ")),
			map[string]any{"argument": "container", "supported_values": qualified})
	}
}

// profileLists are the lists of one container's profile that a section
// reads: the profile itself for a learned profile, or one container of a
// user-authored one.
type profileLists struct {
	execs                []v1beta1.ExecCalls
	opens                []v1beta1.OpenCalls
	syscalls             []string
	capabilities         []string
	endpoints            []v1beta1.HTTPEndpoint
	identifiedCallStacks []v1beta1.IdentifiedCallStack
	ingress, egress      []v1beta1.NetworkNeighbor
}

// profileSectionPage returns a page of one list of the profile, or of one of
// its containers, in the order it is stored in.
func profileSectionPage(profile *v1beta1.ContainerProfile, section, container string, p page) (containerProfileSectionResponse, *mcp.CallToolResult) {
	resp := containerProfileSectionResponse{
		Namespace:     profile.Namespace,
		ProfileName:   profile.Name,
		ObjectVersion: objectVersion(profile),
		Status:        profile.Annotations[helpersv1.StatusMetadataKey],
		Section:       section,
	}
	spec := &profile.Spec
	resp.Containers = profileContainerRefs(spec)
	lists := profileLists{
		execs: spec.Execs, opens: spec.Opens, syscalls: spec.Syscalls, capabilities: spec.Capabilities,
		endpoints: spec.Endpoints, identifiedCallStacks: spec.IdentifiedCallStacks, ingress: spec.Ingress, egress: spec.Egress,
	}
	if container != "" {
		c, toolErr := findProfileContainer(spec, container)
		if toolErr != nil {
			return resp, toolErr
		}
		resp.Container = container
		lists = profileLists{
			execs: c.Execs, opens: c.Opens, syscalls: c.Syscalls, capabilities: c.Capabilities,
			endpoints: c.Endpoints, identifiedCallStacks: c.IdentifiedCallStacks, ingress: c.Ingress, egress: c.Egress,
		}
	}
	switch section {
	case "execs":
		resp.Entries, resp.pageInfo = paginate(lists.execs, p)
	case "opens":
		resp.Entries, resp.pageInfo = paginate(lists.opens, p)
	case "syscalls":
		resp.Entries, resp.pageInfo = paginate(lists.syscalls, p)
	case "capabilities":
		resp.Entries, resp.pageInfo = paginate(lists.capabilities, p)
	case "endpoints":
		resp.Entries, resp.pageInfo = paginate(lists.endpoints, p)
	case "identifiedCallStacks":
		resp.Entries, resp.pageInfo = paginate(lists.identifiedCallStacks, p)
	case "ingress":
		resp.Entries, resp.pageInfo = paginate(lists.ingress, p)
	case "egress":
		resp.Entries, resp.pageInfo = paginate(lists.egress, p)
	}
	return resp, nil
}
