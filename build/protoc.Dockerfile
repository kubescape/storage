FROM --platform=$BUILDPLATFORM golang:1.25-trixie AS builder

ENV GO111MODULE=on CGO_ENABLED=0
WORKDIR /work

RUN git clone -q --depth 1 https://github.com/kubernetes/kubernetes.git /go/src/k8s.io/kubernetes
RUN go install github.com/gogo/protobuf/protoc-gen-gogo@latest
RUN go install golang.org/x/tools/cmd/goimports@latest
RUN go install k8s.io/code-generator/cmd/go-to-protobuf@latest
RUN apt-get update && apt-get install -y unzip
RUN wget -q -O /tmp/protoc.zip https://github.com/protocolbuffers/protobuf/releases/download/v28.0/protoc-28.0-linux-x86_64.zip && \
    unzip -q /tmp/protoc.zip -d /usr/local && \
    rm /tmp/protoc.zip
