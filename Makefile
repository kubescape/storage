DOCKERFILE_PATH=./build/Dockerfile
BINARY_NAME=storage

TAG?=test
IMAGE?=quay.io/kubescape/$(BINARY_NAME)


.PHONY: build test perf-ab docker-build docker-push

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)

test:
	go test ./...

# Paired A/B of HEAD against its merge-base on this machine (Tier B of the
# storage measurement harness). Mandatory before SHIP for any change under
# pkg/registry/file/{storage,singlewriter,containerprofile_*,sqlite}.go; quote
# its verdict line in the PR. BASE=<sha> and PAIRS=<n> override the defaults.
perf-ab:
	hack/perf-ab.sh

docker-build:
	docker buildx build --platform linux/amd64 -t $(IMAGE):$(TAG) --load -f $(DOCKERFILE_PATH) .
docker-push:
	docker push $(IMAGE):$(TAG)
