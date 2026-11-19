package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func profileFixture() v1beta1.ContainerProfile {
	return v1beta1.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Name: "replicaset-web-7d4b9c-nginx", Namespace: "kubescape", Labels: map[string]string{"kubescape.io/workload-kind": "Deployment"}},
		Spec: v1beta1.ContainerProfileSpec{
			Execs:    []v1beta1.ExecCalls{{Path: "/usr/sbin/nginx", Args: []string{"-g", "daemon off;"}}},
			Syscalls: []string{"accept4", "epoll_wait"},
		},
	}
}

func profileReader() *memReader {
	r := newMemReader()
	r.profiles = []v1beta1.ContainerProfile{
		profileFixture(),
		{ObjectMeta: metav1.ObjectMeta{Name: "replicaset-api-5f6c8d-api", Namespace: "kubescape"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "replicaset-shop-9a8b7c-shop", Namespace: "default"}},
	}
	return r
}

// callProfileTool calls tool on r with the path namespace kubescape.
func callProfileTool(t *testing.T, r Reader, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := toolHandler(t, r, tool)(WithDefaultNamespace(context.Background(), "kubescape"), req)
	require.NoError(t, err)
	return res
}

func TestGetContainerProfileReturnsTheProfile(t *testing.T) {
	res := callProfileTool(t, profileReader(), "get_container_profile", map[string]any{"profile_name": "replicaset-web-7d4b9c-nginx"})
	require.False(t, res.IsError, resultText(t, res))
	var got v1beta1.ContainerProfile
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &got))
	want := profileFixture()
	assert.Equal(t, want.ObjectMeta, got.ObjectMeta)
	assert.Equal(t, want.Spec, got.Spec)
}

func TestGetContainerProfileErrors(t *testing.T) {
	r := profileReader()
	for _, tc := range []struct {
		name string
		args map[string]any
		code ErrorCode
		want string
	}{
		{"no profile_name", map[string]any{}, ErrCodeInvalidArgument, "profile_name is required"},
		{"profile_name type", map[string]any{"profile_name": 42}, ErrCodeInvalidArgument, "profile_name must be a string"},
		{"namespace type", map[string]any{"namespace": true, "profile_name": "x"}, ErrCodeInvalidArgument, "namespace must be a string"},
		{"missing", map[string]any{"namespace": "default", "profile_name": "replicaset-web-7d4b9c-nginx"}, ErrCodeResourceNotFound, "not found"},
		{"forbidden", map[string]any{"namespace": restrictedNamespace, "profile_name": "x"}, ErrCodeRBACDenied, "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := callProfileTool(t, r, "get_container_profile", tc.args)
			assert.Equal(t, tc.code, toolErrorOf(t, res).Code)
			assert.Contains(t, resultText(t, res), tc.want)
		})
	}
}

func bigProfile() v1beta1.ContainerProfile {
	p := profileFixture()
	p.Annotations = map[string]string{"kubescape.io/status": "too-large"}
	for i := range 5 {
		p.Spec.Opens = append(p.Spec.Opens, v1beta1.OpenCalls{Path: fmt.Sprintf("/etc/file-%d", i), Flags: []string{"O_RDONLY"}})
	}
	return p
}

func callProfileSection(t *testing.T, args map[string]any) containerProfileSectionResponse {
	t.Helper()
	r := newMemReader()
	r.profiles = []v1beta1.ContainerProfile{bigProfile()}
	args["profile_name"] = "replicaset-web-7d4b9c-nginx"
	res := callProfileTool(t, r, "get_container_profile", args)
	require.False(t, res.IsError, resultText(t, res))
	var resp containerProfileSectionResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &resp))
	return resp
}

func TestGetContainerProfileSection(t *testing.T) {
	resp := callProfileSection(t, map[string]any{"section": "opens", "limit": 2.0})
	assert.Equal(t, "kubescape", resp.Namespace)
	assert.Equal(t, "replicaset-web-7d4b9c-nginx", resp.ProfileName)
	assert.Equal(t, "too-large", resp.Status)
	assert.Equal(t, "opens", resp.Section)
	assert.Equal(t, 5, resp.Total)
	assert.Equal(t, 2, resp.Returned)
	assert.True(t, resp.Truncated)
	require.NotNil(t, resp.NextOffset)
	assert.Equal(t, 2, *resp.NextOffset)
	b, err := json.Marshal(resp.Entries)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"path":"/etc/file-0","flags":["O_RDONLY"]},{"path":"/etc/file-1","flags":["O_RDONLY"]}]`, string(b))

	// Paging through a section returns every entry once, in stored order.
	var paths []string
	for offset := 0; ; {
		resp := callProfileSection(t, map[string]any{"section": "opens", "limit": 2.0, "offset": float64(offset)})
		b, err := json.Marshal(resp.Entries)
		require.NoError(t, err)
		var opens []v1beta1.OpenCalls
		require.NoError(t, json.Unmarshal(b, &opens))
		for _, o := range opens {
			paths = append(paths, o.Path)
		}
		if resp.NextOffset == nil {
			break
		}
		offset = *resp.NextOffset
	}
	assert.Equal(t, []string{"/etc/file-0", "/etc/file-1", "/etc/file-2", "/etc/file-3", "/etc/file-4"}, paths)
}

func TestGetContainerProfileEverySection(t *testing.T) {
	want := map[string]int{"execs": 1, "opens": 5, "syscalls": 2}
	for _, section := range profileSections {
		resp := callProfileSection(t, map[string]any{"section": section})
		assert.Equal(t, section, resp.Section)
		assert.Equal(t, want[section], resp.Total, section)
		assert.NotNil(t, resp.Entries, "%s: an empty section is [], not null", section)
	}
}

func TestGetContainerProfileSectionErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"unknown section", map[string]any{"section": "files"}, "section must be one of execs, opens"},
		{"section type", map[string]any{"section": 1}, "section must be a string"},
		{"limit without section", map[string]any{"limit": 10.0}, "limit and offset need a section"},
		{"offset without section", map[string]any{"offset": 10.0}, "limit and offset need a section"},
		{"limit too big", map[string]any{"section": "opens", "limit": 501.0}, "limit must be between 1 and 500"},
		{"negative offset", map[string]any{"section": "opens", "offset": -1.0}, "offset must not be negative"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.args["profile_name"] = "replicaset-web-7d4b9c-nginx"
			te := toolErrorOf(t, callProfileTool(t, profileReader(), "get_container_profile", tc.args))
			assert.Equal(t, ErrCodeInvalidArgument, te.Code)
			assert.Contains(t, te.Message, tc.want)
		})
	}
}

func TestListContainerProfiles(t *testing.T) {
	r := profileReader()
	res := callProfileTool(t, r, "list_container_profiles", map[string]any{})
	require.False(t, res.IsError, resultText(t, res))
	assert.JSONEq(t, `{"container_profiles":{"profiles":[
		{"namespace":"kubescape","profile_name":"replicaset-web-7d4b9c-nginx"},
		{"namespace":"kubescape","profile_name":"replicaset-api-5f6c8d-api"}
	]}}`, resultText(t, res), "the namespace in the endpoint's URL by default")

	res = callProfileTool(t, r, "list_container_profiles", map[string]any{"namespace": "default"})
	assert.JSONEq(t, `{"container_profiles":{"profiles":[{"namespace":"default","profile_name":"replicaset-shop-9a8b7c-shop"}]}}`, resultText(t, res))

	res = callProfileTool(t, r, "list_container_profiles", map[string]any{"namespace": restrictedNamespace})
	assert.Equal(t, ErrCodeRBACDenied, toolErrorOf(t, res).Code)

	res = callProfileTool(t, r, "list_container_profiles", map[string]any{"continue": 3})
	assert.Equal(t, ErrCodeInvalidArgument, toolErrorOf(t, res).Code)
}

func TestListContainerProfilesContinue(t *testing.T) {
	r := &fakeReader{profiles: &v1beta1.ContainerProfileList{
		ListMeta: metav1.ListMeta{Continue: "next-page"},
		Items:    []v1beta1.ContainerProfile{{ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "replicaset-shop-9a8b7c-shop"}}},
	}}
	res := callProfileTool(t, r, "list_container_profiles", map[string]any{"namespace": "default", "continue": "this-page"})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, "default", r.namespace)
	assert.Equal(t, metav1.ListOptions{Limit: 100, Continue: "this-page"}, r.opts)
	assert.JSONEq(t, `{"container_profiles":{"profiles":[{"namespace":"default","profile_name":"replicaset-shop-9a8b7c-shop"}]},"continue":"next-page"}`, resultText(t, res))
}

// A user-authored profile can describe several containers in its containers,
// initContainers and ephemeralContainers groups, and then its top-level lists
// are empty (storage#451: section syscalls gave total 0). The section answer
// names the containers, and container selects one.
func TestContainerProfileSectionOfAMultiContainerProfile(t *testing.T) {
	authored := v1beta1.ContainerProfile{
		ObjectMeta: metav1.ObjectMeta{Namespace: "kubescape", Name: "authored"},
		Spec: v1beta1.ContainerProfileSpec{
			Containers: []v1beta1.ContainerProfileContainer{
				{Name: "app", Syscalls: []string{"read", "write"}},
				{Name: "debug", Syscalls: []string{"ptrace"}},
			},
			InitContainers:      []v1beta1.ContainerProfileContainer{{Name: "setup", Syscalls: []string{"mount"}}},
			EphemeralContainers: []v1beta1.ContainerProfileContainer{{Name: "debug", Syscalls: []string{"kill"}}},
		},
	}
	r := &memReader{profiles: []v1beta1.ContainerProfile{authored, profileFixture()}}
	call := func(args map[string]any) *mcp.CallToolResult {
		t.Helper()
		args["namespace"] = "kubescape"
		return callProfileTool(t, r, "get_container_profile", args)
	}
	section := func(args map[string]any) containerProfileSectionResponse {
		t.Helper()
		res := call(args)
		require.False(t, res.IsError, resultText(t, res))
		var resp containerProfileSectionResponse
		require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &resp))
		return resp
	}
	entries := func(resp containerProfileSectionResponse) []string {
		var out []string
		for _, e := range resp.Entries.([]any) {
			out = append(out, e.(string))
		}
		return out
	}

	// Without container: the profile's own (empty) list, and its containers.
	resp := section(map[string]any{"profile_name": "authored", "section": "syscalls"})
	assert.Equal(t, 0, resp.Total)
	assert.Equal(t, []profileContainerRef{
		{Name: "app", Group: "containers"}, {Name: "debug", Group: "containers"},
		{Name: "setup", Group: "initContainers"}, {Name: "debug", Group: "ephemeralContainers"},
	}, resp.Containers)

	// Every container's syscalls can be read: 5 in all.
	assert.Equal(t, []string{"read", "write"}, entries(section(map[string]any{"profile_name": "authored", "section": "syscalls", "container": "app"})))
	assert.Equal(t, []string{"mount"}, entries(section(map[string]any{"profile_name": "authored", "section": "syscalls", "container": "setup"})))
	assert.Equal(t, []string{"ptrace"}, entries(section(map[string]any{"profile_name": "authored", "section": "syscalls", "container": "containers/debug"})))
	resp = section(map[string]any{"profile_name": "authored", "section": "syscalls", "container": "ephemeralContainers/debug", "limit": float64(1)})
	assert.Equal(t, []string{"kill"}, entries(resp))
	assert.Equal(t, "ephemeralContainers/debug", resp.Container)

	// A name in two groups needs its group; an unknown one lists what works.
	for _, c := range []string{"debug", "sidecar", "initContainers/app", "pods/app"} {
		te := toolErrorOf(t, call(map[string]any{"profile_name": "authored", "section": "syscalls", "container": c}))
		assert.Equal(t, ErrCodeInvalidArgument, te.Code, c)
		assert.Equal(t, []any{"containers/app", "containers/debug", "initContainers/setup", "ephemeralContainers/debug"}, te.Details["supported_values"], c)
	}

	// container needs a section, and a learned profile has no containers.
	assert.Equal(t, ErrCodeInvalidArgument, toolErrorOf(t, call(map[string]any{"profile_name": "authored", "container": "app"})).Code)
	learned := profileFixture()
	te := toolErrorOf(t, call(map[string]any{"profile_name": learned.Name, "section": "syscalls", "container": "app"}))
	assert.Equal(t, ErrCodeInvalidArgument, te.Code)
	assert.Contains(t, te.Message, "describes one container")
	assert.Empty(t, section(map[string]any{"profile_name": learned.Name, "section": "syscalls"}).Containers)
}
