# Storage only serves requests forwarded by kube-apiserver

## Summary

Storage is an aggregated API server with authorization turned off
(`RecommendedOptions.Authorization = nil`): kube-apiserver authenticates the
caller and checks RBAC on `spdx.softwarecomposition.kubescape.io` before it
proxies a request, so a second SubjectAccessReview per request in storage would
only add load on kube-apiserver.

That design holds only if storage refuses requests that did not come through
kube-apiserver. Before this change it did not: the delegating authenticator from
`k8s.io/apiserver` accepted anonymous calls (hard-coded on in
`DelegatingAuthenticationOptions.ApplyTo`), bearer tokens via TokenReview, and
client certificates signed by the cluster CA (`tlsClientCaFile`). Any pod that
could reach the storage Service could read and write every namespace's data.

## Behavior

`Config()` (`pkg/cmd/server/start.go`) replaces the delegating authenticator with
`frontProxyOnly` (`pkg/cmd/server/frontproxy.go`):

- A request is authenticated only when its client certificate chains to the
  request-header (front-proxy) CA and carries an allowed common name, both read
  from `kube-system/extension-apiserver-authentication`. The user comes from the
  `X-Remote-*` headers kube-apiserver sets.
- `/healthz`, `/livez` and `/readyz` (and their subpaths) are served anonymously
  for kubelet probes.
- Everything else gets 401: anonymous calls, bearer tokens and cluster-CA
  certificates sent straight to the Service.
- Storage fails to start when the request-header CA is not available, instead of
  serving unauthenticated.

kube-aggregator's availability check, discovery and OpenAPI fetches go through
the front proxy, so the APIService stays `Available`.

## Opting out

`insecureSkipFrontProxyVerification: true` in the storage config keeps the old
delegating authenticator and logs a warning at startup. With authorization off,
anything that reaches the Service can then read and write all data. Use it only
for testing, or on clusters where the front-proxy CA cannot be established, and
restrict access to the Service with a NetworkPolicy. Defaults to `false`.

## Consequences

Clients must reach storage through kube-apiserver (the normal
`spdx.softwarecomposition.kubescape.io` client path). Direct calls to the
Service are refused. `tlsClientCaFile` no longer authenticates anyone.

## Tests

`pkg/cmd/server/frontproxy_test.go` covers the front proxy (accepted), anonymous,
bearer token, cluster-CA certificate and wrong common name (refused), and the
health paths (accepted anonymously). It also checks that `Config()` fails without
the request-header CA, and keeps the delegating authenticator when
`insecureSkipFrontProxyVerification` is set.
