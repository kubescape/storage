# ContainerProfile: new profiles now get a metadata.uid

## Summary

`ContainerProfile` objects created for the first time by the consolidation
processor (`pkg/registry/file/containerprofile_processor.go`'s
`loadOrInitializeProfile`) never had `metadata.uid` set — it stayed
permanently empty. `k8s.io/apiserver`'s generic PATCH handler treats an
object with no UID as "doesn't really exist" and refuses to apply the patch,
so any standard Kubernetes PATCH against an existing ContainerProfile
(`kubectl annotate`, `kubectl label`, `kubectl edit`, any JSON-merge-patch or
strategic-merge-patch client) returned `404 NotFound`. See
[issue #385](https://github.com/kubescape/storage/issues/385).

## Why it matters

A standard REST `Create` gets a UID for free via
`rest.FillObjectMetaSystemFields`/`uuid.NewUUID()`. `loadOrInitializeProfile`
bypasses that entirely: it writes directly through `storage.Interface` (via
`SaveContainerProfile` → `GuaranteedUpdate`/`GuaranteedUpdateWithConn` with
`ignoreNotFound=true`), never through `k8s.io/apiserver`'s generic Create
path, so nothing else ever assigned one. This was confirmed live (delve
attached to a running deployment) against real data on `armo-dev-stage`: a
plain `kubectl get` on an existing ContainerProfile succeeds, but
`kubectl annotate` on the exact same object 404s, because
`vendor/k8s.io/apiserver/pkg/endpoints/handlers/patch.go`'s `hasUID` check
sees an empty `ObjectMeta.UID`.

This is **not** specific to Phase 4's `genericrest.Store` migration
(`docs/features/generic-rest-storage-phase4.md`) — both the old
`genericregistry.Store`-based REST and the new `genericrest.Store`-based REST
go through the same vendored PATCH handler and fail identically. It also
does not affect `Get`/`List`/`Watch`, `Create`, or the node-agent's actual
write path (`SaveContainerProfile`) — none of those check UID.

## How it works

- `loadOrInitializeProfile`'s new-profile branch now sets
  `ObjectMeta.UID: uuid.NewUUID()` (`k8s.io/apimachinery/pkg/util/uuid`,
  matching what `rest.FillObjectMetaSystemFields` does for a standard REST
  Create), alongside the existing `Namespace`/`Name`/`Annotations`/`Labels`
  fields.
- No other field changes: `CreationTimestamp` was and remains unset by this
  path (a separate, narrower gap than issue #385 — not addressed here since
  it isn't the cause of the PATCH failure and issue #385 didn't ask for it).

## Audited: no other resource has the same gap

Every other in-repo construction of a fresh `ObjectMeta` for a
newly-initialized object was checked:

- `vulnerabilitysummarystorage.go`, `configurationscansummarystorage.go`,
  `generatednetworkpolicy.go` back onto `immutableStorage`
  (`pkg/registry/file/storage.go`), whose `Create`/`GuaranteedUpdate` are
  hard no-ops/errors — these "virtual CRD" resources don't support mutation
  at all, so the PATCH path this bug lives in is unreachable for them
  regardless of UID.
- `dynamicpathdetector/collapse_config_from_crd.go`'s `CRDFromCollapseSettings`
  builds a client-side object for an external tool (`bobctl autotune`) to
  push via a real `client-go` Create/Update call against the running
  apiserver — that goes through `k8s.io/apiserver`'s actual Create handler,
  which assigns a UID server-side regardless of what the client sent.
- `ContainerProfile`'s consolidation processor is the only code path in this
  repo that constructs a persisted object's `ObjectMeta` and writes it
  directly through `storage.Interface`, bypassing the generic REST Create
  path that would otherwise assign a UID for free.

## What this PR deliberately does not do

Per issue #385's own scope, fixing existing already-persisted
ContainerProfile objects that have no UID (every ContainerProfile in every
deployment prior to this fix) is a separate, more mechanical migration
question, not addressed here: those objects remain unpatchable via kubectl
until they are naturally recreated by the consolidation processor. This
mirrors the issue's own explicitly-acceptable fallback ("explicitly accept
that they stay unpatchable until naturally recreated") rather than shipping
an unreviewed backfill migration alongside this fix.

## Verification

`pkg/registry/file/containerprofile_processor_test.go`'s
`TestLoadOrInitializeProfile_NewProfileGetsUID` calls
`loadOrInitializeProfile` directly for two never-before-seen keys and asserts
both get a non-empty, distinct UID. Confirmed to fail against the pre-fix
code (`profile.UID == ""`) by temporarily reverting the fix and rerunning,
then confirmed to pass again with the fix restored. Full `pkg/registry/file`
suite passes under `-race` (aside from the known pre-existing, unrelated
`TestFileSystemStorageWatchReturnsDistinctWatchers`/
`TestFilesystemStorageWatchPublishing` race in the Watch dispatcher,
independently reproduced on unmodified `main` too).
