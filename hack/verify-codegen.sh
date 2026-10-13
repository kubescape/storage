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

SCRIPT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

DIFFROOT="${SCRIPT_ROOT}/pkg"
TMP_DIFFROOT="${SCRIPT_ROOT}/_tmp/pkg"
_tmp="${SCRIPT_ROOT}/_tmp"
ARTIFACTS_ROOT="${SCRIPT_ROOT}/artifacts/sandboxpolicy"
TMP_CHECKED_ARTIFACTS="${_tmp}/checked-sandboxpolicy"
TMP_GENERATED_ARTIFACTS="${_tmp}/generated-sandboxpolicy"

cleanup() {
  rm -rf "${_tmp}"
}
trap "cleanup" EXIT SIGINT

cleanup

mkdir -p "${TMP_DIFFROOT}"
cp -a "${DIFFROOT}"/* "${TMP_DIFFROOT}"

# Snapshot only generated manifests, not the hand-written examples. Generate
# into verifier-owned output so stale, missing or obsolete checked-in manifests
# remain untouched and cannot be silently repaired before later tests run.
mkdir -p "${TMP_CHECKED_ARTIFACTS}" "${TMP_GENERATED_ARTIFACTS}"
for manifest in "${ARTIFACTS_ROOT}"/*.crd.yaml; do
  if [[ -f "${manifest}" ]]; then
    cp -a "${manifest}" "${TMP_CHECKED_ARTIFACTS}/"
  fi
done
SANDBOX_CRD_OUTPUT="${TMP_GENERATED_ARTIFACTS}" "${SCRIPT_ROOT}/hack/update-codegen.sh"
echo "diffing ${DIFFROOT} against freshly generated codegen"
ret=0
diff -Naupr "${DIFFROOT}" "${TMP_DIFFROOT}" || ret=$?
cp -a "${TMP_DIFFROOT}"/* "${DIFFROOT}"
echo "diffing ${ARTIFACTS_ROOT} against freshly generated manifests"
diff -Naupr "${TMP_CHECKED_ARTIFACTS}" "${TMP_GENERATED_ARTIFACTS}" || ret=$?
if [[ $ret -eq 0 ]]
then
  echo "${DIFFROOT} and ${ARTIFACTS_ROOT} up to date."
else
  echo "Generated code or manifests are out of date. Please run hack/update-codegen.sh"
  exit 1
fi
