# Exec argument collapsing (design, proposed)

> Status: **proposed**. This is a design for review; no code in this PR.

## Summary

Container profiles record every distinct argv a binary runs with as a separate
`ExecCalls` entry. `DeflateContainerProfileSpec` collapses opens, endpoints and
network neighbors by threshold, but execs only go through `DeflateStringer`,
which removes exact duplicates (path plus every arg). Nothing else bounds them.

This proposal adds `dynamicpathdetector.AnalyzeExecs`. It collapses
high-variety argument positions into the exec wildcards the matcher already
supports (`⋯` for one arg, `⋯⋯` for zero or more). A new
`ExecArgsDynamicThreshold` controls it, with CRD support and per-binary
overrides.

## Why it matters

Most containers run a handful of commands, so exact dedupe is enough. Hosts
and script-heavy workloads are different. A host profile we measured had
**9,849 exec entries from only 176 binaries**: `grep` with 2,452 argv
variants, `bash` 1,200, `chmod` 922, `ls` 848, `gawk` 793. It was a host
running inventory and discovery scripts continuously (commands like
`grep bin/splunk$` or `grep com.ibm.ws.runtime.WsServer`). Execs made up 97%
of the profile's entries, pushed it past the size limit, and learning ended
with `ObjectTooLargeError`. After that the workload has no usable baseline,
so every profile-based rule is affected, not just the exec ones.

Collapsing execs by path alone would bound the size, but it would switch off
argument checking (R0040 "Unexpected process arguments" and any rule that
calls `ap.was_executed_with_args`) for every known binary. On a host, where
attacks mostly use binaries that are already installed (`bash`, `find`,
`chmod`, `systemctl`), arguments carry most of the signal. The goal is to
bound size and keep argument checks wherever the arguments are predictable.

## What already exists

The matching side supports exec wildcards today
(`pkg/registry/file/dynamicpathdetector/compare_exec_args.go`):

- `⋯` (`DynamicIdentifier`) as a whole token matches exactly one argument.
  Inside a `/`-separated token it matches exactly one segment.
- `⋯⋯` (`ExecArgsWildcard`) matches zero or more whole arguments.
- `*` is always a literal in exec args, on purpose.
- `CompareExecArgs` matches anchored at both ends. An empty profile `Args`
  means no constraint. `MatchExecArgs` adds the `ArgsRequired` strict mode.
  Learned profiles don't set `ArgsRequired`.
- Learned `Args` include argv[0] (e.g. `["/usr/sbin/ip", "route", "list"]`).

Nothing in storage or node-agent produces these tokens, so today they only
appear in hand-written profiles. This design adds a producer. Matching
semantics stay the same, and neither R0040 nor the CEL library changes.

## Design

### `AnalyzeExecs`

```go
func AnalyzeExecs(execs []types.ExecCalls, analyzer *ExecArgsAnalyzer) []types.ExecCalls
```

It runs per binary path:

1. **Group** entries by `Path`. Entries with `ArgsRequired=true` or empty
   `Args` pass through unchanged, since they're hand-written or already
   unconstrained.
2. **Build an argument trie** for each path. Level *i* is argv[*i*], and
   argv[0] always stays literal. The collapse rule is the same one opens use
   (`updateNodeStats` / `createDynamicNode`): when a node's child count goes
   over the threshold, the children merge into one `⋯` child and their
   subtrees are unioned. Each leaf becomes one argv pattern. Levels are
   positions, so `[grep, -E, ^-A]` and `[grep, bin/splunk$]` collapse
   independently to `[grep, ⋯, ⋯]` and `[grep, ⋯]`, and argument counts
   never merge into each other.
3. **Per-binary ceiling.** If a path still has more than `threshold`
   patterns after step 2, replace them all with `[argv0, ⋯⋯]`: known binary,
   any arguments. This is the only step that fully drops argument checking,
   and it only applies to the noisiest binaries.

A consolidation pass then removes literal entries that a pattern already
covers, using `CompareExecArgs`, the same function the runtime matcher uses.
This mirrors `consolidateOpens`, so storage and the matcher agree on what a
pattern covers. `Envs` of merged entries are unioned and deduped. They aren't
used for matching today.

Out of scope for the first version: segment-level collapsing inside
path-shaped args (e.g. `/tmp/abc123/run.sh` → `/tmp/⋯/run.sh`). The matcher
already supports it, but it needs a `PathAnalyzer` per argument position.
Step 2 handles the arguments that vary most.

### Settings and CRD

- Add `ExecArgsDynamicThreshold int` to `CollapseSettings`, with a default
  constant `ExecArgsDynamicThreshold = 50` (the same as
  `OpenDynamicThreshold`).
- Add `execArgsDynamicThreshold` to the `CollapseConfiguration` spec, using
  the same non-positive-means-default guard as the other thresholds in
  `CollapseSettingsFromCRD`. A literal 0 would otherwise reduce every binary
  to `⋯⋯`.
- Per-binary overrides use `CollapseConfig{Prefix, Threshold}` matched
  against the exec path, with longest prefix winning. Examples: a higher
  threshold for `/usr/bin/bash` or `/usr/bin/python3` to keep them precise, a
  lower one for `/usr/bin/grep`. See open question 1 on whether these share
  the opens list.

### Wiring

In `DeflateContainerProfileSpec`, replace `Execs: DeflateStringer(...)` with
`AnalyzeExecs(...)` in both places: the flat spec and
`deflateContainerProfileContainers`. Deflation runs on the merged profile on
every save, so patterns stay stable, and new literal deltas that an existing
pattern covers get absorbed by the consolidation pass. Downstream consumers
that call `DeflateContainerProfileSpec` directly get the behavior when they
bump the dependency.

### Profiles that already hit the limit

Profiles that already failed with `ObjectTooLargeError` won't recover on
their own, because node-agent stops learning for that container. Consumers
that keep the full spec can re-run `DeflateContainerProfileSpec` on it and
clear the status if the result fits. Otherwise the profile has to be reset
and learned again. The storage change doesn't need to handle this, but the
rollout should say which option each deployment uses.

## Security trade-offs

| Outcome for a binary | When it happens | Effect on R0040 / args-gated rules |
|---|---|---|
| Literal argvs kept | ≤ threshold distinct values at every position | Unchanged |
| Position-level `⋯` | One argument position varies a lot | Weaker only at that position. The other arguments must still match |
| `[argv0, ⋯⋯]` | Still more than threshold patterns after step 2 | Only the path is checked for that binary. R0001 still checks the path |

The risk sits in the third row: an attacker who knows the baseline could hide
inside a fully wildcarded interpreter. Mitigations are stricter per-binary
thresholds for interpreters (`bash`, `sh`, `python*`, `perl`) and emitting a
metric or annotation that lists binaries which fell back to `⋯⋯`, so it's
visible rather than silent.

Doing nothing is worse on every row. A profile that goes too large loses its
whole baseline.

## Expected effect

Simulated on the measured host profile with a simple positional collapse (a
flat per-argc grouping, not the final trie):

| Threshold | Exec entries | Binaries at `⋯⋯` (of 176) |
|---|---|---|
| 50 | 9,849 → 1,212 | 16 |
| 20 | 9,849 → 614 | 24 |
| 10 | 9,849 → 449 | 28 |

At the default of 50 the whole profile goes from about 10,100 entries to
about 1,600. The trie should do at least as well, because it can collapse
below the first varying position.

## Testing plan

- Unit tests for `AnalyzeExecs`: below threshold is a no-op, a
  single-position collapse, collapsing at several positions, argv[0] never
  collapsed, the per-binary `⋯⋯` fallback, `ArgsRequired` and empty-args
  pass-through, `Envs` union, deterministic ordering, and idempotence
  (`AnalyzeExecs(AnalyzeExecs(x)) == AnalyzeExecs(x)`).
- A property test: every input argv still matches some output entry under
  `CompareExecArgs`. Collapsing must never cause a false positive on data it
  has already seen.
- An R0040-shaped test: a collapsed position still fails on a mismatch at a
  literal position.
- A sanitized regression fixture built from the measured host profile, with
  a size assertion.
- A CRD zero-guard test for `execArgsDynamicThreshold`.

## Open questions

1. Should exec per-prefix overrides share `CollapseConfigs` with opens or get
   their own `ExecCollapseConfigs`? A separate list would stop an opens
   override on `/usr/bin` from quietly changing exec behavior.
2. Should interpreters get stricter defaults out of the box, or only through
   the CRD?
3. Should the `⋯⋯` fallback be visible (annotation or metric)?
4. Should the size limit count exec patterns like other entries once
   collapsing exists, or should it move to a byte budget?
5. Separately: should hitting the size limit stay terminal, or should storage
   keep the last good spec and drop new entries? That's a bigger change and
   probably needs its own design.
