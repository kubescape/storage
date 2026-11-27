#!/usr/bin/env bash
# Tier B of the storage measurement harness: paired A/B of HEAD against its
# merge-base, rebuilt in the same run window on the same machine.
# Design: .omc/plans/raw-write-bypass-elimination.md, A.13.4.
#
#   make perf-ab                      # BASE = git merge-base origin/main HEAD
#   BASE=<sha> PAIRS=10 hack/perf-ab.sh
#
# Inputs (env):
#   BASE                 commit to compare against (default: merge-base with origin/main)
#   PAIRS                interleaved (base, head) rounds (default 10)
#   PROBE_ROUNDS         base-only early-abort rounds (default 3)
#   PERF_AB_OUT_DIR      where rounds, logs, schedule.txt and verdict.txt go
#   PERF_AB_ALLOW_NOISY  1 to run despite a high 1-minute load average (verdict stamped "(noisy)")
#   PERF_AB_KEEP         1 to keep the base worktree and build artifacts
#   PERF_AB_BASE_ENV     extra KEY=VALUE pairs (space-separated) for the base arm's rounds
#   PERF_AB_HEAD_ENV     ... for the head arm's rounds. With BASE=HEAD this turns the A/B
#                        into a same-commit comparison of two configurations, e.g.
#                        PERF_AB_BASE_ENV="PERF_AB_BACKEND=legacy"
#                        PERF_AB_HEAD_ENV="PERF_AB_BACKEND=objectstore"
#                        LOAD_HOT_KEYS=1 in BOTH arms selects the same-key
#                        contention shape (every updater on base key 0); it is
#                        part of the effective config, so one arm alone is a
#                        CONFIG MISMATCH.
#
# Exit codes: 0 PASS, 1 REGRESSION, 2 CONFIG MISMATCH (or base cannot host
# HEAD's harness), 3 INCONCLUSIVE, 4 UNDERPOWERED.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PKG=pkg/registry/file
PAIRS=${PAIRS:-10}
PROBE_ROUNDS=${PROBE_ROUNDS:-3}
GOMAXPROCS_PIN=${GOMAXPROCS_PIN:-8}
THRESHOLDS=$PKG/testdata/perfab.thresholds.json
# Files overlaid onto the base worktree so both arms run HEAD's instrument.
HARNESS_FILES=("$PKG/containerprofile_load_test.go" "$THRESHOLDS")

OUT=${PERF_AB_OUT_DIR:-$ROOT/.omc/artifacts/perf-ab/$(date +%Y%m%d-%H%M%S)}
mkdir -p "$OUT"
BASE_WT=$OUT/base-worktree

cd "$ROOT"
HEAD_SHA=$(git rev-parse HEAD)
if [ -z "${BASE:-}" ]; then
  BASE=$(git merge-base origin/main HEAD)
fi
BASE_SHA=$(git rev-parse --verify "$BASE^{commit}")

log() { printf '%s %s\n' "$(date +%H:%M:%S)" "$*"; }
die() { echo "perf-ab: $*" >&2; exit "${2:-2}"; }

cleanup() {
  if [ "${PERF_AB_KEEP:-0}" != "1" ] && [ -d "$BASE_WT" ]; then
    git -C "$ROOT" worktree remove --force "$BASE_WT" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# 1. Start-of-run load check: an early abort, not the control (see step 5).
NPROC=$(nproc)
LOAD_BOUND=$(( NPROC / 2 ))
load1() { cut -d' ' -f1 /proc/loadavg; }
NOISY_FLAG=""
L1=$(load1)
if awk -v l="$L1" -v b="$LOAD_BOUND" 'BEGIN{exit !(l > b)}'; then
  if [ "${PERF_AB_ALLOW_NOISY:-0}" = "1" ]; then
    log "WARNING: 1-minute load $L1 exceeds nproc/2=$LOAD_BOUND; continuing (PERF_AB_ALLOW_NOISY=1), verdict will be stamped (noisy)"
    NOISY_FLAG="-noisy"
  else
    echo "INCONCLUSIVE load=$L1 exceeds nproc/2=$LOAD_BOUND at start; refusing to run (PERF_AB_ALLOW_NOISY=1 overrides)" | tee "$OUT/verdict.txt"
    exit 3
  fi
fi
log "load check: 1-minute load $L1, bound nproc/2=$LOAD_BOUND"

# 2. CPU set from nproc: the first floor(nproc/2) distinct physical cores
#    when lscpu can tell them apart, else the first floor(nproc/2) CPUs.
NCPU=$(( NPROC / 2 ))
[ "$NCPU" -ge 1 ] || NCPU=1
if command -v lscpu >/dev/null 2>&1 && lscpu -p=CPU,CORE >/dev/null 2>&1; then
  CPUSET=$(lscpu -p=CPU,CORE | grep -v '^#' | awk -F, '!seen[$2]++ {print $1}' | head -n "$NCPU" | paste -sd, -)
else
  CPUSET="0-$(( NCPU - 1 ))"
fi
PIN=(env "GOMAXPROCS=$GOMAXPROCS_PIN")
if command -v taskset >/dev/null 2>&1; then
  PIN=(taskset -c "$CPUSET" env "GOMAXPROCS=$GOMAXPROCS_PIN")
fi
BASE_ENV=${PERF_AB_BASE_ENV:-}
HEAD_ENV=${PERF_AB_HEAD_ENV:-}
log "head=$HEAD_SHA base=$BASE_SHA pairs=$PAIRS probe=$PROBE_ROUNDS cpuset=$CPUSET GOMAXPROCS=$GOMAXPROCS_PIN out=$OUT base_env='$BASE_ENV' head_env='$HEAD_ENV'"

# 3. Build both binaries with the same harness: HEAD's harness files are
#    overlaid onto the base worktree before `go test -c`.
git worktree add --detach "$BASE_WT" "$BASE_SHA" >/dev/null 2>&1 || die "git worktree add $BASE_SHA failed"
for f in "${HARNESS_FILES[@]}"; do
  mkdir -p "$BASE_WT/$(dirname "$f")"
  cp "$ROOT/$f" "$BASE_WT/$f"
done
log "building base.test (base $BASE_SHA + HEAD harness overlay)"
if ! (cd "$BASE_WT" && go test -c -o "$OUT/base.test" "./$PKG" >"$OUT/build-base.log" 2>&1); then
  {
    echo "CONFIG MISMATCH base $BASE_SHA cannot host HEAD's harness (${HARNESS_FILES[*]}):"
    grep -v '^#' "$OUT/build-base.log" | head -n 5
  } | tee "$OUT/verdict.txt"
  exit 2
fi
log "building head.test"
(cd "$ROOT" && go test -c -o "$OUT/head.test" "./$PKG" >"$OUT/build-head.log" 2>&1) || { cat "$OUT/build-head.log"; die "head build failed"; }
(cd "$ROOT" && go build -o "$OUT/perfab" ./hack/perfab) || die "perfab tool build failed"
PERFAB=$OUT/perfab

SCHEDULE=$OUT/schedule.txt
: >"$SCHEDULE"
: >"$OUT/base.txt"
: >"$OUT/head.txt"

# run_round <arm> <label> <pair>: one round of the arm's binary, pinned, on a
# fresh temp DB (t.TempDir). Appends the bench lines and a schedule record.
run_round() {
  local arm=$1 label=$2 pair=$3
  local bin="$OUT/$arm.test" dir json l marked armenv
  case $arm in
    base) dir="$BASE_WT/$PKG"; armenv=$BASE_ENV ;;
    head) dir="$ROOT/$PKG"; armenv=$HEAD_ENV ;;
  esac
  json="$OUT/$label.json"
  # Settle before sampling load1: rounds run back-to-back with no idle gap,
  # so the 1-minute average never gets a chance to decay between them and
  # climbs monotonically over a long run regardless of which arm is running
  # -- not evidence of external contamination, just the harness's own
  # workload never idling. A short settle restores load1 to something that
  # actually reflects ambient load rather than a perpetually rising floor.
  sleep "${PERF_AB_SETTLE_SECONDS:-3}"
  l=$(load1)
  marked=0
  if awk -v l="$l" -v b="$LOAD_BOUND" 'BEGIN{exit !(l > b)}'; then marked=1; fi
  log "round $label (arm=$arm pair=$pair load1=$l marked=$marked)"
  # shellcheck disable=SC2086  # armenv is a deliberate word-split list of KEY=VALUE
  (cd "$dir" && PERF_AB_OUT="$json" "${PIN[@]}" $armenv "$bin" -test.run '^TestPerfABRound$' -test.v -test.timeout 30m >"$OUT/$label.log" 2>&1) \
    || { tail -n 30 "$OUT/$label.log"; die "round $label failed; see $OUT/$label.log" 2; }
  grep '^BenchmarkPerfAB/' "$OUT/$label.log" >>"$OUT/$arm.txt" || true
  printf 'pair=%s arm=%s label=%s start=%s load1=%s marked=%s env=%s\n' "$pair" "$arm" "$label" "$(date +%FT%T)" "$l" "$marked" "${armenv:-}" >>"$SCHEDULE"
}

# 4. Early-abort noise probe: A A A on base. Decides nothing else.
PROBE_FILES=()
for i in $(seq 1 "$PROBE_ROUNDS"); do
  run_round base "probe-$i" 0
  PROBE_FILES+=("$OUT/probe-$i.json")
done
PROBE_LIST=$(IFS=,; echo "${PROBE_FILES[*]}")
if ! "$PERFAB" probe -thresholds "$ROOT/$THRESHOLDS" -rounds "$PROBE_LIST" | tee "$OUT/probe.txt"; then
  grep -h 'PROBE ABORT' "$OUT/probe.txt" | sed 's/^PROBE ABORT/INCONCLUSIVE probe/' | tee "$OUT/verdict.txt"
  exit 3
fi

# 5. Interleave PAIRS rounds of A B, same pinning, fresh DB each; the
#    per-round load sample marks the pair (verdict: > ceil(PAIRS/3) marked
#    pairs is INCONCLUSIVE regardless of the statistics).
BASE_FILES=()
HEAD_FILES=()
for i in $(seq 1 "$PAIRS"); do
  run_round base "base-$i" "$i"
  run_round head "head-$i" "$i"
  BASE_FILES+=("$OUT/base-$i.json")
  HEAD_FILES+=("$OUT/head-$i.json")
done
BASE_LIST=$(IFS=,; echo "${BASE_FILES[*]}")
HEAD_LIST=$(IFS=,; echo "${HEAD_FILES[*]}")

# 6. Verdict: paired t on per-round log-ratios, Mann-Whitney as the second
#    opinion, post-hoc paired CV and MDE; benchstat if it is on PATH.
if command -v benchstat >/dev/null 2>&1; then
  benchstat -alpha 0.05 "$OUT/base.txt" "$OUT/head.txt" >"$OUT/benchstat.txt" 2>&1 || true
  log "benchstat table in $OUT/benchstat.txt"
fi
set +e
"$PERFAB" verdict -thresholds "$ROOT/$THRESHOLDS" -pairs "$PAIRS" -schedule "$SCHEDULE" \
  -base "$BASE_LIST" -head "$HEAD_LIST" $NOISY_FLAG | tee "$OUT/verdict-table.txt"
rc=${PIPESTATUS[0]}
set -e
tail -n 1 "$OUT/verdict-table.txt" >"$OUT/verdict.txt"
log "verdict: $(cat "$OUT/verdict.txt") (exit $rc; artifacts in $OUT)"
exit "$rc"
