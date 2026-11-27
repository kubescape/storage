#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
source vendor/k8s.io/code-generator/kube_codegen.sh
kube::codegen::gen_helpers --boilerplate hack/boilerplate.go.txt pkg/apis/sandbox
go run ./hack/sandbox-crds "$@"
