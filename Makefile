DOCKERFILE_PATH=./build/Dockerfile
BINARY_NAME=storage

IMAGE?=docker.io/armoafekb/afek-b-tests
TAG?=storage-test

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)

docker-build:
	docker buildx build --platform linux/amd64 -t $(IMAGE):$(TAG) -f $(DOCKERFILE_PATH) .
docker-push:
	docker push $(IMAGE):$(TAG)
