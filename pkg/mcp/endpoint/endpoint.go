// Package endpoint serves the read-only MCP endpoint as the mcp connect
// subresource of mcpservers (designs-and-proposals storage-mcp-endpoint.md):
//
//	POST /apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/{ns}/mcpservers/default/mcp
//
// kube-apiserver authenticates the caller and checks RBAC create on
// mcpservers/mcp before it forwards the call. The endpoint accepts only calls
// forwarded by kube-apiserver and checks every read a tool does against the
// caller's RBAC.
package endpoint

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/kubescape/go-logger"
	"github.com/kubescape/go-logger/helpers"
	"github.com/kubescape/storage/pkg/apis/softwarecomposition"
	"github.com/kubescape/storage/pkg/mcp/tools"
	"github.com/mark3labs/mcp-go/server"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	x509request "k8s.io/apiserver/pkg/authentication/request/x509"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
)

// Name is the only mcpservers name served.
const Name = "default"

// maxRequestBytes caps an MCP request body; a request is a few hundred bytes.
// Storage's own limit is 1 GiB, for large objects.
const maxRequestBytes = 64 << 10

type Config struct {
	// RequestHeader is storage's front-proxy configuration: the CA and the
	// names of the client certificate kube-apiserver forwards requests with.
	RequestHeader *authenticatorfactory.RequestHeaderConfig
	// Authorizer checks each read for the caller (see NewAuthorizer).
	Authorizer authorizer.Authorizer
	// Scheme converts what the REST storage returns to v1beta1.
	Scheme *runtime.Scheme
	// VulnerabilityManifests is the vulnerabilitymanifests REST storage.
	VulnerabilityManifests rest.Storage
	// SBOMSyfts is the sbomsyfts REST storage.
	SBOMSyfts rest.Storage
	// WorkloadConfigurationScans is the workloadconfigurationscans REST storage.
	WorkloadConfigurationScans rest.Storage
	// ContainerProfiles is the containerprofiles REST storage.
	ContainerProfiles rest.Storage
}

type Endpoint struct {
	frontProxy authenticator.Request
	mcp        http.Handler
}

func New(cfg Config) (*Endpoint, error) {
	if cfg.RequestHeader == nil || cfg.RequestHeader.CAContentProvider == nil {
		return nil, errors.New("the MCP endpoint needs the front-proxy (request-header) CA")
	}
	if cfg.Authorizer == nil || cfg.Scheme == nil {
		return nil, errors.New("the MCP endpoint needs an authorizer and a scheme")
	}
	vulnerabilityManifests, ok := cfg.VulnerabilityManifests.(listGetter)
	if !ok {
		return nil, errors.New("the vulnerabilitymanifests storage cannot list and get")
	}
	sbomSyfts, ok := cfg.SBOMSyfts.(listGetter)
	if !ok {
		return nil, errors.New("the sbomsyfts storage cannot list and get")
	}
	configurationScans, ok := cfg.WorkloadConfigurationScans.(listGetter)
	if !ok {
		return nil, errors.New("the workloadconfigurationscans storage cannot list and get")
	}
	containerProfiles, ok := cfg.ContainerProfiles.(listGetter)
	if !ok {
		return nil, errors.New("the containerprofiles storage cannot list and get")
	}

	// k8s.io/apiserver's own front-proxy check: the wrapped authenticator runs
	// only when the client certificate chains to the request-header CA and has
	// an allowed common name.
	accept := authenticator.RequestFunc(func(*http.Request) (*authenticator.Response, bool, error) {
		return &authenticator.Response{}, true, nil
	})
	e := &Endpoint{
		frontProxy: x509request.NewDynamicCAVerifier(cfg.RequestHeader.CAContentProvider.VerifyOptions, accept, cfg.RequestHeader.AllowedClientNames),
	}

	s := server.NewMCPServer("kubescape-storage", "v1beta1", server.WithToolHandlerMiddleware(audit))
	tools.Register(s, &reader{
		authorizer:             cfg.Authorizer,
		scheme:                 cfg.Scheme,
		vulnerabilityManifests: vulnerabilityManifests,
		sbomSyfts:              sbomSyfts,
		configurationScans:     configurationScans,
		containerProfiles:      containerProfiles,
	})
	e.mcp = server.NewStreamableHTTPServer(s,
		server.WithStateLess(true),
		server.WithDisableStreaming(true),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			// Tools read the caller and the path namespace from the context.
			if u, ok := genericapirequest.UserFrom(r.Context()); ok {
				ctx = genericapirequest.WithUser(ctx, u)
			}
			ns := genericapirequest.NamespaceValue(r.Context())
			return tools.WithDefaultNamespace(genericapirequest.WithNamespace(ctx, ns), ns)
		}),
	)
	return e, nil
}

// ServeHTTP refuses anonymous calls and calls that did not come through
// kube-apiserver. Storage's authenticator also accepts bearer tokens sent
// straight to it and anonymous calls, so a user in the request context is not
// enough on its own.
func (e *Endpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, viaAPIServer, _ := e.frontProxy.AuthenticateRequest(r)
	u, ok := genericapirequest.UserFrom(r.Context())
	if !viaAPIServer || !ok || u.GetName() == "" || u.GetName() == user.Anonymous {
		var userName string
		if ok {
			userName = u.GetName()
		}
		logger.L().Info("mcp: refused a call",
			helpers.String("remote", r.RemoteAddr), helpers.Interface("frontProxyCert", viaAPIServer), helpers.String("user", userName))
		http.Error(w, "the MCP endpoint only accepts authenticated calls forwarded by kube-apiserver", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "the MCP request body is over 64 KiB", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "cannot read the request body", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	e.mcp.ServeHTTP(w, r)
}

// Storage returns the REST storage for "mcpservers" and "mcpservers/mcp".
func (e *Endpoint) Storage() (parent, connect rest.Storage) {
	return parentREST{}, &connectREST{endpoint: e}
}

// parentREST is the storage for mcpservers. It stores nothing and serves no
// verbs; it is the parent the mcp subresource needs.
type parentREST struct{}

var (
	_ rest.Storage              = parentREST{}
	_ rest.Scoper               = parentREST{}
	_ rest.SingularNameProvider = parentREST{}
)

func (parentREST) New() runtime.Object     { return &softwarecomposition.MCPServer{} }
func (parentREST) Destroy()                {}
func (parentREST) NamespaceScoped() bool   { return true }
func (parentREST) GetSingularName() string { return "mcpserver" }

// connectREST is the storage for mcpservers/mcp.
type connectREST struct {
	endpoint http.Handler
}

var _ rest.Connecter = &connectREST{}

func (*connectREST) New() runtime.Object { return &softwarecomposition.MCPServer{} }
func (*connectREST) Destroy()            {}

// ConnectMethods is POST only: MCP calls are POSTs, and the optional GET
// stream is not offered.
func (*connectREST) ConnectMethods() []string { return []string{http.MethodPost} }

func (*connectREST) NewConnectOptions() (runtime.Object, bool, string) { return nil, false, "" }

func (c *connectREST) Connect(ctx context.Context, name string, _ runtime.Object, _ rest.Responder) (http.Handler, error) {
	if name != Name {
		return nil, apierrors.NewNotFound(softwarecomposition.Resource("mcpservers"), name)
	}
	// The path namespace is on ctx, not on the request the handler gets.
	ns := genericapirequest.NamespaceValue(ctx)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.endpoint.ServeHTTP(w, r.WithContext(genericapirequest.WithNamespace(r.Context(), ns)))
	}), nil
}
