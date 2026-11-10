# Read-only MCP endpoint for in-cluster agents

## Summary

Storage can serve a read-only [MCP](https://modelcontextprotocol.io) endpoint,
so an AI agent running in the cluster can read Kubescape's results
(vulnerabilities, SBOMs, configuration scans, container profiles) with its own
ServiceAccount. The endpoint is the `mcp` connect subresource of the
`mcpservers` resource in `spdx.softwarecomposition.kubescape.io`:

```
POST /apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/{namespace}/mcpservers/default/mcp
```

The agent calls kube-apiserver, not the storage Service. kube-apiserver
authenticates the caller, checks RBAC and API Priority and Fairness, and
forwards the call to storage. Storage then checks every read a tool makes
against the caller's RBAC. The endpoint is off by default.

Design: [storage-mcp-endpoint.md](https://github.com/kubescape/designs-and-proposals/blob/main/proposals/storage-mcp-endpoint.md).

## Enabling it

With the kubescape-operator chart:

```yaml
storage:
  mcp:
    enabled: true            # serve the endpoint
    readerClusterRoles: true # create the two ClusterRoles below (default)
```

Without the chart, set `"mcp": {"enabled": true}` in storage's `config.json`.

## Granting access

An agent needs two kinds of permission:

1. `create` and `get` on `mcpservers/mcp` in the namespace of the URL.
   kube-apiserver checks these before it forwards the call. An MCP call is a
   `POST` (`create`). Clients built on the MCP TypeScript SDK, such as Gemini
   CLI, also open an optional `GET` stream after they connect: storage answers
   it with 405, which they accept, but without `get` kube-apiserver answers 403
   and the client disconnects before it lists the tools.
2. `get` and `list` on the resources a tool reads, in each namespace it reads.
   Storage checks this for every read with a SubjectAccessReview.

The chart creates two ClusterRoles for this:

| ClusterRole | Grants |
|---|---|
| `kubescape-mcp-reader` | `create`, `get` on `mcpservers/mcp`; `get`, `list` on `vulnerabilitymanifests` and `workloadconfigurationscans` |
| `kubescape-mcp-runtime-reader` | `get`, `list` on `containerprofiles` and `sbomsyfts` (runtime behaviour and full package inventories, so bound separately) |

Bind them with RoleBindings, one per namespace the agent may use. For an agent
running as `demo/agent` that calls the endpoint in `demo` and reads Kubescape's
results in `kubescape`:

```sh
kubectl -n demo create rolebinding agent-mcp --clusterrole=kubescape-mcp-reader --serviceaccount=demo:agent
kubectl -n kubescape create rolebinding agent-mcp --clusterrole=kubescape-mcp-reader --serviceaccount=demo:agent
# only if the agent should see SBOMs and container profiles:
kubectl -n kubescape create rolebinding agent-mcp-runtime --clusterrole=kubescape-mcp-runtime-reader --serviceaccount=demo:agent

kubectl auth can-i create mcpservers.spdx.softwarecomposition.kubescape.io --subresource=mcp -n demo --as=system:serviceaccount:demo:agent
```

Vulnerability manifests and SBOMs are stored in Kubescape's namespace (usually
`kubescape`). Configuration scans and container profiles are stored in the
namespace of the workload.

## Calling it

The endpoint speaks MCP's streamable HTTP transport, stateless and without
streaming: every JSON-RPC request is one `POST` with one JSON answer. Any MCP
client that supports streamable HTTP works with three settings:

- **URL:** the kube-apiserver address plus the path above. In a pod that is
  `https://kubernetes.default.svc/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/<namespace>/mcpservers/default/mcp`.
- **Header:** `Authorization: Bearer <ServiceAccount token>`. In a pod, the
  projected token at `/var/run/secrets/kubernetes.io/serviceaccount/token`.
- **CA:** the cluster CA, so the client trusts kube-apiserver's certificate. In
  a pod it is `/var/run/secrets/kubernetes.io/serviceaccount/ca.crt`.

The `{namespace}` in the URL only decides where the `create` permission is
checked; a tool's `namespace` argument defaults to it.

With curl:

```sh
API=https://kubernetes.default.svc
URL=$API/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/demo/mcpservers/default/mcp
SA=/var/run/secrets/kubernetes.io/serviceaccount
curl -sS --cacert $SA/ca.crt -H "Authorization: Bearer $(cat $SA/token)" \
  -H "Content-Type: application/json" -H "Accept: application/json, text/event-stream" \
  "$URL" -d '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_vulnerabilities_in_manifest",
  "arguments":{"namespace":"kubescape","manifest_name":"nginx-32fdf9","min_severity":"Critical","limit":2}}}'
```

The tool's answer is JSON text in `result.content[0].text`, for example
`{"total":72,"returned":2,"truncated":true,"next_offset":2,"vulnerabilities":[...]}`.

With [Gemini CLI](https://github.com/google-gemini/gemini-cli) from a laptop
(`.gemini/settings.json`; `NODE_EXTRA_CA_CERTS` points at the cluster CA,
and the token comes from `kubectl -n demo create token agent`):

```json
{
  "mcpServers": {
    "kubescape": {
      "httpUrl": "https://127.0.0.1:6443/apis/spdx.softwarecomposition.kubescape.io/v1beta1/namespaces/demo/mcpservers/default/mcp",
      "headers": { "Authorization": "Bearer $KUBESCAPE_MCP_TOKEN" }
    }
  }
}
```

## Tools

All tools only read. The `namespace` argument defaults to the namespace in the
URL.

| Tool | Arguments (required in bold) | Reads |
|---|---|---|
| `list_vulnerability_manifests` | `namespace`, `level` (`image`, `workload` or `both`), `continue` | `vulnerabilitymanifests` |
| `list_vulnerabilities_in_manifest` | **`manifest_name`**, `namespace`, `min_severity`, `fixable_only`, `include_details`, `limit`, `offset` | `vulnerabilitymanifests` |
| `list_vulnerability_matches_for_cve` | **`manifest_name`**, **`cve_id`**, `namespace` | `vulnerabilitymanifests` |
| `list_sboms` | `namespace`, `continue` | `sbomsyfts` |
| `list_sbom_packages` | **`sbom_name`**, `namespace`, `name`, `package_type`, `limit`, `offset` | `sbomsyfts` |
| `list_configuration_security_scan_manifests` | `namespace`, `continue` | `workloadconfigurationscans` |
| `get_configuration_security_scan_manifest` | **`manifest_name`**, `namespace`, `status`, `min_severity` | `workloadconfigurationscans` |
| `list_container_profiles` | `namespace`, `continue` | `containerprofiles` |
| `get_container_profile` | **`profile_name`**, `namespace` | `containerprofiles` |

`list_*` tools return one page; pass the returned `continue` (or
`next_offset` as `offset`) to get the next one. The tool names are the ones
`kubescape mcpserver` uses for the same data.

## Errors

| What happened | What the caller gets |
|---|---|
| No `create` on `mcpservers/mcp` in the URL's namespace | HTTP 403 from kube-apiserver |
| A `GET` (the optional stream) | HTTP 405 from storage, or 403 from kube-apiserver without `get` |
| The call did not come through kube-apiserver, or is anonymous | HTTP 401 from storage |
| Request body over 64 KiB | HTTP 413 |
| A tool read the caller may not do | tool error `RBAC_DENIED`, with kube-apiserver's reason |
| The object does not exist | tool error `RESOURCE_NOT_FOUND` |
| A bad argument | tool error `INVALID_ARGUMENT` |

A tool error is a normal MCP result with `isError: true` and the code and
message as JSON text, so the agent can read it and try again.

## Audit

kube-apiserver's audit log has the `create mcpservers/mcp` request. Storage adds
one log line per tool call with the caller, the tool, every read it checked and
why it was allowed or denied, the duration, the response size and the result:

```json
{"level":"info","msg":"mcp tool call","user":"system:serviceaccount:demo:agent","tool":"list_vulnerabilities_in_manifest","namespace":"demo","reads":[{"verb":"get","resource":"vulnerabilitymanifests","namespace":"kubescape","name":"nginx-32fdf9","allowed":true,"reason":"RBAC: allowed by RoleBinding \"demo-agent-kubescape-mcp-reader/kubescape\" of ClusterRole \"kubescape-mcp-reader\" to ServiceAccount \"agent/demo\""}],"duration":"26.623595ms","responseBytes":592,"result":"ok"}
```

## Kagent

Kagent's `RemoteMCPServer` can point at the URL above. As of Kagent v1.0.0 its
TLS settings only have `disableVerify` and no way to trust the cluster CA, so
it cannot verify kube-apiserver's certificate yet.

## Tests

- `pkg/mcp/endpoint/endpoint_test.go`: calls not forwarded by kube-apiserver
  and anonymous calls refused, the access check of every read for each kind,
  and the request body limit.
- `pkg/mcp/tools/*_test.go`: each tool's arguments, paging, filters and errors;
  `golden_test.go` pins the tool list and each tool's output.
