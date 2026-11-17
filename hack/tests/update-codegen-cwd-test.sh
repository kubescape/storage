#!/usr/bin/env bash
# Execute the actual update script from outside a fixture repository. Stub only
# the expensive upstream generators and go run; keep path handling and schema
# fix-ups real, without modifying production files or the module cache.
set -euo pipefail
source_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
root="$tmp/repository"
mkdir -p "$root/hack" "$root/pkg/generated/openapi" "$tmp/codegen" "$tmp/bin"
cp "$source_root/hack/update-codegen.sh" "$root/hack/update-codegen.sh"
cp "$source_root/pkg/generated/openapi/zz_generated.openapi.go" "$root/pkg/generated/openapi/"
cat > "$tmp/codegen/kube_codegen.sh" <<'STUB'
kube::codegen::gen_helpers() { :; }
kube::codegen::gen_openapi() { :; }
kube::codegen::gen_client() { :; }
STUB
cat > "$tmp/bin/go" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
[[ "$PWD" == "$EXPECTED_ROOT" ]] || { echo "go run used wrong working directory" >&2; exit 1; }
[[ "$1" == run && "$2" == ./hack/sandbox-crds && "$3" == --output ]] || { echo "unexpected go command" >&2; exit 1; }
[[ "$4" == "$EXPECTED_OUTPUT" ]] || { echo "wrong manifest output" >&2; exit 1; }
mkdir -p "$4"
printf 'generated\n' > "$4/sandboxpolicy.crd.yaml"
STUB
chmod +x "$tmp/bin/go"
for mode in default redirected; do
  output=artifacts/sandboxpolicy
  if [[ "$mode" == redirected ]]; then output="$tmp/redirected"; fi
  (
    cd "$tmp"
    export CODEGEN_PKG="$tmp/codegen" EXPECTED_ROOT="$root" EXPECTED_OUTPUT="$output" PATH="$tmp/bin:$PATH"
    if [[ "$mode" == redirected ]]; then export SANDBOX_CRD_OUTPUT="$output"; else unset SANDBOX_CRD_OUTPUT; fi
    bash "$root/hack/update-codegen.sh"
  ) > "$tmp/$mode.log" 2>&1 || { cat "$tmp/$mode.log"; exit 1; }
  if [[ "$mode" == default ]]; then output="$root/$output"; fi
  [[ -f "$output/sandboxpolicy.crd.yaml" ]] || { echo "missing $mode manifest" >&2; exit 1; }
  echo "update-codegen: external cwd / $mode PASS"
done
