# Kubescape Storage

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fkubescape%2Fstorage.svg?type=shield&issueType=license)](https://app.fossa.com/projects/git%2Bgithub.com%2Fkubescape%2Fstorage?ref=badge_shield&issueType=license)


An Aggregated APIServer for the Kubescape internal storage services.

**Note:** go-get or vendor this package as `k8s.io/sample-apiserver`.

## Purpose

The Kubescape Storage APIServer serves custom resources that Kubescape defines
for its operation. These custom reources might store internal Kubescape
configuration, scan artifacts, computed snapshots etc that help the entire
Kubescape in-cluster solution operate.

## Fetch sample-apiserver and its dependencies

Like the rest of Kubernetes, sample-apiserver has used
[godep](https://github.com/tools/godep) and `$GOPATH` for years and is now
adopting go 1.11 modules. While upstream mentions two alternative ways to go
about fetching the sample repository and its dependencies, we recommend and
primarily use only one: using native Go 1.11 modules and vendoring.

### When using native Go 1.11 modules

When using go 1.11 modules (`GO111MODULE=on`), you first need to create the appropriate working directory:

```
mkdir ~/github.com/kubescape
cd ~/github.com/kubescape
```

> [!WARNING]
> Due to the specifics of the code generation script `hack/update-codegen.sh`,
> your working directory should always match your module path. That is,
> `~/github.com/kubescape/storage` for this specific repo. If the directories
> don’t match and you store code in some other directory, you will write code
> in whatever directory you chose, but once you run codegen, it will generate
> the code only in `~/github.com/kubescape/storage`.

Once you have a working directory set up, issue the following
commands in your working directory.

```sh
git clone https://github.com/kubescape/storage.git
cd storage
```

Note that when you need to [generate code](#changes-to-the-types) then you will
also need the code-generator repo to exist in an old-style location. One easy
way to do this is to use the command `go mod vendor` to create and populate the
`vendor` directory.

### A Note on kubernetes/kubernetes

If you are developing Kubernetes according to
https://github.com/kubernetes/community/blob/master/contributors/guide/github-workflow.md
then you already have a copy of this demo in
`kubernetes/staging/src/k8s.io/sample-apiserver` and its dependencies
--- including the code generator --- are in usable locations.


## Normal Build and Deploy

### Changes to the Types

If you change the API object type definitions in any of the
`pkg/apis/.../types.go` files then you will need to update the files generated
from the type definitions. To do this, first [pull the dependencies and create
the vendor directory](#when-using-native-go-111-modules). Once you vendored the
dependencies, you will have the code generation scripts in your `vendor`
directory. To make code generation, you need to make them executable: 

```
chmod +x vendor/k8s.io/code-generator/*.sh
```

Now you’re all set to generate the code for changed types. Do this with:

```
hack/update-codegen.sh
```

If you see any errors regarding `GOPATH`, just provide it manually:
```
GOPATH=$(go env GOPATH) hack/update-codegen.sh
```

The code generation script will give you warnings about API rule violations.
Don’t mind them. To address these warnings, add them to the exclusion list as
show in the updated upstream repo.

You will also see a warning about `generate-internal-groups.sh` being deprecated:
```
WARNING: generate-internal-groups.sh is deprecated.
WARNING: Please use k8s.io/code-generator/kube_codegen.sh instead.
```

This is valid, and upstream has also been updated to use the latest code
generation script — `kube_codegen.sh`. However, as of now it breaks code
generation for us, and we had no opportunity to reconcile the changes.

Once the code generation finishes successfully, you should be able to run tests and build the binary with no errors:
```
go build -v ./...
go test -v -failfast -count=1 ./...
```

### Authentication plugins

The normal build supports only a very spare selection of
authentication methods.  There is a much larger set available in
https://github.com/kubernetes/client-go/tree/master/plugin/pkg/client/auth
.  If you want your server to support one of those, such as `oidc`,
then add an import of the appropriate package to
`sample-apiserver/main.go`.  Here is an example:

``` go
import _ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
```

Alternatively you could add support for all of them, with an import
like this:

``` go
import _ "k8s.io/client-go/plugin/pkg/client/auth"
```

### Build the Binary

With `storage` as your current working directory, issue the
following command:

```
make build
```

### Build the Container Image

With `storage` as your current working directory, issue the
following commands:

```
TAG=v1.2.3 make docker-build && TAG=v1.2.3 make docker-push
```

Take note that the Makefile targets use default values for the image tag and the Dockerfile path, so feel free to adjust them as environment variables as needed:
```
TAG=v1.2.3 IMAGE=quay.io/kubescape/storage make docker-build
TAG=v1.2.3 IMAGE=quay.io/kubescape/storage make docker-push
```

### Deploy into a Kubernetes Cluster

Edit `artifacts/example/deployment.yaml`, updating the pod template's image
reference to match what you pushed and setting the `imagePullPolicy`
to something suitable.

If you’re running a Minikube cluster locally, build and tag an container image, use it in the `artifacts/example/deployment.yaml`, set `imagePullPolicy: Never` and this will let you use a local container image without having to push it to a container registry.

Then, make sure the appropriate namespace for the APIServer components exists:

```
kubectl apply -f artifacts/example/ns.yaml
```

Finally, create all the other Aggregated APIServer components:

```
kubectl apply -f artifacts/example
```

## Running it stand-alone

During development it is helpful to run the Storage APIServer stand-alone, i.e. without
a Kubernetes API server for authn/authz and without aggregation. This is possible, but needs
a couple of flags, keys and certs as described below. You will still need some kubeconfig,
e.g. `~/.kube/config`, but the Kubernetes cluster is not used for authn/z. A minikube or
hack/local-up-cluster.sh cluster will work.

Instead of trusting the aggregator inside kube-apiserver, the described setup uses local
client certificate based X.509 authentication and authorization. This means that the client
certificate is trusted by a CA and the passed certificate contains the group membership
to the `system:masters` group. As we disable delegated authorization with `--authorization-skip-lookup`,
only this superuser group is authorized.

1. First we need a CA to later sign the client certificate:

   ``` shell
   openssl req -nodes -new -x509 -keyout ca.key -out ca.crt
   ```

2. Then we create a client cert signed by this CA for the user `development` in the superuser group
   `system:masters`:

   ``` shell
   openssl req -out client.csr -new -newkey rsa:4096 -nodes -keyout client.key -subj "/CN=development/O=system:masters"
   openssl x509 -req -days 365 -in client.csr -CA ca.crt -CAkey ca.key -set_serial 01 -out client.crt
   ```

3. As curl requires client certificates in p12 format with password, do the conversion:

   ``` shell
   openssl pkcs12 -export -in ./client.crt -inkey ./client.key -out client.p12 -passout pass:password
   ```

4. With these keys and certs in-place, we start the server:

   ``` shell
   etcd &
   sample-apiserver --secure-port 8443 --etcd-servers http://127.0.0.1:2379 --v=7 \
      --client-ca-file ca.crt \
      --kubeconfig ~/.kube/config \
      --authentication-kubeconfig ~/.kube/config \
      --authorization-kubeconfig ~/.kube/config
   ```

   The first kubeconfig is used for the shared informers to access
   Kubernetes resources. The second kubeconfig passed to
   `--authentication-kubeconfig` is used to satisfy the delegated
   authenticator. The third kubeconfig passed to
   `--authorized-kubeconfig` is used to satisfy the delegated
   authorizer. Neither the authenticator, nor the authorizer will
   actually be used: due to `--client-ca-file`, our development X.509
   certificate is accepted and authenticates us as `system:masters`
   member. `system:masters` is the superuser group such that delegated
   authorization is skipped.

5. Use curl to access the server using the client certificate in p12 format for authentication:

   ``` shell
   curl -fv -k --cert-type P12 --cert client.p12:password \
      https://localhost:8443/apis/wardle.example.com/v1alpha1/namespaces/default/flunders
   ```

   Or use wget:
   ``` shell
   wget -O- --no-check-certificate \
      --certificate client.crt --private-key client.key \
      https://localhost:8443/apis/wardle.example.com/v1alpha1/namespaces/default/flunders
   ```

   Note: Recent OSX versions broke client certs with curl. On Mac try `brew install httpie` and then:

   ``` shell
   http --verify=no --cert client.crt --cert-key client.key \
      https://localhost:8443/apis/wardle.example.com/v1alpha1/namespaces/default/flunders
   ```

## Changelog

Kubescape Storage changes are tracked on the [release](https://github.com/kubescape/storage/releases) page.

## Profiling

To profile the Storage APIServer, you can use the `--profiling` flag (enabled by default).
This will expose the profiling endpoints on the `/debug/pprof` path.

To access the profiling endpoints, you have to port-forward the Storage APIServer pod and generate a token:

```shell
kubectl port-forward -n kubescape svc/storage 8443:443
```

```shell
kubectl create serviceaccount k8sadmin -n kube-system
kubectl create clusterrolebinding k8sadmin --clusterrole=cluster-admin --serviceaccount=kube-system:k8sadmin
TOKEN=$(kubectl create token -n kube-system k8sadmin)
curl -k https://localhost:8443/debug/pprof/heap -H "Authorization: Bearer $TOKEN" > heap.out
```

You can also use the following script to generate a heap dump every second:

```shell
#!/usr/bin/env bash
while true; do
  timestamp=$(date '+%Y-%m-%d_%H-%M-%S')
  curl -k https://localhost:8443/debug/pprof/heap -H "Authorization: Bearer $TOKEN" > "$timestamp"_heap.out
  sleep 1
done
```
