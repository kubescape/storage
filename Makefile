DOCKERFILE_PATH=./build/Dockerfile
BINARY_NAME=storage-apiserver

IMAGE?=quay.io/kubescape/$(BINARY_NAME)


binary:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME)

docker-build:
	docker build -t $(IMAGE):$(TAG) -f $(DOCKERFILE_PATH) .
docker-push:
	docker push $(IMAGE):$(TAG)
