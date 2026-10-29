# syntax=docker/dockerfile:1
ARG ALPINE_VERSION=3.24.3
ARG TARGETARCH
ARG TARGETVARIANT
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
      arm/v5) asset=Xray-linux-arm32-v5.zip ;; \
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

FROM alpine:${ALPINE_VERSION} AS alpine-base
RUN apk add --no-cache tzdata iproute2-minimal ca-certificates

FROM alpine-base AS base-amd64
FROM alpine-base AS base-arm64
FROM alpine-base AS base-arm64v8
FROM alpine-base AS base-armv7

FROM debian:trixie-slim AS armv5-packages
RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends tzdata iproute2 ca-certificates \
    && rm -rf /var/lib/apt/lists/*

FROM armv5-packages AS armv5-runtime-files
RUN set -eu; \
    mkdir -p /runtime/usr/local/sbin /runtime/etc/ssl/certs /runtime/usr/share /runtime/run /runtime/tmp; \
    chmod 1777 /runtime/tmp; \
    cp -L /usr/bin/ip /runtime/usr/local/sbin/ip; \
    for library in $(ldd /usr/bin/ip | awk '/=> \// {print $3} /^[[:space:]]*\// {print $1}'); do \
        cp -L --parents "$library" /runtime; \
    done; \
    cp /etc/ssl/certs/ca-certificates.crt /runtime/etc/ssl/certs/; \
    cp -a /usr/share/zoneinfo /runtime/usr/share/; \
    cp -a /usr/share/common-licenses /runtime/usr/share/; \
    cp /etc/nsswitch.conf /runtime/etc/; \
    mkdir -p /runtime/usr/share/licenses; \
    for package in iproute2 libc6 libselinux1 libbpf1 libelf1t64 libmnl0 libcap2 libpcre2-8-0 zlib1g libzstd1 tzdata ca-certificates; do \
        mkdir -p /runtime/usr/share/licenses/$package; \
        cp /usr/share/doc/$package/copyright /runtime/usr/share/licenses/$package/; \
    done

FROM busybox:1.38.0-glibc AS base-armv5
ENV PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
COPY --from=armv5-runtime-files /runtime/ /

FROM base-${TARGETARCH}${TARGETVARIANT} AS base

FROM base AS test-tools
RUN apk add --no-cache python3 openssl jq curl

FROM base AS runtime
LABEL org.opencontainers.image.title="xray-ui" \
      org.opencontainers.image.description="Ready-to-run VLESS REALITY solution for MikroTik with Xray, TUN and a web panel."
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
