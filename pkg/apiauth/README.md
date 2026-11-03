# apiauth

Service-account bearer authentication and RBAC authorization for the HTTP APIs
between Kubescape in-cluster components. A server wraps its handler in a
middleware that validates the caller's token with a TokenReview and checks RBAC
with a SubjectAccessReview; a client adds its projected ServiceAccount token to
requests for the hosts it is allowed to call.

This is a nested Go module, `github.com/kubescape/storage/pkg/apiauth`, so
consumers pin it independently of storage.

## Server side

```go
cfg, _ := rest.InClusterConfig() // handle errors in real code
client, _ := kubernetes.NewForConfig(cfg)

route := func(r *http.Request) (apiauth.Attributes, bool) {
	switch {
	case r.URL.Path == "/healthz":
		return apiauth.Attributes{}, false // not protected
	case strings.HasPrefix(r.URL.Path, "/v1/scan"):
		return apiauth.Attributes{Group: "kubescape.io", Resource: "scans", Verb: "create"}, true
	default:
		return apiauth.Attributes{Group: "kubescape.io", Resource: "reports", Verb: "get"}, true
	}
}

protect, err := apiauth.New(ctx, apiauth.Config{
	Client:    client,
	Audience:  "kubescape.io",
	Namespace: "kubescape", // SubjectAccessReview namespace when Attributes.Namespace is empty
	Route:     route,
	Logger:    logger,
})
if err != nil {
	log.Fatal(err) // exit: the server cannot enforce auth
}
http.ListenAndServe(":8080", protect(mux))
```

### Request flow

For each request that `Config.Route` marks as protected, the middleware:

1. reads `Authorization: Bearer <token>` (scheme is case-insensitive),
2. authenticates the token with a TokenReview for `Config.Audience`,
3. authorizes the user with a SubjectAccessReview for the route's group,
   resource and verb, in `Attributes.Namespace` or else `Config.Namespace`,
4. stores the user in the request context (`apiauth.UserFrom(ctx)`) and removes
   the `Authorization` header from the request passed to the next handler.

TokenReview and SubjectAccessReview answers are cached for 10 seconds each.
Routes for which `Route` returns `false` skip all of this.

All error responses have `Content-Type: application/json`:

| Status | Body | When |
|---|---|---|
| 401 | `{"error":"unauthenticated","reason":"..."}` plus `WWW-Authenticate: Bearer realm="kubescape"` | missing header, not a Bearer token, invalid or wrong-audience token |
| 403 | `{"error":"forbidden","reason":"<SubjectAccessReview reason>"}` | RBAC denies the request |
| 503 | `{"error":"unavailable","reason":"authentication backend error"}` | kube-apiserver cannot be reached or refuses the review |

### Startup probe

`New` first runs two SelfSubjectAccessReviews, asking whether the client may
create `tokenreviews` (authentication.k8s.io) and `subjectaccessreviews`
(authorization.k8s.io). If either is refused or fails (10 second timeout), `New`
returns an error that names the `system:auth-delegator` ClusterRole, so a
missing binding fails at startup rather than on the first request.

`NewAuthorizer` returns the cached SubjectAccessReview authorizer on its own,
for code that does its own authentication.

## Client side

```go
src := apiauth.NewFileTokenSource(apiauth.DefaultTokenFile())
httpClient := &http.Client{
	Transport: apiauth.NewTransport(nil, src, "kubescape:8080", "kubevuln:8080"),
}
```

- The transport adds `Authorization: Bearer <token>` only to requests whose URL
  host exactly matches (case-insensitively) one of the listed `host` or
  `host:port` values. Other hosts never receive the token.
- An existing `Authorization` header is never overwritten.
- If the token file does not exist, or the token is empty or unreadable, the
  request is sent without the header instead of failing.
- The file source re-reads the file when its mtime changes, so token rotation
  by the kubelet is picked up without a restart. A nil base transport means
  `http.DefaultTransport`.

## Environment variables

| Variable | Side | Meaning |
|---|---|---|
| `KS_API_AUTH_DISABLED` | server | `true` turns authentication off: `New` logs a startup warning and returns a pass-through middleware (the probe is skipped). Any other value leaves auth on. |
| `KS_API_AUDIENCE` | server | The audience the server accepts, set by the chart; default `kubescape.io`. The package itself takes it through `Config.Audience`; the service reads this variable and passes it in. |
| `KS_API_TOKEN_FILE` | client | Path of the projected token. Default `/var/run/secrets/kubescape.io/api-token/token`. Read by `DefaultTokenFile()`. |

## RBAC the server needs

Bind the server's ServiceAccount to `system:auth-delegator` so it may create
TokenReviews and SubjectAccessReviews:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubescape-api-auth-delegator
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - kind: ServiceAccount
    name: kubescape
    namespace: kubescape
```

Callers need two things. First, a Role (or ClusterRole) granting the verbs on
the virtual resources the routes map to; nothing with these names exists in the
cluster, they are only RBAC attributes:

```yaml
rules:
  - apiGroups: ["kubescape.io"]
    resources: ["scans"]
    verbs: ["create"]
```

Second, a projected ServiceAccount token with audience `kubescape.io`:

```yaml
volumes:
  - name: api-token
    projected:
      sources:
        - serviceAccountToken:
            audience: kubescape.io
            expirationSeconds: 3600
            path: token
# mount it read-only at /var/run/secrets/kubescape.io/api-token
```

## Design

https://github.com/kubescape/designs-and-proposals/blob/main/proposals/in-cluster-api-authn-authz.md
