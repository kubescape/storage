package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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
	handlers := map[string]func(Reader) server.ToolHandlerFunc{
		"list_container_profiles": listContainerProfiles,
		"get_container_profile":   getContainerProfile,
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := handlers[tool](r)(WithDefaultNamespace(context.Background(), "kubescape"), req)
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
