# CollapseConfiguration cache refresh no longer stalls consolidation

## Summary

The `CollapseSettingsProvider` returned by `NewCRDCollapseSettingsProvider`
(`pkg/registry/file/collapse_config_provider.go`) is now stale-while-revalidate: the
cache is primed once at wiring time, every call is a single atomic load, and an expired
cache is refreshed by one background goroutine. The caller's goroutine never takes a pool
connection, a lock or a SQLite statement.

Before this change an expired cache was refreshed synchronously on the caller's goroutine
with a `storage.Interface.Get` on a **second** pool connection. The only caller,
`ContainerProfileProcessor.PreSave`, runs on the consolidation path from inside
`GuaranteedUpdateWithConn`, in an open transaction whose connection already holds SQLite's
write lock. With no `CollapseConfiguration/default` CR applied, `get()` found no payload
and issued `DeleteMetadata` on the second connection — a write that needs the lock the
same goroutine holds. It waited in SQLite's busy handler for the full busy timeout
(`DefaultBusyTimeout`, 60 s), holding the WAL writer lock the whole time, so every shard
commit (every node-agent TS `Create`) and every other consolidation worker queued behind it.

## Why it fired on every tick

- Only consolidated (non-TS) profile saves reach the provider: `PreSave` returns early for
  TS profiles, so node-agent's `Create`s never refreshed the cache.
- The consolidation interval (30 s) exceeds the cache TTL (10 s), and the expiry is stamped
  when the (60 s) refresh finishes.
- Therefore, whenever no CR was applied and TS data existed, the storage process spent
  ~60 s of every ~90 s with the write lock held by a goroutine waiting on itself.

Live since the provider was introduced (`2ea734d9`, v0.0.291); `6abe45da` (v0.0.297) added
the TTL cache, which reduced the frequency from per-save to per-tick without removing the
mechanism.

## How it works now

- `NewCRDCollapseSettingsProvider` calls `refresh()` once synchronously at construction.
  The wiring goroutine (`pkg/apiserver/apiserver.go`) holds no transaction, so that read
  cannot wait on itself.
- The returned closure loads the cached entry; if it is older than `collapseSettingsTTL`
  and no refresh is in flight (`atomic.Bool` CAS), it starts one background refresh and
  returns the cached value. The next call after the refresh completes sees the new value.
- Staleness bound is therefore **TTL + one call** instead of TTL. Deflate tolerates eventual
  consistency (next-deflate correctness is not critical), which is the tradeoff the
  provider already documented.
- The TTL is captured once at construction; the background goroutine never reads the
  package-level `collapseSettingsTTL` (tests shrink it before constructing a provider).

## Tests

Real `StorageImpl` + real pool with a 2 s busy timeout, asserting `< 500 ms`
(`pkg/registry/file/collapse_settings_self_stall_test.go`):

- `TestCRDCollapseSettingsProvider_NoStorageIOUnderHeldWriteLock` — caller holds the WAL
  writer lock in an open transaction, cache expired, no CR. Before: 2.004 s.
- `TestCRDCollapseSettingsProvider_NoLockWaitOnCallerGoroutine` — caller holds the CR
  key's per-key write lock (`lockTimeout` shrunk to 1 s). Before: 1.000 s. Keeps guarding
  the boundary independently of what `get()` does on a miss.
- `TestConsolidateTimeSeries_DoesNotStallOnCollapseRefresh` — the production chain
  (`ConsolidateTimeSeries` → `PreSave` → provider) with the real provider wired. Before:
  2.005 s.
- `TestNewCRDCollapseSettingsProvider_LiveUpdate` and `_RefreshesAfterTTLExpiry` now use
  `assert.Eventually` for the TTL-plus-one-call semantics.

## Operational notes

- Applying a `CollapseConfiguration/default` CR was, and remains, a full mitigation on older
  versions: with a CR present the refresh is a pure read.
- Design and root-cause trace: `.omc/plans/collapse-settings-self-stall.md`.
