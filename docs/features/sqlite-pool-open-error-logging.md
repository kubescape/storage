# A database that cannot be opened is now logged

## Summary

When the SQLite metadata database (`/data/metadata.sq3`) cannot be opened, storage
now logs the reason at Error level:

```
sqlite pool - error while opening the database. error: sqlite: open "/data/metadata.sq3": sqlite: prepare: file is not a database; path: /data/metadata.sq3; suppressedSinceLastLog: 0
```

Before this change the reason was never logged, at any log level.

## Why it matters

The pool (`zombiezen.com/go/sqlite/sqlitemigration`) opens the database in a
background goroutine and retries forever when the open fails. Each `Take` asks it to
try again and then waits until the pool is ready or the caller's context ends. The
library reports the open error only through its `OnError` callback, and storage did
not set one. So the only visible effect was a context error with no cause:

- On v0.0.297 (observed) the API server starts and serves. Every request waits out
  the pool timeout and returns `ServerTimeout` ("The list operation against ... could not be
  completed at this time, please try again"), on every resource, while the pod is
  `Running`, `1/1 Ready`, and the APIService is `Available`. Clients that list every
  resource of the group wait on each one.
- On v0.0.382 (observed) startup takes a connection before serving
  (`migratePersistentStorage`, added in v0.0.356), so the process blocks there until
  the liveness probe restarts it. The pod is not ready, the APIService is unavailable, and the only log
  line is `persistent storage migration error: get sqlite connection: context canceled`.

In both cases the operator cannot tell a damaged or locked database file from
anything else.

## How it works

`NewPoolWithOptions` passes an `OnError` callback to the pool. The default callback
logs the error and the database path.

The pool retries the open on every `Take`, so the log is throttled: the first error
is logged at once, then at most one line per 30 seconds
(`openErrorLogInterval`). Each line carries `suppressedSinceLastLog`, the number of
failed attempts since the previous line.

`PoolOptions.OnOpenError` replaces the default callback. Tests use it to capture the
error.

## What does not change

- The pool still retries forever. A database that becomes openable later (for example
  a lock held by another process is released) is picked up without a restart.
- No readiness or liveness behaviour changes.
- A failure of the schema migration (as opposed to the open) was already returned to
  callers by `Take` and is not affected.

## How to reproduce

Stop storage, overwrite `metadata.sq3`, `metadata.sq3-wal` and `metadata.sq3-shm`
with random bytes, and start storage. Overwriting only `metadata.sq3` is not enough:
an intact WAL still serves every page and the database opens.

## Tests

`pkg/registry/file/sqlite_open_error_test.go`:

- `TestNewPoolReportsOpenError`: the open error of an unreadable database reaches
  `OnOpenError`, and `Take` fails on its context.
- `TestNewPoolDefaultOpenErrorReporter`: the same with the default callback.
- `TestOpenErrorThrottle`: first error reported, later ones suppressed inside the
  interval and counted.
