# syntax=docker/dockerfile:1
ARG ALPINE_VERSION=3.24.3
FROM --platform=$BUILDPLATFORM alpine:${ALPINE_VERSION} AS extract
ARG TARGETARCH
ARG TARGETVARIANT
ARG XRAY_VERSION=26.9.9
RUN apk add --no-cache curl unzip
COPY build/xray-checksums.txt /tmp/xray-checksums.txt
RUN set -eu; \
    case "$TARGETARCH/$TARGETVARIANT" in \
      amd64/) asset=Xray-linux-64.zip ;; \
      arm64/|arm64/v8) asset=Xray-linux-arm64-v8a.zip ;; \
      arm/v7) asset=Xray-linux-arm32-v7a.zip ;; \
      *) echo "Unsupported platform: $TARGETARCH/$TARGETVARIANT" >&2; exit 1 ;; \
    esac; \
    checksum=$(awk -v file="$asset" '$2 == file {print $1}' /tmp/xray-checksums.txt); \
    test -n "$checksum"; \
    curl -fsSL --retry 3 "https://github.com/XTLS/Xray-core/releases/download/v${XRAY_VERSION}/${asset}" -o /tmp/xray.zip; \
    echo "$checksum  /tmp/xray.zip" | sha256sum -c -; \
    mkdir -p /out; \
    unzip -j /tmp/xray.zip xray LICENSE -d /out; \
    chmod 755 /out/xray

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24 AS manager-build
WORKDIR /src
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
ARG TARGETARCH
ARG TARGETVARIANT
RUN --mount=type=cache,target=/root/.cache/go-build \
    set -eu; \
    if [ "$TARGETARCH" = arm ]; then export GOARM="${TARGETVARIANT#v}"; fi; \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -buildvcs=false -trimpath -ldflags="-s -w -buildid=" -o /out/xray-manager ./cmd/xray-manager

FROM alpine:${ALPINE_VERSION} AS base
RUN apk add --no-cache tzdata iproute2-minimal ca-certificates

FROM base AS test-tools
RUN apk add --no-cache python3 openssl jq curl

FROM base AS runtime
LABEL org.opencontainers.image.title="xray-ui" \
      org.opencontainers.image.description="Ready-to-run VLESS REALITY solution for MikroTik with Xray, TUN and a web panel."
RUN mkdir -p /opt/xray/state && chmod 700 /opt/xray/state
COPY --from=extract /out/xray /opt/xray/xray
COPY --from=extract /out/LICENSE /usr/share/licenses/xray/LICENSE
COPY --from=manager-build /out/xray-manager /opt/xray-manager
EXPOSE 8080
ENTRYPOINT ["/opt/xray-manager"]
CMD []

FROM test-tools AS test
COPY --from=runtime /opt/ /opt/
COPY tests/ /tests/
ENTRYPOINT ["/bin/sh"]
CMD ["/tests/panel-runtime.sh"]

FROM runtime AS final
