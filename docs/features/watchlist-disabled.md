# WatchList disabled (streaming list / `sendInitialEvents`)

## Summary

The storage apiserver **disables the `WatchList` feature gate** (`feature-gates=WatchList=false`,
set in `pkg/cmd/server/start.go`). As a result it **rejects `watch?sendInitialEvents=true`
requests pre-stream with HTTP 422**, and WatchList-capable clients fall back to legacy
`LIST` + `WATCH`.

This is deliberate. The file/SQLite-backed storage has **no Cacher** and its watch path
(`pkg/registry/file/watch.go`) only forwards *future* events — it never replays current
state and **never emits the terminal `k8s.io/initial-events-end` BOOKMARK** that the
WatchList (streaming-list) protocol requires. If the server accepted such a request, the
client's reflector would block forever awaiting that bookmark.

## Why it matters

Kubernetes controllers that build **metadata informers** (`PartialObjectMetadata` LIST/WATCH)
use WatchList by default once `WatchListClient` is on (client-go ≥ v0.35 ships it Beta /
default-on; control planes from k8s ~1.32 enable it). Without this fix, those informers hang
against the spdx aggregated API, which has two known, severe consequences:

- **ResourceQuota controller stall.** The quota controller blocks `WaitForCacheSync` on
  *every* discovered namespaced list+watch resource. One spdx monitor that never syncs
  freezes **all** quota replenishment cluster-wide, so quotas stop decrementing and
  eventually behave as if at their hard limit. (Upstream context:
  [kubernetes/kubernetes#133737](https://github.com/kubernetes/kubernetes/issues/133737),
  which names `spdx.softwarecomposition.kubescape.io/v1beta1`.)
- **Rancher cattle-agent tight retry loop** ([#318](https://github.com/kubescape/storage/issues/318)):
  reflectors awaiting the missing bookmark, plus pre-closed watch channels, drove a hot
  re-watch loop.

## How it works

1. **Effective gate ordering.** `flags.Set("feature-gates", "WatchList=false")` must run
   **before** `ComponentGlobalsRegistry.Set()` — that registry call is the only bridge that
   propagates the parsed flag into `utilfeature.DefaultMutableFeatureGate`. If the order is
   reversed (as it was before [#330](https://github.com/kubescape/storage/pull/330)), the
   override is a silent no-op and the gate stays at its default (**enabled**). The apiserver's
   watch handler then validates `ListOptions` against the now-disabled gate and returns 422
   for `sendInitialEvents`, so clients use legacy list+watch instead.

   > `ServerSideApply` is GA / non-gated in Kubernetes 1.35; it must **not** appear in the
   > `feature-gates` override or the server fails to boot with `unrecognized feature gate`.

2. **Idle watches instead of pre-closed channels.** `watch.NewEmptyWatch()` returns a
   *pre-closed* channel; a reflector reading it sees 0 events in <1s → `VeryShortWatchError`
   → immediate re-watch → tight loop, in both legacy and streaming modes. The namespaced
   path in `StorageImpl.Watch` and `immutableStorage.Watch` (ConfigurationScanSummary,
   VulnerabilitySummary, GeneratedNetworkPolicy) return an `idleWatch` instead: a
   zero-goroutine `watch.Interface` that stays open and event-free until client disconnect
   or `Stop()`. The real `watchDispatcher` path for cluster-scoped resources is unchanged.

## Scope / limitations

- This does **not** implement WatchList / `sendInitialEvents` semantics for the resources
  served by the real watch dispatcher (tracked in
  [#320](https://github.com/kubescape/storage/issues/320)). Correctness relies on the
  effective `WatchList=false` producing the pre-stream 422 fallback.
- Plain (non-streaming) `LIST` and `WATCH` are unaffected and continue to work normally.

## Verifying

- Server logs (debug): `effective feature gates ... WatchList=false`.
- A `ResourceQuota` in a namespace should have a populated `status.used` / `status.hard`
  (not `status: {}`), and the kube-controller-manager should stop logging
  `timed out waiting for quota monitor sync`.
- Tests: `pkg/cmd/server/start_test.go` (gate effective + 422 decision point) and
  `pkg/registry/file/idlewatch_test.go` (idle watch stays open / closes correctly).
