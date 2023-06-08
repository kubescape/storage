FROM golang:1.19-alpine as builder

ENV GO111MODULE=on CGO_ENABLED=0
WORKDIR /work

ADD go.mod go.sum /work/
RUN go mod download

ADD . .
RUN go build -a -o build/storage

FROM alpine

RUN addgroup -S ks && adduser -S ks -G ks
USER ks
WORKDIR /home/ks/

COPY --from=builder /work/build/storage /usr/bin/storage

ARG image_version
ENV RELEASE=$image_version

ENTRYPOINT ["storage"]
