# ContainerProfile locking hardening (fail-fast lock backstop)

## Summary

Every lock/rlock acquisition in `pkg/registry/file/storage.go` (`CreateWithConn`,
`DeleteWithConn`, `GetWithConn`, the `get()` noLock path, `GuaranteedUpdateWithConn`, and
`appendGobObjectFromFile`'s list path) is now bound to a hardcoded **5s** child context
(`lockTimeout`). If the per-key `MapMutex` (`pkg/utils/mutex.go`) cannot be acquired within
that window, the request fails fast with `apierrors.NewServerTimeout` (HTTP 500, Reason
`ServerTimeout`, `Details.RetryAfterSeconds = 1`) instead of the previous
`apierrors.NewTimeoutError(msg, 0)`.

The per-key `MapMutex` primitive itself is unchanged and confirmed correct; this is purely a
bound on how long a caller waits for it.

## Why it matters

Before this change, a contended lock acquisition had no independent backstop and could block
up to the outer apiserver request deadline (~60s). The caller then saw a plain request timeout
with **no `Retry-After` signal**, so client-go had nothing to base a backoff decision on —
under sustained contention this produced retry storms rather than smooth backoff.

`apierrors.NewServerTimeout(..., retryAfterSeconds=1)` sets `Details.RetryAfterSeconds`, which
the apiserver turns into a `Retry-After: 1` HTTP header. client-go's retry logic honors that
header, so contended requests now fail in ~5s with an explicit "retry in 1s" signal instead of
hanging silently for up to a minute.

## How it works

- `lockTimeout` (`pkg/registry/file/storage.go`) is a package-level `var`, not a `const`, set
  to `5 * time.Second`. It is never mutated at runtime; unit tests override it to a few
  milliseconds to exercise the real timeout path without a real 5s wait.
- `newLockTimeoutError(op, key, err)` builds the `apierrors.NewServerTimeout` error, logging the
  underlying `err` (e.g. `context.DeadlineExceeded`) at Debug alongside `op`/`key` so a
  contended-vs-cancelled acquisition can be told apart post-hoc.
- Each entry-acquisition call site wraps the caller's `ctx` in
  `context.WithTimeout(ctx, lockTimeout)` before calling `s.locks.Lock`/`RLock`, and returns
  `newLockTimeoutError(...)` on failure instead of the previous `apierrors.NewTimeoutError`.
- **Migration-path re-acquisitions are intentionally left unbounded/unchanged.** The gob
  external-migration retry logic inside `get()` and the write-lock upgrade inside
  `appendGobObjectFromFile` re-acquire a lock to run (or restore) a migration and must keep
  their existing `fmt.Errorf(...)` error returns — a timeout on a lock-*restore* path could
  leave lock accounting unmatched (e.g. a caller's deferred `RUnlock` left without a
  corresponding acquire). Correctness beats fail-fast on those specific paths.

## Scope / limitations

- The 5s bound and the 1s `Retry-After` value are hardcoded defaults, not operator-configurable.
- This only bounds *storage-layer* lock acquisition inside this process. It does not change
  node-agent's own workqueue-requeue/backoff behavior on receiving the error — that is a
  separate-repo concern and out of scope here.
- The existing `lockDuration > 1s` Debug logs at each acquisition site
  (`storage.go`, e.g. in `CreateWithConn`/`DeleteWithConn`/`GetWithConn`/`GuaranteedUpdateWithConn`)
  remain the observability hook for correlating slow-but-successful acquisitions against the new
  fail-fast timeouts.

## Verifying

- Unit tests in `pkg/registry/file/storage_test.go`:
  - `Test_newLockTimeoutError` pins the status/reason/`RetryAfterSeconds` shape of the error
    directly.
  - `TestStorageImpl_LockContentionReturnsServerTimeout` overrides `lockTimeout` to
    `10 * time.Millisecond`, holds a key's write lock, and asserts a concurrent `Get` on the
    same key returns `apierrors.IsServerTimeout(err) == true` with HTTP-mapped status 500 and
    `RetryAfterSeconds == 1`.
- In production, look for the `lock acquisition timed out` Debug log line (op/key/underlying
  err) correlated with client-observed HTTP 500s carrying a `Retry-After` header, instead of
  requests hanging for the full request deadline.
