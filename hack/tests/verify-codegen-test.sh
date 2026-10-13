#!/usr/bin/env bash
# Test the real verifier with an isolated codegen stub: no production files or
# expensive general generators are touched by these regression cases.
set -euo pipefail
source_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
for scenario in clean stale missing obsolete generation-failure; do
  root="$tmp/$scenario"
  mkdir -p "$root/hack" "$root/pkg" "$root/artifacts/sandboxpolicy"
  cp "$source_root/hack/verify-codegen.sh" "$root/hack/verify-codegen.sh"
  printf 'original source\n' > "$root/pkg/source.go"
  printf 'expected manifest\n' > "$root/artifacts/sandboxpolicy/sandboxpolicy.crd.yaml"
  cat > "$root/hack/update-codegen.sh" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
: "${SANDBOX_CRD_OUTPUT:?verifier must redirect artifact output}"
printf 'expected manifest\n' > "$SANDBOX_CRD_OUTPUT/sandboxpolicy.crd.yaml"
if [[ "${SIMULATE_GENERATOR_FAILURE:-false}" == true ]]; then exit 1; fi
STUB
  chmod +x "$root/hack/update-codegen.sh"
  expected_status=1
  case "$scenario" in
    clean) expected_status=0 ;;
    stale) printf 'stale manifest\n' > "$root/artifacts/sandboxpolicy/sandboxpolicy.crd.yaml" ;;
    missing) rm "$root/artifacts/sandboxpolicy/sandboxpolicy.crd.yaml" ;;
    obsolete) printf 'obsolete manifest\n' > "$root/artifacts/sandboxpolicy/obsolete.crd.yaml" ;;
    generation-failure) ;;
  esac
  cp -a "$root/artifacts" "$root/original-artifacts"
  actual_status=0
  (
    cd "$root"
    if [[ "$scenario" == generation-failure ]]; then export SIMULATE_GENERATOR_FAILURE=true; fi
    bash hack/verify-codegen.sh
  ) > "$root/output.log" 2>&1 || actual_status=$?
  if [[ "$actual_status" != "$expected_status" ]]; then
    cat "$root/output.log"
    echo "$scenario: expected exit $expected_status, got $actual_status" >&2
    exit 1
  fi
  diff -ru "$root/original-artifacts" "$root/artifacts"
  if [[ -e "$root/_tmp" ]]; then echo "$scenario: verifier temporary output leaked" >&2; exit 1; fi
  echo "verify-codegen: $scenario PASS"
done
