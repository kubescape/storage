FROM --platform=$BUILDPLATFORM golang:1.22-bullseye AS builder

ENV GO111MODULE=on CGO_ENABLED=0
WORKDIR /work
ARG TARGETOS TARGETARCH

RUN --mount=target=. \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/storage .

FROM gcr.io/distroless/static-debian11:nonroot

COPY --from=builder /out/storage /usr/bin/storage

ARG image_version
ENV RELEASE=$image_version

ENTRYPOINT ["storage"]
