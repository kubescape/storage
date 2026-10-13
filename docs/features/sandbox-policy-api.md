# Sandbox policy API foundation

`pkg/apis/sandbox/v1alpha1` defines three Kubernetes-served resources in
`kubescape.io/v1alpha1`:

| Kind | Scope | Status subresource |
| --- | --- | --- |
| `SandboxPolicy` | Namespaced | Yes |
| `ClusterSandboxPolicy` | Cluster | Yes |
| `SandboxPolicyClasses` | Cluster | No |

These are real CRDs, **not** resources of storage's aggregated
`spdx.softwarecomposition.kubescape.io` API. Import the package and call
`v1alpha1.AddToScheme` to register its objects with a client scheme. Dynamic
Kubernetes clients can use the plural resource names in the manifests.
No existing ContainerProfile or SeccompProfile API changes are needed.

The contract follows
`shared-designs-and-docs/projects/cloud-agent-sandbox/design/sandbox-policy-crd.md`.
All five sections (`exec`, `file`, `network`, `privilege`, `identity`) and their
shared actor predicates are represented; this does **not** mean an agent
supports them yet.

## Schema and semantic boundaries

- `selector`, `selector.podSelector`, `containers` and `mode` are required.
  Empty pod selectors intentionally match all pods in the applicable namespace(s).
  A namespaced policy cannot contain `selector.namespaceSelector`: the schema
  explicitly rejects its presence instead of silently pruning it. Only a cluster
  policy supports that selector; omission matches all namespaces.
- `containers: ["*"]` means regular containers only. Init/ephemeral container
  names must be explicit. Schema cannot inspect the selected pods to check names.
- Policy modes: `audit`, `enforce`, `off`. Rule modes: `audit`, `enforce`, or
  omission to inherit. Tripwire is an effective tier/mode reported by a consumer,
  not an authored mode. Off disables the entire policy, including rule overrides.
- Every present section requires an explicit `default`. Network defaults require
  both `egress` and `listen`; missing sections introduce no defaults or rules.
  `file.default` accepts `allow`/`deny`, or a complete object specifying `read`,
  `write`, `create`, `delete`, `rename`, `chmod`, `execMap`. Requiring all keys
  prevents an undefined fallback for omitted access kinds.
- **Kubernetes structural schemas cannot validate the string-or-object file
  default union.** Its schema preserves the value without pruning. The Go
  `FileDefault.UnmarshalJSON` / `Validate` methods reject invalid values,
  incomplete objects, and unknown object keys. Future unstructured consumers
  must perform equivalent validation before compiling; admission alone is not
  a validity guarantee for this field.
- Rules are map-lists keyed by `name`, nodes by `name`, conditions by `type`, and
  degraded entries by `rule`. This enables independent per-node status patches.
  `ValidatePolicySpec` additionally rejects duplicate names **across sections**.
- Integer ports are schema-bounded to 1..65535. String ports must have the form
  `a-b` with at most five digits per endpoint; `ValidatePolicySpec` checks endpoint
  bounds and ordering. The same function validates explicit network CIDRs.
  Kubernetes CEL cost estimates would otherwise force arbitrary limits on the
  enclosing rule and port lists; those limits are not part of this design.
- As with other CRDs, unknown fields normally follow Kubernetes pruning rules.
  Authors should use strict field validation to catch misspelled predicates;
  schema acceptance alone must not be treated as a compiled-policy guarantee.
- Optional predicate booleans are pointers: absent means unconstrained, `false`
  is an explicit predicate. Lists within a predicate are ORed; fields are ANDed.
- Empty class sets are valid configuration. Resolving classes, DNS/globs,
  detecting shadowed allows, and deciding readiness/degradation are future
  consumer responsibilities. No class contents are implicitly injected by these
  API types. The classes example is illustrative, not a deployed built-in set.
- `kubescape.io/managed-by: armo` is metadata only. **No ownership webhook is
  included**, so this annotation does not prevent human edits. Consumers and
  backend authors must not claim single-writer protection until admission exists.
- Consumer-written status includes compile counts, conditions, per-node tiers,
  degradation, restart requirements, last-good/stale/error information, shadowed
  allows and rule hit counts. Status types do not implement a status writer.

## Generation and validation

From the storage repository:

```sh
hack/update-sandbox-codegen.sh
# Also synchronize the installation chart, when working in both repositories:
hack/update-sandbox-codegen.sh --chart-crds-dir ../helm-charts/charts/kubescape-operator/crds
go test ./pkg/apis/sandbox/... ./hack/sandbox-crds
hack/verify-sandbox-crds.sh -count=1
```

`artifacts/sandboxpolicy/*.crd.yaml` are canonical generated manifests. The chart
copies live under `charts/kubescape-operator/crds/` in `kubescape/helm-charts`.
Three `*.example.yaml` files exercise the API; they are not installed by the chart.
The general `hack/verify-codegen.sh` compares generated manifests in temporary
output against committed artifacts without modifying them; regression tests
cover clean, stale, missing, obsolete and failed-generation cases.
The regular tests pin manifest/type coverage, examples, scheme registration,
JSON round trips, explicit-false predicates, deep copies and semantic validation.

The verification script runs the Kubernetes definition, structural, OpenAPI,
pruning and CEL validators without contacting a cluster. It uses an isolated
copy of the existing go.mod/go.sum with `-mod=mod`: the test-only validation
packages are in an existing dependency but not production's vendor tree. No new
dependencies, version changes or vendor updates are required. Existing YAML and
apiextensions dependencies are now classified as direct imports. Network access may be
needed if those existing modules are not cached.

Runtime consumption, compiler, eBPF/seccomp, telemetry, backend intents,
ownership admission, RBAC changes and cluster deployment are deliberately
outside this API-only slice. Installing the CRDs alone provides **no enforcement**.
