# apiauth

Server middleware and client helpers for service-account bearer authentication between Kubescape in-cluster components.

## Server middleware

`New(ctx, Config)` returns an `http.Handler` middleware. For each request that
`Config.Route` marks as protected, it:

1. reads `Authorization: Bearer <token>` (scheme case-insensitive),
2. authenticates the token with a TokenReview for `Config.Audience`,
3. authorizes the user with a SubjectAccessReview for the route's group,
   resource and verb, in the route's namespace or `Config.Namespace`,
4. stores the user in the request context (`UserFrom(ctx)`).

`Config` fields: `Client` (needs `system:auth-delegator`), `Audience`,
`Namespace` (default SubjectAccessReview namespace), `Route`
(`func(*http.Request) (Attributes, bool)`; `false` means the route is not
protected, e.g. health probes) and `Logger` (zero value discards). Review
answers are cached for 10 seconds.

Responses, all `Content-Type: application/json`:

| Status | Body | When |
|---|---|---|
| 401 | `{"error":"unauthenticated","reason":"..."}` with `WWW-Authenticate: Bearer realm="kubescape"` | missing header, not a Bearer token, invalid or wrong-audience token |
| 403 | `{"error":"forbidden","reason":"<SubjectAccessReview reason>"}` | RBAC denies the request |
| 503 | `{"error":"unavailable","reason":"authentication backend error"}` | kube-apiserver cannot be reached or refuses the review |

Off switch: with `KS_API_AUTH_DISABLED=true`, `New` logs a warning and
returns a pass-through middleware.

`NewAuthorizer` returns the cached SubjectAccessReview authorizer on its own.
