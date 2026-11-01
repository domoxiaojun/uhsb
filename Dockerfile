# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.22
ARG SINGBOX_VERSION=1.14.4

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cfweb ./cfweb \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cfdata ./cfdata \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cfscan ./cfscan \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cfspeed ./cfspeed \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/cfconnectivity ./cfconnectivity

FROM debian:bookworm-slim AS singbox
ARG SINGBOX_VERSION
ARG TARGETARCH
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl tar \
 && rm -rf /var/lib/apt/lists/* \
 && arch="$TARGETARCH" \
 && case "$arch" in \
      amd64) sb_arch=amd64 ;; \
      arm64) sb_arch=arm64 ;; \
      *) echo "unsupported arch: $arch" >&2; exit 1 ;; \
    esac \
 && curl -fsSL "https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-${sb_arch}.tar.gz" \
    | tar -xz --strip-components=1 -C /usr/local/bin "sing-box-${SINGBOX_VERSION}-linux-${sb_arch}/sing-box" \
 && chmod +x /usr/local/bin/sing-box \
 && /usr/local/bin/sing-box version

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl mtr-tiny tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /app /data /data/runs
COPY --from=build /out/ /usr/local/bin/
COPY --from=singbox /usr/local/bin/sing-box /usr/local/bin/sing-box
COPY locations.json /app/locations.json
COPY ips-v4.txt /app/ips-v4.txt
COPY scripts/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
WORKDIR /data
ENV CFWEB_ADDR=0.0.0.0:8787 \
    CFWEB_OPEN=false \
    CFWEB_ROOT=/data \
    PATH=/usr/local/bin:$PATH
EXPOSE 8787
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s \
  CMD curl -fsS http://127.0.0.1:8787/api/status >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["cfweb", "-addr", "0.0.0.0:8787", "-root", "/data", "-open=false"]
