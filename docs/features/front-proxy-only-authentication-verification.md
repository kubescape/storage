# Front-proxy-only authentication: verification

Verification of [front-proxy-only-authentication.md](front-proxy-only-authentication.md)
(GHSA-5jm5-fm7v-vq2w, moved from kubescape/kubescape GHSA-p9x2-r9x5-2wxf) on four
Kubernetes distributions, 2026-10-28.

## Setup

On every cluster:

1. Install the operator with the command from kubescape.io
   (`docs/install-operator.md`), chart `kubescape-operator` 1.40.5, which ships
   storage `v0.0.348`:

   ```shell
   helm upgrade --install kubescape kubescape/kubescape-operator -n kubescape --create-namespace \
     --set clusterName=<cluster> --set capabilities.continuousScan=enable
   ```

2. Start an attacker pod in its own namespace, with the namespace's default
   ServiceAccount and no RBAC: `kubectl -n attacker run probe --image=curlimages/curl:8.11.1`.
3. Run the probe below against the stock image (control).
4. Switch the storage Deployment to the fix image, wait for the Service
   endpoint to point at the new pod, and run the probe again.
5. Deploy a workload (`nginx:1.27.3` in namespace `victim`) and check that
   kubevuln and node-agent still store data through kube-apiserver.

The fix image is storage `main` at `4f89f04` plus this change:
`quay.io/kubescape/storage:test-4f89f04`
(`sha256:7f73a756adc53ab94434c90dfd33755cfb62ead7f45fa45fe0a546b7437b641c`); on kind
the same build was loaded locally with `kind load docker-image`.

Probe, run inside the attacker pod:

```shell
B=https://storage.kubescape.svc:443
R=/apis/spdx.softwarecomposition.kubescape.io/v1beta1
T=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
c() { curl -sk -o /dev/null -w '%{http_code}' "$@"; }
c $B$R/vulnerabilitymanifests                                   # no credential, read
c -X POST -H 'Content-Type: application/json' \
  -d '{"apiVersion":"spdx.softwarecomposition.kubescape.io/v1beta1","kind":"KnownServer","metadata":{"name":"probe"}}' \
  $B$R/knownservers                                             # no credential, write
c -H "Authorization: Bearer $T" $B$R/vulnerabilitymanifests     # SA token, direct
c -H "Authorization: Bearer $T" https://kubernetes.default.svc$R/vulnerabilitymanifests  # SA token, via kube-apiserver
c $B/readyz; c $B/livez                                         # probes, no credential
```

## Clusters

| Distribution | Kubernetes | Front-proxy allowed names |
|---|---|---|
| DigitalOcean DOKS (fra1) | 1.37.0 | not recorded |
| GKE (europe-west1-b) | 1.36.4 | `aggregator` |
| EKS (eu-central-1) | 1.34.12 | `front-proxy-client` |
| kind v0.29.0 | 1.33 | `front-proxy-client` |

Every distribution publishes the request-header CA in
`kube-system/extension-apiserver-authentication`; on GKE it is a different CA
from `client-ca-file`. Storage started on all four without
`insecureSkipFrontProxyVerification`.

## Results

Identical on all four clusters:

| Request from the attacker pod | stock `v0.0.348` | fix |
|---|---|---|
| No credential, GET vulnerabilitymanifests | 200 | **401** |
| No credential, POST knownservers | 201 (object created) | **401** |
| Own SA token, sent straight to storage | 200 | **401** |
| Own SA token, through kube-apiserver | 403 | 403 |
| `/readyz`, `/livez`, no credential | 200 | 200 |

With the fix, on every cluster:

- The storage pod is Ready with no restarts and the APIService is
  `Available=True`. Right after the rollout it reports `FailedDiscoveryCheck`
  for a few seconds while the old pod is replaced (seen on kind: new pod at
  09:49:21Z, `Available` at 09:49:28Z).
- Data written after the new storage pod started: SBOMs and vulnerability
  manifests by kubevuln, container profiles by node-agent (including the
  `victim` workload's). No component logged a 401.
- Cluster-admin reads through kube-apiserver work.

## Not covered

- Configuration scans: with no backend configured, the default install does
  not run one by itself, so `workloadconfigurationscans` were not exercised.
- The fix image is built from storage `main`, newer than the `v0.0.348` the
  chart expects; storage logs `nodes is forbidden` warnings from that version
  skew, unrelated to this change.
- `insecureSkipFrontProxyVerification: true` was covered by unit tests only.
