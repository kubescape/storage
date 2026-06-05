DOCKERFILE_PATH=./build/Dockerfile
BINARY_NAME=storage

TAG?=test
IMAGE?=quay.io/kubescape/$(BINARY_NAME)


.PHONY: build test docker-build docker-push

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)

test:
	go test ./...

docker-build:
	docker buildx build --platform linux/amd64 -t $(IMAGE):$(TAG) --load -f $(DOCKERFILE_PATH) .
docker-push:
	docker push $(IMAGE):$(TAG)
