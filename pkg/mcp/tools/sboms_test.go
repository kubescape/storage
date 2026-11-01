package tools

import (
	"context"
	"encoding/json"
	"testing"

	helpersv1 "github.com/kubescape/k8s-interface/instanceidhandler/v1/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/v1beta1"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func sbomPackage(name, version, pkgType string, licenses ...v1beta1.License) v1beta1.SyftPackage {
	return v1beta1.SyftPackage{PackageBasicData: v1beta1.PackageBasicData{
		ID:       name + "@" + version,
		Name:     name,
		Version:  version,
		Type:     pkgType,
		PURL:     "pkg:" + pkgType + "/" + name + "@" + version,
		Licenses: licenses,
		Locations: []v1beta1.Location{{LocationData: v1beta1.LocationData{
			Coordinates: v1beta1.Coordinates{RealPath: "/var/lib/dpkg/status"},
		}}},
	}}
}

func sbomReader() *memReader {
	r := newMemReader()
	r.sboms = []v1beta1.SBOMSyft{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "nginx-1.21", Namespace: "kubescape", Annotations: map[string]string{
				helpersv1.ImageIDMetadataKey:  "docker.io/library/nginx@sha256:2834",
				helpersv1.ImageTagMetadataKey: "nginx:1.21",
				helpersv1.StatusMetadataKey:   helpersv1.Learning,
			}},
			Spec: v1beta1.SBOMSyftSpec{Syft: v1beta1.SyftDocument{
				Distro: v1beta1.LinuxRelease{ID: "debian", VersionID: "11"},
				Artifacts: []v1beta1.SyftPackage{
					sbomPackage("zlib1g", "1:1.2.11", "deb", v1beta1.License{Value: "Zlib", SPDXExpression: "Zlib"}),
					sbomPackage("openssl", "1.1.1n", "deb",
						v1beta1.License{Value: "OpenSSL"},
						v1beta1.License{Value: "OpenSSL"}),
					sbomPackage("libssl1.1", "1.1.1n", "deb"),
					// Same package found in a second place: listed once.
					sbomPackage("libssl1.1", "1.1.1n", "deb"),
					sbomPackage("express", "4.17.1", "npm", v1beta1.License{Value: "MIT", SPDXExpression: "MIT"}),
				},
			}},
		},
		{ObjectMeta: metav1.ObjectMeta{Name: "scratch", Namespace: "kubescape"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "huge", Namespace: "kubescape", Annotations: map[string]string{
			helpersv1.StatusMetadataKey: helpersv1.TooLarge,
		}}},
		{ObjectMeta: metav1.ObjectMeta{Name: "redis-7", Namespace: "other"}},
	}
	return r
}

// callSBOMTool calls tool on r with the path namespace kubescape.
func callSBOMTool(t *testing.T, r Reader, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	handlers := map[string]func(Reader) server.ToolHandlerFunc{
		"list_sboms":         listSBOMs,
		"list_sbom_packages": listSBOMPackages,
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = tool
	req.Params.Arguments = args
	res, err := handlers[tool](r)(WithDefaultNamespace(context.Background(), "kubescape"), req)
	require.NoError(t, err)
	return res
}

func callSBOMPackages(t *testing.T, r Reader, args map[string]any) sbomPackageListResponse {
	t.Helper()
	res := callSBOMTool(t, r, "list_sbom_packages", args)
	require.False(t, res.IsError, resultText(t, res))
	var resp sbomPackageListResponse
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &resp))
	return resp
}

func packageNames(resp sbomPackageListResponse) []string {
	names := []string{}
	for _, p := range resp.Packages {
		names = append(names, p.Name)
	}
	return names
}

func TestListSBOMPackagesDefaultsAndOrder(t *testing.T) {
	resp := callSBOMPackages(t, sbomReader(), map[string]any{"sbom_name": "nginx-1.21"})
	assert.Equal(t, "nginx-1.21", resp.SBOMName)
	assert.Equal(t, helpersv1.Learning, resp.Status)
	assert.Equal(t, "debian 11", resp.Distro)
	assert.Equal(t, 4, resp.Total, "the duplicate libssl1.1 is listed once")
	assert.Equal(t, 4, resp.Returned)
	assert.False(t, resp.Truncated)
	assert.Nil(t, resp.NextOffset)
	assert.Equal(t, []string{"express", "libssl1.1", "openssl", "zlib1g"}, packageNames(resp))

	assert.Equal(t, sbomPackageEntry{Name: "zlib1g", Version: "1:1.2.11", Type: "deb", PURL: "pkg:deb/zlib1g@1:1.2.11", Licenses: []string{"Zlib"}}, resp.Packages[3])
	assert.Equal(t, []string{"OpenSSL"}, resp.Packages[2].Licenses, "repeated licenses are listed once; the value is used when there is no SPDX expression")
	assert.Equal(t, []string{}, resp.Packages[1].Licenses)
}

func TestListSBOMPackagesLeavesOutLocations(t *testing.T) {
	text := resultText(t, callSBOMTool(t, sbomReader(), "list_sbom_packages", map[string]any{"sbom_name": "nginx-1.21"}))
	assert.NotContains(t, text, "/var/lib/dpkg/status")
	assert.NotContains(t, text, "locations")
}

func TestListSBOMPackagesFilters(t *testing.T) {
	r := sbomReader()
	assert.Equal(t, []string{"libssl1.1", "openssl"}, packageNames(callSBOMPackages(t, r, map[string]any{"sbom_name": "nginx-1.21", "name": "SSL"})),
		"name matches a case-insensitive substring")
	assert.Equal(t, []string{"express"}, packageNames(callSBOMPackages(t, r, map[string]any{"sbom_name": "nginx-1.21", "package_type": "NPM"})))

	resp := callSBOMPackages(t, r, map[string]any{"sbom_name": "nginx-1.21", "name": "ssl", "package_type": "npm"})
	assert.Equal(t, 0, resp.Total)
	assert.Equal(t, []sbomPackageEntry{}, resp.Packages)
}

func TestListSBOMPackagesPaging(t *testing.T) {
	r := sbomReader()
	first := callSBOMPackages(t, r, map[string]any{"sbom_name": "nginx-1.21", "limit": float64(3)})
	assert.Equal(t, []string{"express", "libssl1.1", "openssl"}, packageNames(first))
	assert.Equal(t, 4, first.Total)
	assert.Equal(t, 3, first.Returned)
	assert.True(t, first.Truncated)
	require.NotNil(t, first.NextOffset)
	assert.Equal(t, 3, *first.NextOffset)

	second := callSBOMPackages(t, r, map[string]any{"sbom_name": "nginx-1.21", "limit": float64(3), "offset": float64(*first.NextOffset)})
	assert.Equal(t, []string{"zlib1g"}, packageNames(second))
	assert.False(t, second.Truncated)
	assert.Nil(t, second.NextOffset)

	past := callSBOMPackages(t, r, map[string]any{"sbom_name": "nginx-1.21", "offset": float64(10)})
	assert.Equal(t, 4, past.Total)
	assert.Equal(t, []sbomPackageEntry{}, past.Packages)
}

func TestListSBOMPackagesEmptySBOM(t *testing.T) {
	assert.JSONEq(t, `{"sbom_name":"scratch","total":0,"returned":0,"truncated":false,"packages":[]}`,
		resultText(t, callSBOMTool(t, sbomReader(), "list_sbom_packages", map[string]any{"sbom_name": "scratch"})))
}

func TestListSBOMPackagesTooLargeSBOMKeepsStatus(t *testing.T) {
	assert.JSONEq(t, `{"sbom_name":"huge","status":"`+helpersv1.TooLarge+`","total":0,"returned":0,"truncated":false,"packages":[]}`,
		resultText(t, callSBOMTool(t, sbomReader(), "list_sbom_packages", map[string]any{"sbom_name": "huge"})))
}

func TestListSBOMPackagesInvalidArguments(t *testing.T) {
	for _, tt := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "missing sbom_name", args: map[string]any{}, want: "sbom_name is required"},
		{name: "name type", args: map[string]any{"sbom_name": "nginx-1.21", "name": 7}, want: "name must be a string"},
		{name: "package_type type", args: map[string]any{"sbom_name": "nginx-1.21", "package_type": true}, want: "package_type must be a string"},
		{name: "limit zero", args: map[string]any{"sbom_name": "nginx-1.21", "limit": float64(0)}, want: "limit must be between 1 and 500"},
		{name: "limit too large", args: map[string]any{"sbom_name": "nginx-1.21", "limit": float64(501)}, want: "limit must be between 1 and 500"},
		{name: "limit fraction", args: map[string]any{"sbom_name": "nginx-1.21", "limit": 1.5}, want: "limit must be an integer"},
		{name: "negative offset", args: map[string]any{"sbom_name": "nginx-1.21", "offset": float64(-1)}, want: "offset must not be negative"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res := callSBOMTool(t, sbomReader(), "list_sbom_packages", tt.args)
			require.True(t, res.IsError)
			assert.Equal(t, ErrCodeInvalidArgument, toolErrorOf(t, res).Code)
			assert.Contains(t, resultText(t, res), tt.want)
		})
	}
}

func TestListSBOMPackagesReadErrors(t *testing.T) {
	r := sbomReader()
	res := callSBOMTool(t, r, "list_sbom_packages", map[string]any{"sbom_name": "missing"})
	require.True(t, res.IsError)
	assert.Equal(t, ErrCodeResourceNotFound, toolErrorOf(t, res).Code)

	res = callSBOMTool(t, r, "list_sbom_packages", map[string]any{"namespace": restrictedNamespace, "sbom_name": "redis-7"})
	require.True(t, res.IsError)
	assert.Equal(t, ErrCodeRBACDenied, toolErrorOf(t, res).Code)
}

func TestListSBOMPackagesPackageIdentity(t *testing.T) {
	maven := func(group string, licenses ...v1beta1.License) v1beta1.SyftPackage {
		p := sbomPackage("commons-text", "1.9", "java-archive", licenses...)
		p.PURL = "pkg:maven/" + group + "/commons-text@1.9"
		return p
	}
	sbom := v1beta1.SBOMSyft{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "kubescape"},
		Spec: v1beta1.SBOMSyftSpec{Syft: v1beta1.SyftDocument{Artifacts: []v1beta1.SyftPackage{
			maven("org.apache.commons", v1beta1.License{Value: "Apache-2.0", SPDXExpression: "Apache-2.0"}),
			// Same name, version and type, but a different group ID: a different package.
			maven("com.example"),
			// The same package found in a second jar, with a license the first copy did not have.
			maven("org.apache.commons", v1beta1.License{Value: "MIT", SPDXExpression: "MIT"}),
		}}},
	}
	r := newMemReader()
	r.sboms = []v1beta1.SBOMSyft{sbom}
	resp := callSBOMPackages(t, r, map[string]any{"sbom_name": "app"})
	require.Equal(t, 2, resp.Total)
	assert.Equal(t, "pkg:maven/com.example/commons-text@1.9", resp.Packages[0].PURL, "ties on name, version and type are ordered by purl")
	assert.Equal(t, []string{}, resp.Packages[0].Licenses)
	assert.Equal(t, "pkg:maven/org.apache.commons/commons-text@1.9", resp.Packages[1].PURL)
	assert.Equal(t, []string{"Apache-2.0", "MIT"}, resp.Packages[1].Licenses, "licenses of all copies are kept")

	// A copy that the name filter does not match still adds its licenses.
	renamed := maven("org.apache.commons", v1beta1.License{Value: "BSD-3-Clause", SPDXExpression: "BSD-3-Clause"})
	renamed.Name = "apache-text"
	r.sboms[0].Spec.Syft.Artifacts = append(r.sboms[0].Spec.Syft.Artifacts, renamed)
	resp = callSBOMPackages(t, r, map[string]any{"sbom_name": "app", "name": "commons-text", "package_type": "java-archive"})
	require.Equal(t, 2, resp.Total)
	assert.Equal(t, []string{"Apache-2.0", "MIT", "BSD-3-Clause"}, resp.Packages[1].Licenses)

	// The fields shown come from a copy that matches the filter.
	resp = callSBOMPackages(t, r, map[string]any{"sbom_name": "app", "name": "apache-text"})
	require.Equal(t, 1, resp.Total)
	assert.Equal(t, "apache-text", resp.Packages[0].Name)
	assert.Equal(t, []string{"Apache-2.0", "MIT", "BSD-3-Clause"}, resp.Packages[0].Licenses)
}

func TestListSBOMs(t *testing.T) {
	r := sbomReader()
	call := func(args map[string]any) sbomListResponse {
		t.Helper()
		res := callSBOMTool(t, r, "list_sboms", args)
		require.False(t, res.IsError, resultText(t, res))
		var resp sbomListResponse
		require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &resp))
		return resp
	}

	resp := call(map[string]any{})
	require.Len(t, resp.SBOMs, 3, "the namespace in the endpoint's URL by default")
	byName := map[string]sbomEntry{}
	for _, e := range resp.SBOMs {
		byName[e.SBOMName] = e
	}
	assert.Equal(t, sbomEntry{
		Namespace: "kubescape",
		SBOMName:  "nginx-1.21",
		ImageID:   "docker.io/library/nginx@sha256:2834",
		ImageTag:  "nginx:1.21",
		Status:    helpersv1.Learning,
	}, byName["nginx-1.21"])
	assert.Equal(t, sbomEntry{Namespace: "kubescape", SBOMName: "scratch"}, byName["scratch"])
	assert.Equal(t, helpersv1.TooLarge, byName["huge"].Status)

	assert.Equal(t, []sbomEntry{{Namespace: "other", SBOMName: "redis-7"}}, call(map[string]any{"namespace": "other"}).SBOMs)
	assert.JSONEq(t, `{"sboms":[]}`, resultText(t, callSBOMTool(t, r, "list_sboms", map[string]any{"namespace": "nobody"})))

	res := callSBOMTool(t, r, "list_sboms", map[string]any{"namespace": restrictedNamespace})
	require.True(t, res.IsError)
	assert.Equal(t, ErrCodeRBACDenied, toolErrorOf(t, res).Code)

	res = callSBOMTool(t, r, "list_sboms", map[string]any{"continue": 3})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "continue must be a string")
}

func TestListSBOMsContinue(t *testing.T) {
	r := &fakeReader{sboms: &v1beta1.SBOMSyftList{
		ListMeta: metav1.ListMeta{Continue: "next-page"},
		Items:    []v1beta1.SBOMSyft{{ObjectMeta: metav1.ObjectMeta{Namespace: "kubescape", Name: "nginx-1.21"}}},
	}}
	res := callSBOMTool(t, r, "list_sboms", map[string]any{"namespace": "kubescape", "continue": "this-page"})
	require.False(t, res.IsError, resultText(t, res))
	assert.Equal(t, "kubescape", r.namespace)
	assert.Equal(t, metav1.ListOptions{Limit: 100, Continue: "this-page"}, r.opts)
	assert.JSONEq(t, `{"sboms":[{"namespace":"kubescape","sbom_name":"nginx-1.21"}],"continue":"next-page"}`, resultText(t, res))
}
