package server

import (
	"errors"
	"net/http"
	"strings"

	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authentication/request/headerrequest"
	"k8s.io/apiserver/pkg/authentication/user"
)

// healthPaths are served to kubelet probes, which present no credential.
var healthPaths = []string{"/healthz", "/livez", "/readyz"}

// frontProxyOnly authenticates a request only when kube-apiserver forwarded it:
// the client certificate must chain to the request-header (front-proxy) CA and
// carry an allowed common name. Authorization is left to kube-apiserver, which
// checks RBAC before proxying, so storage must refuse everything else - bearer
// tokens and certificates sent straight to the Service, and anonymous calls.
type frontProxyOnly struct {
	frontProxy authenticator.Request
}

func newFrontProxyOnly(cfg *authenticatorfactory.RequestHeaderConfig) (*frontProxyOnly, error) {
	if cfg == nil || cfg.CAContentProvider == nil {
		return nil, errors.New("storage only serves requests forwarded by kube-apiserver and needs the front-proxy (request-header) CA from kube-system/extension-apiserver-authentication")
	}
	return &frontProxyOnly{
		frontProxy: headerrequest.NewDynamicVerifyOptionsSecure(
			cfg.CAContentProvider.VerifyOptions,
			cfg.AllowedClientNames,
			cfg.UsernameHeaders,
			cfg.UIDHeaders,
			cfg.GroupHeaders,
			cfg.ExtraHeaderPrefixes,
		),
	}, nil
}

func (a *frontProxyOnly) AuthenticateRequest(req *http.Request) (*authenticator.Response, bool, error) {
	resp, ok, err := a.frontProxy.AuthenticateRequest(req)
	if ok || !isHealthPath(req.URL.Path) {
		return resp, ok, err
	}
	return &authenticator.Response{User: &user.DefaultInfo{Name: user.Anonymous, Groups: []string{user.AllUnauthenticated}}}, true, nil
}

func isHealthPath(path string) bool {
	for _, p := range healthPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}
