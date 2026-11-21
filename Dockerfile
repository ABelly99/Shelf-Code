# syntax=docker/dockerfile:1
# SHELFCODE container image: a single static program, no shell, data in /data.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN GOARM="${TARGETVARIANT#v}" CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -trimpath -tags timetzdata -ldflags "-s -w -X main.appVersion=${VERSION}" -o /out/shelfcode . \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12
ARG VERSION=dev
LABEL org.opencontainers.image.title="SHELFCODE" \
      org.opencontainers.image.description="Self-hosted barcode inventory database" \
      org.opencontainers.image.source="https://github.com/ABelly99/Shelf-Code" \
      org.opencontainers.image.licenses="CC0-1.0" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /out/shelfcode /shelfcode
COPY --from=build /out/data /data
ENV SHELFCODE_HEADLESS=1 \
    SHELFCODE_DATA=/data \
    SHELFCODE_PORT=8765
EXPOSE 8765
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/shelfcode", "healthcheck"]
ENTRYPOINT ["/shelfcode"]
