#!/usr/bin/env bash
# Run real Kubernetes validators without changing go.mod/go.sum or the vendor
# tree. Validation packages belong to an existing dependency but aren't used
# by production storage, so the default vendor tree doesn't contain them.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
cp go.mod "$tmp/validation.mod"
cp go.sum "$tmp/validation.sum"
go test -mod=mod -modfile="$tmp/validation.mod" -tags=crdvalidation ./pkg/apis/sandbox/... "$@"
