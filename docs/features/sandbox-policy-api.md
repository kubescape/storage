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
  policy requires that selector explicitly: `{}` matches all namespaces, while
  omission and null are invalid. There is no inferred agent-namespace exemption.
- Selector shape/operators are schema-validated; full native Kubernetes
  qualified-key and label-value syntax is a consumer check. Use
  `ValidateSandboxPolicySpec` / `ValidateClusterSandboxPolicySpec` before compiling
  typed policies; these call Kubernetes' own selector validator and the shared
  semantic checks. They complement, not replace, API-server schema validation.
  The API does not invent maximum selector-map/list counts to accommodate CEL
  string-pattern cost estimates.
- `containers: ["*"]` means regular containers only. Init/ephemeral container
  names must be explicit, including Kubernetes native sidecars declared in
  `initContainers`. Schema cannot inspect the selected pods to check names.
- Policy modes: `audit`, `enforce`, `off`. Rule modes: `audit`, `enforce`, or
  omission to inherit. Tripwire is an effective tier/mode reported by a consumer,
  not an authored mode. Off disables the entire policy, including rule overrides. Quote `mode: "off"`
  in YAML: YAML 1.1 tooling can otherwise interpret `off` as a boolean.
- Every present section requires an explicit default. Network defaults require
  both `egress` and `listen`; missing sections introduce no defaults or rules.
  File sections require **exactly one** of scalar `default: allow` / `default: deny`
  or `defaultByAccess`, a complete object specifying `read`, `write`, `create`,
  `delete`, `rename`, `chmod`, and `execMap`, each `allow` or `deny`. Admission
  validates both forms structurally and rejects missing, incomplete, invalid, or
  simultaneous defaults. The former object-valued `file.default` is not accepted;
  see migration below.
- Rules are map-lists keyed by `name`, conditions by `type`, and degraded entries
  by `rule`. Rule names are limited to 256 characters so status identifiers can
  remain bounded without being silently shortened.
  `ValidatePolicySpec` additionally rejects duplicate names **across sections**.
- Integer ports are schema-bounded to 1..65535. String ports must have the form
  `a-b` with at most five digits per endpoint; `ValidatePolicySpec` checks endpoint
  bounds and ordering. CIDR strings use the OpenAPI `cidr` format for baseline admission validation,
  including IPv4/IPv6 prefix bounds and required masks. The same Go function
  additionally enforces strict CIDR syntax with `netip.ParsePrefix`: Kubernetes
  1.28 format validation accepts legacy IPv4 leading zeros that the strict consumer
  check rejects. Admission and consumer validation are intentionally not identical.
  CIDR CEL validation uses `isCIDR()` only from Kubernetes 1.31 onward, whereas
  this API targets Kubernetes 1.28 or newer; no such CEL call is generated.
  Kubernetes CEL cost estimates for ordered string-port ranges would force arbitrary limits on the
  enclosing rule and port lists; those limits are not part of this design.
- As with other CRDs, unknown fields normally follow Kubernetes pruning rules.
  Authors should use strict field validation to catch misspelled predicates;
  schema acceptance alone must not be treated as a compiled-policy guarantee.
- Optional predicate booleans are pointers: absent means unconstrained, `false`
  is an explicit predicate. Lists within a predicate are ORed; fields are ANDed.
- `actor.ancestor.depth` is bounded to 1..16, matching the current exec backend's
  bounded ancestry lookup. Unsupported predicates still require consumer readiness
  reporting; schema acceptance is not a promise of runtime support.
- Privilege targets contain exactly one primitive: `syscall`, `capability`,
  `mountFsType`, or `pathClass`. `flags` may qualify `syscall` only. Independent
  hooks cannot satisfy an AND across different primitives. Use separate rules.
  Capability names use Linux names without the `CAP_` prefix, e.g. `SYS_ADMIN`.
  Identity targets contain exactly one primitive family: `path`/`pathClass`
  (which may be combined), `imds`, or `envRead`. Privilege `path`, `fromImage`,
  `fsType`, and identity `fromImage`/`fsType` are explicitly rejected, not pruned.
- Empty class names are rejected by CEL and `ValidatePolicyClasses`, including
  inline policy classes. Empty class sets are valid configuration. Resolving classes, DNS/globs,
  detecting shadowed allows, and deciding readiness/degradation are future
  consumer responsibilities. No class contents are implicitly injected by these
  API types. Inline path/destination class additions are policy-local, not a
  shared per-pod mutation affecting other matching policies. The classes example is illustrative, not a deployed built-in set.
- `kubescape.io/managed-by: armo` is metadata only. **No ownership webhook is
  included**, so this annotation does not prevent human edits. Consumers and
  backend authors must not claim single-writer protection until admission exists.
- Per-node compile counts, tiers, degradation, restart requirements, last-good/
  stale/error information, shadowed allows and hit counts live in separate
  `SandboxPolicyNodeStatus` resources. Status types do not implement writers.

## Composition and migration

Matching policies contribute a union of rules: matching deny wins, then matching
allow, then deny-by-default if any applicable section defaults to deny. This is
not most-specific-policy precedence. An allow from another policy can therefore
permit an operation otherwise blocked only by a baseline default deny; explicit
baseline denies still win. Ownership of a baseline object alone is not an
unalterable effective-policy ceiling. Enforce authoring boundaries with RBAC and,
where required, an ownership admission policy; this API adds no such webhook.

Scalar `file.default: allow` / `deny` remain wire-compatible. Experimental objects
with object-valued `file.default` must migrate that object to `file.defaultByAccess`.
Inventory existing objects with an **unstructured** client first: the old schema
could admit malformed values that break a typed list. New admission does not repair
already stored objects. Do not blindly write `defaultByAccess` while the old CRD
schema is installed: it requires `default` and may prune the new field. Plan a
staged schema/data migration, or controlled recreation of experimental objects,
with enforcement/last-good behavior reviewed for the affected consumers. Remove
or convert all old object-form and malformed entries before deploying new typed
consumers; explicitly add cluster `namespaceSelector` (use `{}` only when all
namespaces are intended) and split cross-primitive targets. No migration,
deployment, deletion, or withdrawal is performed by this API change.

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
  Keep report create/update and status permissions separate from tenant policy
  authoring permissions: a tenant able to forge reports can spoof readiness.
  Moving all reports to an agent namespace is not an RBAC-only change: namespaced
  policy references and same-namespace owner/garbage-collection semantics would
  need a separately designed API migration.

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
