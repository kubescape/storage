APISERVER_BIN?=kube-sample-apiserver
SIMPLE_IMAGE_PATH?=./artifacts/simple-image

IMAGE?=vklokun/$(APISERVER_BIN)


binary:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o $(SIMPLE_IMAGE_PATH)/$(APISERVER_BIN)

docker-build:
	docker build -t $(IMAGE):$(TAG) $(SIMPLE_IMAGE_PATH)
docker-push:
	docker push $(IMAGE):$(TAG)
