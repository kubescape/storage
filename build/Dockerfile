FROM --platform=$BUILDPLATFORM golang:1.23-bookworm AS builder

ENV GO111MODULE=on CGO_ENABLED=0
WORKDIR /work
ARG TARGETOS TARGETARCH TARGETPLATFORM

RUN --mount=target=. \
    --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/storage .

FROM gcr.io/distroless/static-debian12:nonroot AS linux
COPY --from=builder /out/storage /usr/bin/storage
ENTRYPOINT ["storage"]

FROM mcr.microsoft.com/windows/nanoserver:ltsc2025 AS windows
COPY --from=builder /out/storage C:/storage.exe
ENTRYPOINT ["C:\\storage.exe"]

FROM $TARGETOS
ARG image_version
ENV RELEASE=$image_version
