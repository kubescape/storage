package apiauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-logr/logr"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authentication/authenticator"
	"k8s.io/apiserver/pkg/authentication/authenticatorfactory"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/apiserver/pkg/endpoints/request"
	genericoptions "k8s.io/apiserver/pkg/server/options"
	"k8s.io/client-go/kubernetes"
	authorizationv1client "k8s.io/client-go/kubernetes/typed/authorization/v1"
)

// DisabledEnv names the environment variable that turns the middleware off
// when set to exactly "true".
const DisabledEnv = "KS_API_AUTH_DISABLED"

// Cache durations of the token and access review answers, the defaults of
// k8s.io/apiserver's delegated authentication and authorization.
const (
	tokenCacheTTL = 10 * time.Second
	allowCacheTTL = 10 * time.Second
	denyCacheTTL  = 10 * time.Second
)

// Attributes are the Kubernetes RBAC attributes a route is checked against
// with a SubjectAccessReview.
type Attributes struct {
	Group, Resource, Verb, Namespace string
}

// RouteFunc maps a request to the attributes to authorize it with. It returns
// false for routes that need no authentication, such as health probes.
type RouteFunc func(r *http.Request) (Attributes, bool)

// Config configures the middleware returned by New.
type Config struct {
	// Client is used to create TokenReviews and SubjectAccessReviews. Its
	// ServiceAccount needs the system:auth-delegator ClusterRole.
	Client kubernetes.Interface
	// Audience is the audience the bearer token must have been issued for.
	Audience string
	// Namespace is the SubjectAccessReview namespace used when
	// Attributes.Namespace is empty.
	Namespace string
	// Route maps a request to its RBAC attributes.
	Route RouteFunc
	// Logger receives one line per decision. The zero value discards.
	Logger logr.Logger
}

// NewAuthorizer returns an authorizer that asks kube-apiserver with a
// SubjectAccessReview and caches the answer. The caller's ServiceAccount must
// be bound to system:auth-delegator, which allows it to create
// SubjectAccessReviews.
func NewAuthorizer(client authorizationv1client.AuthorizationV1Interface) (authorizer.Authorizer, error) {
	return authorizerfactory.DelegatingAuthorizerConfig{
		SubjectAccessReviewClient: client,
		AllowCacheTTL:             allowCacheTTL,
		DenyCacheTTL:              denyCacheTTL,
		WebhookRetryBackoff:       genericoptions.DefaultAuthWebhookRetryBackoff(),
	}.New()
}

// UserFrom returns the user the middleware authenticated, stored with
// k8s.io/apiserver/pkg/endpoints/request.WithUser.
func UserFrom(ctx context.Context) (user.Info, bool) {
	return request.UserFrom(ctx)
}

// New returns middleware that authenticates the request's bearer token with a
// TokenReview for cfg.Audience and authorizes it with a SubjectAccessReview
// for the attributes cfg.Route returns. Answers are cached for 10 seconds.
//
// Unauthenticated requests get 401, unauthorized ones 403, and requests that
// cannot be checked because kube-apiserver cannot be reached or refuses the
// review get 503. If $KS_API_AUTH_DISABLED is "true", New returns a
// pass-through middleware. Otherwise New first checks with
// SelfSubjectAccessReviews that the client may create TokenReviews and
// SubjectAccessReviews, and returns an error naming system:auth-delegator if not.
func New(ctx context.Context, cfg Config) (func(http.Handler) http.Handler, error) {
	log := cfg.Logger
	if log.GetSink() == nil {
		log = logr.Discard()
	}
	if os.Getenv(DisabledEnv) == "true" {
		log.Info("WARNING: API authentication is disabled; every request is allowed", "env", DisabledEnv)
		return func(next http.Handler) http.Handler { return next }, nil
	}
	if cfg.Client == nil {
		return nil, errors.New("apiauth: Config.Client is required")
	}
	if cfg.Route == nil {
		return nil, errors.New("apiauth: Config.Route is required")
	}
	if cfg.Audience == "" {
		return nil, errors.New("apiauth: Config.Audience is required")
	}

	if err := probePermissions(ctx, cfg.Client); err != nil {
		return nil, err
	}

	authn, _, err := authenticatorfactory.DelegatingAuthenticatorConfig{
		Anonymous:               nil,
		TokenAccessReviewClient: cfg.Client.AuthenticationV1(),
		APIAudiences:            authenticator.Audiences{cfg.Audience},
		CacheTTL:                tokenCacheTTL,
		WebhookRetryBackoff:     genericoptions.DefaultAuthWebhookRetryBackoff(),
	}.New()
	if err != nil {
		return nil, err
	}
	authz, err := NewAuthorizer(cfg.Client.AuthorizationV1())
	if err != nil {
		return nil, err
	}
	m := &middleware{cfg: cfg, log: log, authn: authn, authz: authz}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			m.serve(next, w, r)
		})
	}, nil
}

// probeTimeout bounds the startup permission probe.
const probeTimeout = 10 * time.Second

// probePermissions checks with SelfSubjectAccessReviews that the caller may
// create TokenReviews and SubjectAccessReviews, so a missing
// system:auth-delegator binding fails at startup, not on the first request.
func probePermissions(ctx context.Context, client kubernetes.Interface) error {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	for _, a := range []authorizationv1.ResourceAttributes{
		{Verb: "create", Group: "authentication.k8s.io", Resource: "tokenreviews"},
		{Verb: "create", Group: "authorization.k8s.io", Resource: "subjectaccessreviews"},
	} {
		a := a
		resp, err := client.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx,
			&authorizationv1.SelfSubjectAccessReview{Spec: authorizationv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &a}},
			metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("apiauth: cannot check permission to create %s.%s (the ServiceAccount needs the system:auth-delegator ClusterRole): %w",
				a.Resource, a.Group, err)
		}
		if !resp.Status.Allowed {
			return fmt.Errorf("apiauth: not allowed to create %s.%s: bind the ServiceAccount to the system:auth-delegator ClusterRole",
				a.Resource, a.Group)
		}
	}
	return nil
}

type middleware struct {
	cfg   Config
	log   logr.Logger
	authn authenticator.Request
	authz authorizer.Authorizer
}

func (m *middleware) serve(next http.Handler, w http.ResponseWriter, r *http.Request) {
	attrs, protected := m.cfg.Route(r)
	if !protected {
		next.ServeHTTP(w, r)
		return
	}
	deny := func(username, outcome, reason string) {
		m.log.Info("API request denied", "user", username, "verb", attrs.Verb, "resource", attrs.Resource,
			"outcome", outcome, "reason", reason, "path", r.URL.Path)
	}

	header := r.Header.Get("Authorization")
	if header == "" {
		deny("", "unauthenticated", reasonMissingHeader)
		writeUnauthenticated(w, reasonMissingHeader)
		return
	}
	scheme, token, _ := strings.Cut(header, " ")
	token = strings.TrimSpace(token)
	if !strings.EqualFold(scheme, "Bearer") || token == "" || strings.ContainsAny(token, " \t") {
		deny("", "unauthenticated", reasonNotBearer)
		writeUnauthenticated(w, reasonNotBearer)
		return
	}

	// The authenticator gets a request of its own carrying only the
	// normalised Authorization header, so no other credential in the
	// caller's request (such as a WebSocket protocol token) is considered,
	// and the request's audience is set so that the TokenReview checks it.
	ctx := authenticator.WithAudiences(r.Context(), authenticator.Audiences{m.cfg.Audience})
	authReq, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		m.log.Error(err, "API request denied", "verb", attrs.Verb, "resource", attrs.Resource, "outcome", "unavailable")
		writeUnavailable(w)
		return
	}
	authReq.Header.Set("Authorization", "Bearer "+token)
	resp, ok, err := m.authn.AuthenticateRequest(authReq)
	if isBackendError(err) {
		m.log.Error(err, "API request denied: TokenReview failed", "verb", attrs.Verb, "resource", attrs.Resource, "outcome", "unavailable")
		writeUnavailable(w)
		return
	}
	if !ok || resp == nil || resp.User == nil {
		deny("", "unauthenticated", reasonInvalidToken)
		writeUnauthenticated(w, reasonInvalidToken)
		return
	}
	info := resp.User

	ns := attrs.Namespace
	if ns == "" {
		ns = m.cfg.Namespace
	}
	decision, reason, err := m.authz.Authorize(r.Context(), authorizer.AttributesRecord{
		User:            info,
		Verb:            attrs.Verb,
		APIGroup:        attrs.Group,
		Resource:        attrs.Resource,
		Namespace:       ns,
		ResourceRequest: true,
	})
	if err != nil {
		m.log.Error(err, "API request denied: SubjectAccessReview failed", "user", info.GetName(), "verb", attrs.Verb,
			"resource", attrs.Resource, "outcome", "unavailable")
		writeUnavailable(w)
		return
	}
	if decision != authorizer.DecisionAllow {
		deny(info.GetName(), "forbidden", reason)
		writeForbidden(w, reason)
		return
	}

	m.log.Info("API request allowed", "user", info.GetName(), "verb", attrs.Verb, "resource", attrs.Resource,
		"outcome", "allowed", "path", r.URL.Path)
	// Like kube-apiserver, drop the bearer token once it has been used so no
	// downstream handler can forward or log it. Clone deep-copies the headers,
	// leaving the caller's request untouched.
	out := r.Clone(request.WithUser(r.Context(), info))
	out.Header.Del("Authorization")
	next.ServeHTTP(w, out)
}
