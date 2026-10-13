# Sandbox policy API foundation

`pkg/apis/sandbox/v1alpha1` defines four Kubernetes-served resources in
`kubescape.io/v1alpha1`:

| Kind | Scope | Status subresource |
| --- | --- | --- |
| `SandboxPolicy` | Namespaced | Yes |
| `ClusterSandboxPolicy` | Cluster | Yes |
| `SandboxPolicyClasses` | Cluster | No |
| `SandboxPolicyNodeStatus` | Namespaced | Yes |

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
- Rules are map-lists keyed by `name`, conditions by `type`, and degraded entries
  by `rule`. Rule names are limited to 256 characters so status identifiers can
  remain bounded without being silently shortened.
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
- Per-node compile counts, tiers, degradation, restart requirements, last-good/
  stale/error information, shadowed allows and hit counts live in separate
  `SandboxPolicyNodeStatus` resources. Status types do not implement writers.

## Per-node status contract (review-approved design amendment)

The original design embeds `status.nodes` in each policy. The author explicitly
selected separate node-status resources during PR review instead: the shared
object would grow as nodes × rules and eventually prevent status updates.
Neither policy kind now contains a node list or per-node data. Its status keeps
only `observedGeneration`, bounded conditions, `lastAggregated`, and `nodeSummary`
counts (`total`, `reporting`, `ready`, `degraded`, `stale`). Adding cluster nodes
creates independent report objects, not a larger shared policy object.

### Identity, scope and lifecycle

- `SandboxPolicyNodeStatus.spec.policyRef` requires policy kind, name and UID;
  `nodeRef` requires node name and UID. The entire spec is immutable. A report
  cannot be retargeted to a different policy, node, or recreated object.
- For `SandboxPolicy`, create the report in the **policy's namespace**. Its
  policy reference has no namespace field and resolves only there. For
  `ClusterSandboxPolicy`, create reports in the configured **agent namespace**.
  Use a single configured namespace consistently for cluster-policy reports.
- Writers use `NodeStatusName(policyUID, nodeUID)` as the resource name. This
  hashes a length-prefixed identity into a fixed DNS-safe name. Each node writes
  only its own report's status; it does not contend on the parent policy object.
  Spec immutability does not enforce uniqueness across separately named objects;
  writers must use the canonical name, and readers must reject noncanonical names.
- Set a policy `ownerReference` with the same kind/name/UID as `policyRef`.
  Namespaced dependents can reference a same-namespace policy or a cluster policy,
  consistent with [Kubernetes ownership rules](https://kubernetes.io/docs/concepts/architecture/garbage-collection/).
  Do not add a second Node owner: either surviving owner would retain the report.
  A future controller must remove reports for deleted/recreated nodes and ignore
  old node/policy UIDs. Normal policy deletion can garbage-collect owned reports;
  orphan deletion requires explicit controller cleanup.
- These are still normal CRDs, stored by Kubernetes in etcd, not SQLite/files.
  Neither admission nor these API types resolve referenced objects or authenticate
  the reporting node. Runtime identity validation and least-privilege writer RBAC
  remain prerequisites for trustworthy enforcement status, outside this slice.

### Bounded snapshots and honest truncation

Per report, schema limits are 64 entries **per** rule-detail collection
(`degraded`, `appliesOnRestart`, `shadowedAllows`, `ruleHitCounts`), 8 conditions,
256-character rule names, 128-character condition type/reason codes,
512-character diagnostic messages/degradation reasons, and 2048-character errors.
These bound status detail size, **not** policy rule count or cluster node count.
Regression tests populate every detail limit with JSON-escaped control characters
and verify the snapshot stays below 768 KiB. Keep report metadata minimal; the
usual Kubernetes whole-object request/storage limits still apply.

Future writers must emit full collection cardinalities in `status.counts`, retain
a deterministic first 64 details in rule-name order, and set `truncated: true`
when entries or diagnostic text are omitted. Admission rejects counts smaller
than stored details and omitted counted entries with `truncated: false`.
Condition lists must remain within their documented 8-entry contract; readers
must not infer Ready from missing or omitted conditions. Diagnostic text may be
shortened at Unicode-character boundaries with the truncation flag set; stable
rule identifiers and condition codes must never be silently shortened. Full
per-rule telemetry belongs in the event/metrics pipeline, not this snapshot.
The report is replaced, not append-only, so it does not grow over time.

A future **single aggregation controller** writes policy summaries, not each node.
`total` counts eligible nodes; `reporting` counts canonical, fresh reports for live
policy/node UIDs and the policy's current generation. Per-node `observedGeneration`
refers to the **parent policy generation**, not the report object's generation,
and records the latest processed attempt, including a failed compile. On failure,
`stale` is true and `compiled`/`lastCompiled` retain last-good information. The
required `lastReported` heartbeat timestamps the snapshot independently of
`lastCompiled`; reports past the configured freshness deadline are not current. Nodes without a current report are
not Ready. `ready` counts explicit `Ready=True`, non-stale reports; `degraded`
and `stale` are independent categories and may overlap. Read degradation from the
full counts, not the truncated list. Every categorized count must be at most
`reporting`, which must be at most `total`; Ready and stale are disjoint
(`ready <= reporting - stale`). Restart and shadowed-rule detail lists are sets,
so duplicate names cannot consume bounded slots or inflate cardinalities. Match `observedGeneration` and check
`lastAggregated` freshness before trusting the summary. A generation change,
removed node or missing report must never leave an old Ready count treated as
current. Aggregation, cleanup, freshness expiry and status writes are deliberately
not implemented by this API-only PR.

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
Four `*.example.yaml` files exercise the API; they are not installed by the chart.
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
