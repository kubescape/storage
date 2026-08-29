# Namespace-scoped watches enabled; Create/Update out param now carries Spec

## Summary

Two independent, pre-existing `StorageImpl` bugs are fixed, both shared identically by every
resource's OLD (`genericregistry.Store`-based) and NEW (`genericrest.Store`-based, see
`docs/features/generic-rest-storage-phase4.md`) REST implementation:

1. **`StorageImpl.Watch` (`pkg/registry/file/storage.go`) no longer rejects namespace-scoped
   watch requests.** Previously, any `Watch` call whose key had a non-empty namespace segment
   (i.e. any watch on a namespaced resource, whether a single-object watch or a namespace-root
   list-watch) got a permanently idle, event-free watch instead of a real one.
2. **`Create`/`GuaranteedUpdate`'s `metaOut`/`out` parameter now contains the full persisted
   object (Spec included), not just `ObjectMeta`/`SchemaVersion`.**

## Why it matters

### Namespace-scoped watches

The rejection was originally added deliberately (`bd4cac69`, "fix watch with NS", 2025-07-01) to
work around **namespaces getting stuck in `Terminating`** -- with a real watch in place at the
time, something downstream (consuming this apiserver's watch, e.g. a controller's informer)
never observed the events it needed to let namespace deletion complete. It was later reinforced
by replacing a pre-closed `watch.NewEmptyWatch()` with the current idle, never-closing
`newIdleWatch()` (`1176394`, 2026-06-05) to also stop reflectors from tight-retry-looping
(issue #318) -- but namespace-scoped watches remained non-functional either way.

`WatchDispatcher.Register`/`notify` (`pkg/registry/file/watch.go`) match purely on path-prefix
strings (`extractKeysToNotify` walks every ancestor of an object's key) -- they have never
treated a namespaced key any differently from a cluster-scoped one. A namespace-root watch key
is itself one of the path-prefix ancestors walked for any object created under that namespace,
so namespace-scoped watches dispatch through the exact same mechanism already proven correct for
cluster-scoped resources. Whatever broke the original namespace-deletion flow was not a defect
in this shared, namespace-agnostic dispatch code.

**Verified live** on a real cluster (`armo-dev-stage`, `kubescape` namespace, built from this
fix as `quay.io/matthiasb_1/storage:namespace-watch-fix`): a throwaway namespace containing a
namespaced `OpenVulnerabilityExchangeContainer` was deleted and finalized cleanly and promptly
(`kubectl delete namespace ... --wait=true`, no hang, exit 0) -- the acceptance test matching
the original incident. A `kubectl get ... -w` against the namespace also observed a real,
live `Added` event for an object created while the watch was streaming, directly confirming
event delivery (not just an idle-but-non-hanging watch).

### `metaOut`/`out` Spec

`saveObject` computed a metadata-only object (`ObjectMeta`/`SchemaVersion`, no `Spec`) for two
different purposes that actually have conflicting requirements: the SQLite metadata row (which
deliberately excludes `Spec` -- the payload lives in a separate gob file) and the REST layer's
`out` parameter (the object returned to the client from `Create`/`Update`, which must reflect
everything that was actually persisted). Because both purposes reused the same value, any client
chaining an `Update` off a `Create`/`Update` response without an intervening `Get` saw an empty
`Spec` -- which could trip resource-specific validation (surfaced via
`openvulnerabilityexchangecontainers`' `spec.Author` required check while building its Phase 4
differential test suite).

## How it works

- `StorageImpl.Watch` no longer inspects the key's namespace segment at all; every key goes
  through the same `newWatcher`/`watchDispatcher.Register` path.
- `saveObject` (`pkg/registry/file/storage.go`) now returns two values: it still fills the
  caller-supplied `metaOut` with the **full** post-mutation object (resourceVersion bumped,
  managedFields cleared, checksum annotation set -- i.e. exactly what was serialized to the
  payload file), and separately **returns** the metadata-only object for callers to use as the
  lightweight watch-event object. `CreateWithConn`/`GuaranteedUpdateWithConn` pass that returned
  value (not `metaOut`) into `watchDispatcher.Added`/`Modified`, preserving the existing
  bandwidth optimization where a watcher created without `ResourceVersionFullSpec` receives the
  reduced, Spec-free event object.
