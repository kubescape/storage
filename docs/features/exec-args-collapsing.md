# Exec argument collapsing

> Status: **implemented**. `dynamicpathdetector.AnalyzeExecs`, wired into
> `DeflateContainerProfileSpec`; see "Implementation notes" for the details the
> code settled.

## Summary

Container profiles record every distinct argv a binary runs with as a separate
`ExecCalls` entry. `DeflateContainerProfileSpec` collapses opens, endpoints and
network neighbors by threshold, but execs only go through `DeflateStringer`,
which removes exact duplicates (path plus every arg). Nothing else bounds them.

This proposal adds `dynamicpathdetector.AnalyzeExecs`. It collapses
high-variety argument positions into the exec wildcards the matcher already
supports (`⋯` for one arg, `⋯⋯` for zero or more). A new
`ExecDynamicThreshold` controls it, set through the `CollapseConfiguration`
CRD. Per-binary overrides reuse the existing `CollapseConfigs`. The profile
size limit and what happens when a profile hits it stay the same.

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
   argv[0] stays literal (see Implementation notes for the high-variety case). The collapse rule is the same one opens use
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

- Add `ExecDynamicThreshold int` to `CollapseSettings`, with a compiled-in
  default constant `ExecDynamicThreshold = 50` (the same as
  `OpenDynamicThreshold`).
- Add `execDynamicThreshold` to the `CollapseConfiguration` spec, using the
  same non-positive-means-default guard as the other thresholds in
  `CollapseSettingsFromCRD`. A literal 0 would otherwise reduce every binary
  to `⋯⋯`.
- Per-binary overrides reuse the existing `CollapseConfigs` list: no separate
  exec list. For execs, `effectiveThreshold` matches the exec path against
  `CollapseConfig.Prefix` (longest prefix at a path boundary wins), and
  `ExecDynamicThreshold` is the fallback when no prefix matches. That makes
  one entry apply to both opens and execs under that prefix. For example,
  `{Prefix: "/usr/bin", Threshold: 200}` loosens opens under `/usr/bin` and
  keeps more argv variants for binaries in `/usr/bin`. A single binary can be
  targeted with its full path, e.g. `{Prefix: "/usr/bin/bash", Threshold: 200}`.
  The current built-in configs (`/etc` 100, `/etc/apache2` 50, `/opt` 50,
  `/var/run` 50, `/app` 50) would now also apply to binaries under those
  paths. All but `/etc` equal the exec default, and binaries rarely run from
  `/etc`.
- Tuning is **only through the CRD**. No built-in per-binary defaults are
  added, including for interpreters. Operators who want stricter or looser
  handling for `bash`, `python3` and so on set a `CollapseConfigs` entry.

### Wiring

In `DeflateContainerProfileSpec`, replace `Execs: DeflateStringer(...)` with
`AnalyzeExecs(...)` in both places: the flat spec and
`deflateContainerProfileContainers`. Deflation runs on the merged profile on
every save, so patterns stay stable, and new literal deltas that an existing
pattern covers get absorbed by the consolidation pass. Downstream consumers
that call `DeflateContainerProfileSpec` directly get the behavior when they
bump the dependency.

### Size limit and limit behavior: unchanged

The profile size limit stays a **count of entries**. It doesn't become a byte
budget, and a collapsed exec pattern counts as one entry, like any other.
What happens when a profile hits the limit also stays the same: learning ends
with `ObjectTooLargeError` as it does today. The change only makes the limit
much harder to reach, by keeping execs from growing without bound.

Profiles that already hit the limit won't recover from this change alone.
Consumers that keep the full spec can re-run `DeflateContainerProfileSpec` on
it. Otherwise the profile has to be reset and learned again. That's a rollout
concern for each deployment and outside this change.

## Implementation notes

- **One trie per (argv[0], argc).** A single trie per argv[0] would let a
  high-variety level merge argvs of different lengths (`[grep, -E, x]` would
  become `[grep, ⋯, x]` next to many `[grep, <pattern>]`). Rooting a separate
  trie at each argument count is what keeps lengths apart.
- **An existing `⋯` child absorbs its siblings**, the same way
  `PathAnalyzer.processSegment` handles `IsNextDynamic`. That's what keeps
  a stored pattern absorbing new literal deltas and makes the pass idempotent.
- **argv[0] stays literal up to the threshold.** For interpreters,
  argv[0] is often the script (`/bin/dracut`, `/usr/bin/dnf` run through
  `bash` / `python3`), which is worth keeping. On the measured host profile,
  `bash` alone had 47 argv[0] values. A binary with more than threshold
  distinct argv[0] values, like generated scripts under `/tmp/tmp.<N>/`,
  would otherwise grow one entry per script, even through the fallback. So
  argv[0] then collapses by path shape (`/tmp/tmp.1/run.sh` →
  `/tmp/⋯/run.sh`), and to a bare `⋯` only if that still leaves more than
  threshold. This runs *after* covered entries are absorbed, so deltas a
  stored argv[0] pattern already covers don't count as variety, and argv[0]
  values that are already patterns are never re-analyzed. Shapes come from
  the same argument trie used for argv positions, applied to argv[0]'s
  `/`-separated segments (one trie per segment count). It only produces `⋯`,
  exactly the one-segment semantics `CompareExecArgs` gives `⋯` inside an
  argument, so a `*` in argv[0], whether embedded (`star*dir`) or a whole
  segment, stays literal. `PathAnalyzer` isn't used here: its `*` is opens
  glob syntax. Every shape is verified with `CompareExecArgs` against its
  original.
- **The fallback is one `[argv0, ⋯⋯]` per remaining argv[0]**, so it's
  bounded by the argv[0] step above. The ceiling counts distinct entries
  *after* existing `⋯⋯` patterns are deduped (by argv, envs unioned) and
  covered literals are absorbed. A delta the stored profile already allows
  (e.g. `bash -c a` under `[bash, -c, ⋯⋯]`) never broadens the binary to
  `[bash, ⋯⋯]` on a later save.
- **Absorb before generalizing, and absorb patterns soundly.** Entries are
  deduped by argv (envs unioned), and anything an existing pattern already
  covers is absorbed *before* the trie runs. A stored `[bash, -c, ⋯⋯]` plus
  many covered deltas therefore can't turn those deltas into new
  `[bash, -c, ⋯]` patterns that push the binary over the ceiling. After the
  trie, patterns are consolidated again, and a pattern can absorb another
  pattern. Coverage is `CompareExecArgs(coverer, covered)` with the covered
  entry's tokens read as literals. That's sound as long as the covered entry
  has no `⋯⋯`: a single `⋯` is never taken to cover zero-or-more args, so
  `⋯⋯` entries are only ever merged with exact duplicates.
- **Keys are injective.** Dedupe keys are length-prefixed token lists, not
  `strings.Join`: any separator glyph (including `␟`) is valid argv data.
- **Output is totally ordered and never nil.** It's sorted by path, then
  argv (element-wise), then `ArgsRequired` (false first), then envs, so the
  stored bytes don't depend on input order. Empty
  input returns `[]`, so stored profiles keep encoding empty execs as `[]`,
  as they did with `DeflateStringer`. Exact dedupe now ignores `Envs`, and
  envs of merged entries are unioned.
- **Thresholds.** `NewExecAnalyzer` treats a non-positive default as
  `ExecDynamicThreshold`. Per-prefix entries with `Threshold < 1` are ignored
  for execs (admission already rejects them).
- **Measured on the real host profile** (not the flat simulation below):

  | Threshold | Exec entries | `[argv0, ⋯⋯]` entries | Inputs not covered |
  |---|---|---|---|
  | 50 | 9,849 → 1,292 | 88 | 0 |
  | 20 | 9,849 → 657 | 57 | 0 |
  | 10 | 9,849 → 467 | 57 | 0 |

  A second pass over each output is a no-op, including with a covered delta
  added. The first deflation of the whole profile takes about 33 ms at the
  default threshold, and about 0.6 s at thresholds 10–20.

## Security trade-offs

| Outcome for a binary | When it happens | Effect on R0040 / args-gated rules |
|---|---|---|
| Literal argvs kept | ≤ threshold distinct values at every position | Unchanged |
| Position-level `⋯` | One argument position varies a lot | Weaker only at that position. The other arguments must still match |
| `[argv0, ⋯⋯]` | Still more than threshold patterns after step 2 | Only the path is checked for that binary. R0001 still checks the path |

The risk sits in the third row: an attacker who knows the baseline could hide
inside a fully wildcarded interpreter. Mitigations are `CollapseConfigs`
entries with stricter thresholds for interpreters (`bash`, `sh`, `python*`,
`perl`), set through the CRD where the deployment wants them, and emitting a
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
- A CRD zero-guard test for `execDynamicThreshold`.
- A `CollapseConfigs` test: one prefix entry changes the threshold for both
  opens and execs under it, and a full binary path targets one binary.

## Decisions

Resolved in review:

1. **Per-prefix overrides:** reuse `CollapseConfigs` for execs. The only new
   threshold is `ExecDynamicThreshold`.
2. **Interpreter handling:** only through the CRD. No built-in per-binary
   defaults.
3. **Size limit:** stays an entry count. No byte budget.
4. **Behavior at the limit:** unchanged.

## Open questions

1. Should the `⋯⋯` fallback be visible (annotation or metric)?
