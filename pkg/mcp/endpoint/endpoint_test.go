package endpoint

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition/install"
	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authentication/request/headerrequest"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
)

const frontProxyName = "front-proxy-client"

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

func newTestCA(t *testing.T, name string) testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// clientCert returns a client certificate signed by ca.
func (ca testCA) clientCert(t *testing.T, commonName string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

// fakeLister stands in for the vulnerabilitymanifests REST storage.
type fakeLister struct {
	rest.TableConvertor
	calls     int
	namespace string
	info      *genericapirequest.RequestInfo
	items     []softwarecomposition.VulnerabilityManifest
}

func (f *fakeLister) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	f.calls++
	f.namespace = genericapirequest.NamespaceValue(ctx)
	f.info, _ = genericapirequest.RequestInfoFrom(ctx)
	for i := range f.items {
		if f.items[i].Namespace == f.namespace && f.items[i].Name == name {
			return &f.items[i], nil
		}
	}
	return nil, apierrors.NewNotFound(softwarecomposition.Resource("vulnerabilitymanifests"), name)
}

func (f *fakeLister) New() runtime.Object { return &softwarecomposition.VulnerabilityManifest{} }
func (f *fakeLister) Destroy()            {}

func (f *fakeLister) NewList() runtime.Object {
	return &softwarecomposition.VulnerabilityManifestList{}
}

func (f *fakeLister) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	f.calls++
	f.namespace = genericapirequest.NamespaceValue(ctx)
	f.info, _ = genericapirequest.RequestInfoFrom(ctx)
	return &softwarecomposition.VulnerabilityManifestList{Items: f.items}, nil
}

// fakeSBOMs stands in for the sbomsyfts REST storage.
type fakeSBOMs struct {
	rest.TableConvertor
	calls int
	info  *genericapirequest.RequestInfo
	items []softwarecomposition.SBOMSyft
}

func (f *fakeSBOMs) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	f.calls++
	f.info, _ = genericapirequest.RequestInfoFrom(ctx)
	namespace := genericapirequest.NamespaceValue(ctx)
	for i := range f.items {
		if f.items[i].Namespace == namespace && f.items[i].Name == name {
			return &f.items[i], nil
		}
	}
	return nil, apierrors.NewNotFound(softwarecomposition.Resource("sbomsyfts"), name)
}

func (f *fakeSBOMs) New() runtime.Object     { return &softwarecomposition.SBOMSyft{} }
func (f *fakeSBOMs) Destroy()                {}
func (f *fakeSBOMs) NewList() runtime.Object { return &softwarecomposition.SBOMSyftList{} }

func (f *fakeSBOMs) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	f.calls++
	f.info, _ = genericapirequest.RequestInfoFrom(ctx)
	return &softwarecomposition.SBOMSyftList{Items: f.items}, nil
}

type testEndpoint struct {
	connect   rest.Storage
	lister    *fakeLister
	sboms     *fakeSBOMs
	frontCA   testCA
	attrs     []authorizer.Attributes
	allowedNS string
}

// newTestEndpoint returns an endpoint whose authorizer allows only
// "list" and "get" on vulnerabilitymanifests and sbomsyfts in allowedNS. opts
// change its configuration.
func newTestEndpoint(t *testing.T, allowedNS string, opts ...func(*Config)) *testEndpoint {
	t.Helper()
	te := &testEndpoint{frontCA: newTestCA(t, "front-proxy-ca"), allowedNS: allowedNS, lister: &fakeLister{
		items: []softwarecomposition.VulnerabilityManifest{
			{ObjectMeta: metav1.ObjectMeta{Namespace: allowedNS, Name: "nginx-1.21"},
				Spec: softwarecomposition.VulnerabilityManifestSpec{Payload: softwarecomposition.GrypeDocument{Matches: []softwarecomposition.Match{
					{Vulnerability: softwarecomposition.Vulnerability{VulnerabilityMetadata: softwarecomposition.VulnerabilityMetadata{ID: "CVE-2017-12424", Severity: "Critical"}},
						Artifact: softwarecomposition.GrypePackage{Name: "login", Version: "1:4.4-4.1", Type: "deb"}},
				}}}},
		},
	}}
	te.sboms = &fakeSBOMs{items: []softwarecomposition.SBOMSyft{
		{ObjectMeta: metav1.ObjectMeta{Namespace: allowedNS, Name: "nginx-1.21"},
			Spec: softwarecomposition.SBOMSyftSpec{Syft: softwarecomposition.SyftDocument{Artifacts: []softwarecomposition.SyftPackage{
				{PackageBasicData: softwarecomposition.PackageBasicData{Name: "login", Version: "1:4.4-4.1", Type: "deb"}},
			}}}},
	}}
	ca, err := dynamiccertificates.NewStaticCAContent("front-proxy-ca", te.frontCA.pem)
	require.NoError(t, err)
	scheme := runtime.NewScheme()
	install.Install(scheme)
	cfg := Config{
		RequestHeader: &authenticatorfactory.RequestHeaderConfig{
			CAContentProvider:  ca,
			AllowedClientNames: headerrequest.StaticStringSlice{frontProxyName},
		},
		Authorizer: authorizer.AuthorizerFunc(func(_ context.Context, a authorizer.Attributes) (authorizer.Decision, string, error) {
			te.attrs = append(te.attrs, a)
			if (a.GetVerb() == "list" || a.GetVerb() == "get") && (a.GetResource() == "vulnerabilitymanifests" || a.GetResource() == "sbomsyfts") && a.GetNamespace() == te.allowedNS {
				return authorizer.DecisionAllow, "", nil
			}
			return authorizer.DecisionNoOpinion, "", nil
		}),
		Scheme:                 scheme,
		VulnerabilityManifests: te.lister,
		SBOMSyfts:              te.sboms,
		CallTimeout:            20 * time.Second,
		MaxResponseBytes:       256 << 10,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	e, err := New(cfg)
	require.NoError(t, err)
	_, te.connect = e.Storage()
	return te
}

// call sends one MCP request to mcpservers/{name}/mcp in namespace, as
// kube-apiserver would forward it: with cert as the client certificate and u
// as the authenticated user.
func (te *testEndpoint) call(t *testing.T, namespace string, cert *x509.Certificate, u user.Info, body string) *httptest.ResponseRecorder {
	t.Helper()
	ctx := genericapirequest.WithNamespace(context.Background(), namespace)
	h, err := te.connect.(rest.Connecter).Connect(ctx, Name, nil, nil)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/"+namespace+"/mcpservers/default/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	if cert != nil {
		req.TLS = &tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	}
	if u != nil {
		req = req.WithContext(genericapirequest.WithUser(req.Context(), u))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const listCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_vulnerability_manifests","arguments":%s}}`

func toolCall(args string) string { return strings.Replace(listCall, "%s", args, 1) }

// toolResult returns the tool result text and whether it is an error.
func toolResult(t *testing.T, rec *httptest.ResponseRecorder) (string, bool) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp), rec.Body.String())
	require.Len(t, resp.Result.Content, 1, rec.Body.String())
	return resp.Result.Content[0].Text, resp.Result.IsError
}

var agent = &user.DefaultInfo{Name: "system:serviceaccount:kagent:agent", Groups: []string{"system:serviceaccounts", "system:authenticated"}}

func TestRefusesCallsNotForwardedByAPIServer(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	other := newTestCA(t, "cluster-client-ca")
	for _, tc := range []struct {
		name string
		cert *x509.Certificate
		user user.Info
	}{
		{"no client certificate (bearer token sent straight to storage)", nil, agent},
		{"certificate from another CA", other.clientCert(t, frontProxyName), agent},
		{"front-proxy CA but not an allowed name", te.frontCA.clientCert(t, "someone-else"), agent},
		{"anonymous", te.frontCA.clientCert(t, frontProxyName), &user.DefaultInfo{Name: user.Anonymous}},
		{"no user", te.frontCA.clientCert(t, frontProxyName), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := te.call(t, "kubescape", tc.cert, tc.user, toolCall(`{}`))
			assert.Equal(t, http.StatusUnauthorized, rec.Code)
		})
	}
	assert.Zero(t, te.lister.calls)
	assert.Empty(t, te.attrs)
}

func TestToolReadsAreCheckedForTheCaller(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)

	// Allowed: the read is checked for the caller with the same attributes as
	// "kubectl get vulnerabilitymanifests -n kubescape".
	text, isError := toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"kubescape"}`)))
	require.False(t, isError, text)
	assert.Contains(t, text, `"manifest_name":"nginx-1.21"`)
	assert.Equal(t, 1, te.lister.calls)
	assert.Equal(t, "kubescape", te.lister.namespace)
	require.NotNil(t, te.lister.info)
	assert.Equal(t, []string{"list", "vulnerabilitymanifests", "kubescape"}, []string{te.lister.info.Verb, te.lister.info.Resource, te.lister.info.Namespace})
	require.Len(t, te.attrs, 1)
	a := te.attrs[0]
	assert.Equal(t, agent.Name, a.GetUser().GetName())
	assert.Equal(t, agent.Groups, a.GetUser().GetGroups())
	assert.Equal(t, []any{"list", "spdx.softwarecomposition.kubescape.io", "v1beta1", "vulnerabilitymanifests", "kubescape", true},
		[]any{a.GetVerb(), a.GetAPIGroup(), a.GetAPIVersion(), a.GetResource(), a.GetNamespace(), a.IsResourceRequest()})

	// Denied: no namespace means the path namespace, where the caller may not
	// list; the agent gets RBAC_DENIED and nothing is read.
	text, isError = toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{}`)))
	require.True(t, isError)
	var toolErr tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeRBACDenied, toolErr.Code)
	assert.Contains(t, toolErr.Message, `cannot list resource "vulnerabilitymanifests" in API group "spdx.softwarecomposition.kubescape.io" in the namespace "kagent"`)
	assert.Equal(t, "kagent", te.attrs[1].GetNamespace())
	assert.Equal(t, 1, te.lister.calls)
}

func TestGetIsCheckedForTheCallerWithTheName(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)
	call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_vulnerabilities_in_manifest","arguments":%s}}`

	// Allowed: checked as "kubectl get vulnerabilitymanifests nginx-1.21 -n
	// kubescape", and the tool sees the manifest's matches.
	text, isError := toolResult(t, te.call(t, "kagent", cert, agent, strings.Replace(call, "%s", `{"namespace":"kubescape","manifest_name":"nginx-1.21"}`, 1)))
	require.False(t, isError, text)
	assert.Contains(t, text, `"id":"CVE-2017-12424"`)
	require.Len(t, te.attrs, 1)
	a := te.attrs[0]
	assert.Equal(t, []any{"get", "vulnerabilitymanifests", "kubescape", "nginx-1.21"},
		[]any{a.GetVerb(), a.GetResource(), a.GetNamespace(), a.GetName()})
	assert.Equal(t, 1, te.lister.calls)
	// The REST storage names the resource in its errors after the request
	// info, so the read carries its own, not the one of mcpservers/mcp.
	require.NotNil(t, te.lister.info)
	assert.Equal(t, []string{"get", "spdx.softwarecomposition.kubescape.io", "vulnerabilitymanifests", "kubescape", "nginx-1.21"},
		[]string{te.lister.info.Verb, te.lister.info.APIGroup, te.lister.info.Resource, te.lister.info.Namespace, te.lister.info.Name})

	// Denied in the path namespace: RBAC_DENIED and nothing is read.
	text, isError = toolResult(t, te.call(t, "kagent", cert, agent, strings.Replace(call, "%s", `{"manifest_name":"nginx-1.21"}`, 1)))
	require.True(t, isError)
	var toolErr tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeRBACDenied, toolErr.Code)
	assert.Contains(t, toolErr.Message, `cannot get resource "vulnerabilitymanifests" in API group "spdx.softwarecomposition.kubescape.io" in the namespace "kagent"`)
	assert.Equal(t, 1, te.lister.calls)

	// Allowed but missing: RESOURCE_NOT_FOUND.
	text, isError = toolResult(t, te.call(t, "kagent", cert, agent, strings.Replace(call, "%s", `{"namespace":"kubescape","manifest_name":"redis"}`, 1)))
	require.True(t, isError)
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeResourceNotFound, toolErr.Code)
}

func TestSBOMReadsAreCheckedForTheCaller(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)
	call := func(tool, args string) string {
		return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool + `","arguments":` + args + `}}`
	}

	// list_sboms: checked as "kubectl get sbomsyfts -n kubescape".
	text, isError := toolResult(t, te.call(t, "kagent", cert, agent, call("list_sboms", `{"namespace":"kubescape"}`)))
	require.False(t, isError, text)
	assert.Contains(t, text, `"sbom_name":"nginx-1.21"`)
	require.Len(t, te.attrs, 1)
	assert.Equal(t, []any{"list", "sbomsyfts", "kubescape", ""},
		[]any{te.attrs[0].GetVerb(), te.attrs[0].GetResource(), te.attrs[0].GetNamespace(), te.attrs[0].GetName()})
	require.NotNil(t, te.sboms.info)
	assert.Equal(t, []string{"list", "sbomsyfts", "kubescape"}, []string{te.sboms.info.Verb, te.sboms.info.Resource, te.sboms.info.Namespace})

	// list_sbom_packages: checked as "kubectl get sbomsyfts nginx-1.21 -n kubescape".
	text, isError = toolResult(t, te.call(t, "kagent", cert, agent, call("list_sbom_packages", `{"namespace":"kubescape","sbom_name":"nginx-1.21"}`)))
	require.False(t, isError, text)
	assert.Contains(t, text, `"name":"login"`)
	require.Len(t, te.attrs, 2)
	assert.Equal(t, []any{"get", "sbomsyfts", "kubescape", "nginx-1.21"},
		[]any{te.attrs[1].GetVerb(), te.attrs[1].GetResource(), te.attrs[1].GetNamespace(), te.attrs[1].GetName()})
	assert.Equal(t, []string{"get", "sbomsyfts", "kubescape", "nginx-1.21"},
		[]string{te.sboms.info.Verb, te.sboms.info.Resource, te.sboms.info.Namespace, te.sboms.info.Name})
	assert.Equal(t, 2, te.sboms.calls)

	// Denied in the path namespace: RBAC_DENIED and nothing is read.
	for _, tc := range []struct{ tool, args, message string }{
		{"list_sboms", `{}`, `cannot list resource "sbomsyfts"`},
		{"list_sbom_packages", `{"sbom_name":"nginx-1.21"}`, `cannot get resource "sbomsyfts"`},
	} {
		text, isError = toolResult(t, te.call(t, "kagent", cert, agent, call(tc.tool, tc.args)))
		require.True(t, isError)
		var toolErr tools.ToolError
		require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
		assert.Equal(t, tools.ErrCodeRBACDenied, toolErr.Code)
		assert.Contains(t, toolErr.Message, tc.message)
	}
	assert.Equal(t, 2, te.sboms.calls)
	assert.Zero(t, te.lister.calls)
}

func TestResponseSizeCap(t *testing.T) {
	te := newTestEndpoint(t, "kubescape", func(c *Config) { c.MaxResponseBytes = 100 })
	cert := te.frontCA.clientCert(t, frontProxyName)
	text, isError := toolResult(t, te.call(t, "kagent", cert, agent, toolCall(`{"namespace":"kubescape"}`)))
	require.True(t, isError)
	var toolErr tools.ToolError
	require.NoError(t, json.Unmarshal([]byte(text), &toolErr))
	assert.Equal(t, tools.ErrCodeResponseTooLarge, toolErr.Code)
	assert.EqualValues(t, 100, toolErr.Details["max_bytes"])
	assert.Equal(t, 1, te.lister.calls, "the read happened; only the answer is replaced")
}

func TestNewNeedsLimits(t *testing.T) {
	ca, err := dynamiccertificates.NewStaticCAContent("front-proxy-ca", newTestCA(t, "front-proxy-ca").pem)
	require.NoError(t, err)
	scheme := runtime.NewScheme()
	install.Install(scheme)
	_, err = New(Config{
		RequestHeader: &authenticatorfactory.RequestHeaderConfig{CAContentProvider: ca},
		Authorizer: authorizer.AuthorizerFunc(func(context.Context, authorizer.Attributes) (authorizer.Decision, string, error) {
			return authorizer.DecisionNoOpinion, "", nil
		}),
		Scheme:                 scheme,
		VulnerabilityManifests: &fakeLister{},
		SBOMSyfts:              &fakeSBOMs{},
	})
	assert.ErrorContains(t, err, "positive call timeout and response size cap")
}

func TestRequestBodyLimit(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	cert := te.frontCA.clientCert(t, frontProxyName)
	padded := toolCall(`{"namespace":"kubescape","continue":"` + strings.Repeat("x", maxRequestBytes) + `"}`)
	assert.Equal(t, http.StatusRequestEntityTooLarge, te.call(t, "kagent", cert, agent, padded).Code)
	assert.Zero(t, te.lister.calls)
}

func TestConnect(t *testing.T) {
	te := newTestEndpoint(t, "kubescape")
	c := te.connect.(rest.Connecter)
	assert.Equal(t, []string{http.MethodPost}, c.ConnectMethods())
	_, err := c.Connect(genericapirequest.WithNamespace(context.Background(), "kagent"), "other", nil, nil)
	assert.True(t, apierrors.IsNotFound(err), err)
}

func TestNewAuthorizerAsksKubeAPIServerAndCaches(t *testing.T) {
	// A kube-apiserver that answers SubjectAccessReviews: allowed only in kubescape.
	var reviews []authorizationv1.SubjectAccessReview
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/apis/authorization.k8s.io/v1/subjectaccessreviews", r.URL.Path)
		var sar authorizationv1.SubjectAccessReview
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&sar))
		reviews = append(reviews, sar)
		sar.Status.Allowed = sar.Spec.ResourceAttributes.Namespace == "kubescape"
		w.Header().Set("Content-Type", "application/json")
		assert.NoError(t, json.NewEncoder(w).Encode(sar))
	}))
	defer srv.Close()
	client, err := kubernetes.NewForConfig(&restclient.Config{Host: srv.URL})
	require.NoError(t, err)
	authz, err := NewAuthorizer(client.AuthorizationV1())
	require.NoError(t, err)

	attrs := func(namespace string) authorizer.Attributes {
		return authorizer.AttributesRecord{User: agent, Verb: "list", Namespace: namespace,
			APIGroup: softwarecomposition.GroupName, APIVersion: "v1beta1", Resource: "vulnerabilitymanifests", ResourceRequest: true}
	}
	for range 2 {
		decision, _, err := authz.Authorize(context.Background(), attrs("kubescape"))
		require.NoError(t, err)
		assert.Equal(t, authorizer.DecisionAllow, decision)
	}
	decision, _, err := authz.Authorize(context.Background(), attrs("team-a"))
	require.NoError(t, err)
	assert.NotEqual(t, authorizer.DecisionAllow, decision)

	// The second identical check came from the cache.
	require.Len(t, reviews, 2)
	assert.Equal(t, agent.Name, reviews[0].Spec.User)
	assert.Equal(t, agent.Groups, reviews[0].Spec.Groups)
	assert.Equal(t, &authorizationv1.ResourceAttributes{Namespace: "kubescape", Verb: "list",
		Group: softwarecomposition.GroupName, Version: "v1beta1", Resource: "vulnerabilitymanifests"}, reviews[0].Spec.ResourceAttributes)
}
