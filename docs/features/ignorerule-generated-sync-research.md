# Issue #455: IgnoreRule generated synchronization

Research date: 2026-10-30. This note covers the selected IgnoreRule-only
contribution associated with [issue #455](https://github.com/kubescape/storage/issues/455).
It does not implement or close the wider typed HotSpot stack-evidence proposal,
and it does not claim maintainer acceptance of a split, deployment, or release.

## Existing contract and the synchronization gap

The internal and versioned `IgnoreRule` types already carry three provenance
fields (`SourceKind`, `SourceName`, `SourceNamespace`) and two reason fields
(`Justification`, `ImpactStatement`). The versioned JSON names are `sourceKind`,
`sourceName`, `sourceNamespace`, `justification`, and `impactStatement`; their
existing protobuf field numbers are 4 through 8. These are optional string
fields, not a new API introduced by this contribution. Sources:
[internal types](../../pkg/apis/softwarecomposition/grype_types.go),
[versioned types](../../pkg/apis/softwarecomposition/v1beta1/grype_types.go),
and [protobuf schema](../../pkg/apis/softwarecomposition/v1beta1/generated.proto).

The identified gap was the generated apply-configuration builder: callers could
not express those five existing fields through its typed fluent API. The scoped
repair synchronizes `IgnoreRuleApplyConfiguration` and its five `With...`
methods with the owning type. Source:
[apply configuration](../../pkg/generated/applyconfiguration/softwarecomposition/v1beta1/ignorerule.go).

The existing protobuf implementation, bidirectional API conversions, and
OpenAPI definition already include these fields. They must be inspected when
checking generated consistency, but need no unrelated rewrite for this gap.
Sources: [protobuf implementation](../../pkg/apis/softwarecomposition/v1beta1/generated.pb.go)
(`IgnoreRule` marshal, size, and unmarshal methods),
[conversions](../../pkg/apis/softwarecomposition/v1beta1/zz_generated.conversion.go)
(`IgnoreRule` conversion functions), and
[OpenAPI](../../pkg/generated/openapi/zz_generated.openapi.go)
(`schema_pkg_apis_softwarecomposition_v1beta1_IgnoreRule`).

## Presence and compatibility

Apply configurations use `*string` fields with `omitempty`: an unset pointer
omits the JSON key, whereas calling a setter with `""` sends an explicit empty
string. Setters return the same builder for chaining and repeated calls retain
the last supplied value. This presence distinction belongs to the declarative
request; the existing API object's plain string fields do not retain an
unset-versus-empty distinction after decoding. Sources:
[apply configuration](../../pkg/generated/applyconfiguration/softwarecomposition/v1beta1/ignorerule.go)
and [versioned API](../../pkg/apis/softwarecomposition/v1beta1/grype_types.go).

Preserve the original vulnerability, fix-state, and package fields and the
existing `IgnoredMatch.appliedIgnoreRules` nesting. No storage migration,
ContainerProfile behavior change, or HotSpot schema is part of this subset.
Sources: [IgnoreRule and IgnoredMatch types](../../pkg/apis/softwarecomposition/v1beta1/grype_types.go)
and [nested apply builder](../../pkg/generated/applyconfiguration/softwarecomposition/v1beta1/ignoredmatch.go).

## Generation and verification

The module pins `k8s.io/code-generator` to v0.35.0, and the repository generation
script enables apply-configuration generation. Use that pinned generator with
`GOFLAGS=-mod=readonly` and a temporary output directory, then compare and copy
only the intended generated result. Do not edit dependencies or regenerate
unrelated artifacts to repair this builder. Sources:
[module dependencies](../../go.mod) and
[generation script](../../hack/update-codegen.sh).

Regression coverage checks every added field for omission, nonempty values,
explicit empty strings, chaining, and last-call-wins behavior. It also checks
JSON decoding into the existing API type and nesting under `IgnoredMatch`.
Source: [apply regression tests](../../pkg/generated/applyconfiguration/softwarecomposition/v1beta1/ignorerule_test.go).
Existing JSON, deepcopy, and protobuf round-trip tests provide complementary
coverage: [API tests](../../pkg/apis/softwarecomposition/v1beta1/grype_types_test.go).

Run targeted apply/API tests and broader repository validation, recording actual
results separately. The checkout's default Go test invocation encountered
inconsistent vendoring during the initial investigation; use read-only module
resolution for validation without changing the vendor tree. This note records
the verification requirements and observed baseline limitation, not a claim
that all tests or server-side apply integration have passed.
