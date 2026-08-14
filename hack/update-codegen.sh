#!/usr/bin/env bash

# Copyright 2017 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..
CODEGEN_PKG=${CODEGEN_PKG:-$(cd "${SCRIPT_ROOT}"; ls -d -1 ./vendor/k8s.io/code-generator 2>/dev/null || echo ../code-generator)}
API_KNOWN_VIOLATIONS_DIR="${API_KNOWN_VIOLATIONS_DIR:-"${SCRIPT_ROOT}/api-rules"}"

source "${CODEGEN_PKG}/kube_codegen.sh"

THIS_PKG="github.com/kubescape/storage"

kube::codegen::gen_helpers \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/pkg/apis"

if [[ -n "${API_KNOWN_VIOLATIONS_DIR:-}" ]]; then
    report_filename="${API_KNOWN_VIOLATIONS_DIR}/sample_apiserver_violation_exceptions.list"
    if [[ "${UPDATE_API_KNOWN_VIOLATIONS:-}" == "true" ]]; then
        update_report="--update-report"
    fi
fi

kube::codegen::gen_openapi \
    --output-dir "${SCRIPT_ROOT}/pkg/generated/openapi" \
    --output-pkg "${THIS_PKG}/pkg/generated/openapi" \
    --report-filename "${report_filename:-"/dev/null"}" \
    ${update_report:+"${update_report}"} \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    --output-model-name-file zz_generated.model_name.go \
    "${SCRIPT_ROOT}/pkg/apis"

kube::codegen::gen_client \
    --with-watch \
    --with-applyconfig \
    --output-dir "${SCRIPT_ROOT}/pkg/generated" \
    --output-pkg "${THIS_PKG}/pkg/generated" \
    --boilerplate "${SCRIPT_ROOT}/hack/boilerplate.go.txt" \
    "${SCRIPT_ROOT}/pkg/apis"

# Post-generation fix-up: HTTPEndpoint.Headers is a json.RawMessage carrying a
# JSON OBJECT, but openapi-gen types json.RawMessage as {type: string, format:
# byte}. The apiserver's server-side-apply TypeConverter is built from this
# OpenAPI, and converting a profile whose endpoints carry headers then fails
# ("expected string, got <object>"), which keeps profiles from completing.
# Re-type the published schema as a preserve-unknown-fields object.
# TestHTTPEndpointHeaders_SMDConversion (pkg/registry/file) pins this and goes
# red if a regeneration loses the fix-up.
python3 - "${SCRIPT_ROOT}/pkg/generated/openapi/zz_generated.openapi.go" <<'PYEOF'
import re, sys
path = sys.argv[1]
src = open(path).read()
old = '''					"headers": {
						SchemaProps: spec.SchemaProps{
							Type:   []string{"string"},
							Format: "byte",
						},
					},'''
new = '''					"headers": {
						VendorExtensible: spec.VendorExtensible{
							Extensions: spec.Extensions{
								"x-kubernetes-preserve-unknown-fields": true,
							},
						},
						SchemaProps: spec.SchemaProps{
							Type: []string{"object"},
						},
					},'''
if old in src:
    open(path, "w").write(src.replace(old, new))
    print("headers schema fix-up applied")
elif new in src:
    print("headers schema fix-up already present")
else:
    sys.exit("headers schema fix-up anchor not found - update hack/update-codegen.sh")
PYEOF
