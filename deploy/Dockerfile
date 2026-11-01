# ---- frontend -------------------------------------------------------------
FROM node:22-alpine AS web
WORKDIR /src
COPY web/package.json web/package-lock.json ./web/
RUN cd web && npm ci --no-audit --no-fund
COPY web ./web
COPY internal/webui ./internal/webui
RUN cd web && npm run build

# ---- backend --------------------------------------------------------------
FROM golang:1.26-alpine AS build
ARG ACME_VERSION=3.1.6
ARG VERSION=docker
ARG COMMIT=unknown
ARG GOPROXY=https://proxy.golang.org,direct
ARG ACME_SOURCE_URL=
ARG APK_MIRROR=
ENV GOPROXY=${GOPROXY}
WORKDIR /src
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories; fi && \
    apk add --no-cache ca-certificates curl tar
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/webui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/kpccssl ./cmd/kpccssl
RUN mkdir -p /out/acme && \
    URL="${ACME_SOURCE_URL:-https://github.com/acmesh-official/acme.sh/archive/refs/tags/${ACME_VERSION}.tar.gz}" && \
    curl -fsSL "$URL" | tar -xz -C /out/acme --strip-components=1 && \
    cp /out/acme/LICENSE.md /out/acme-LICENSE.md

# ---- runtime --------------------------------------------------------------
FROM alpine:3.21
ARG ACME_VERSION=3.1.6
ARG APK_MIRROR=

RUN if [ -n "$APK_MIRROR" ]; then sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories; fi && \
    apk add --no-cache bash curl openssl socat ca-certificates tzdata wget && \
    addgroup -g 10001 kpccssl && \
    adduser -u 10001 -G kpccssl -D -H -s /sbin/nologin kpccssl && \
    mkdir -p /data /opt/acme.sh /licenses && \
    chown -R kpccssl:kpccssl /data /opt/acme.sh

COPY --from=build /out/kpccssl /usr/local/bin/kpccssl
COPY --from=build /out/acme /opt/acme.sh
COPY --from=build /out/acme-LICENSE.md /licenses/acme.sh-LICENSE.md
COPY LICENSE /licenses/KPCCSSL-LICENSE

RUN chmod 0755 /usr/local/bin/kpccssl /opt/acme.sh/acme.sh && \
    printf 'acme.sh %s (GPLv3) is bundled under /opt/acme.sh; see /licenses.\n' "${ACME_VERSION}" > /licenses/README.txt

ENV KPCC_DATA_DIR=/data \
    KPCC_ACME_BIN=/opt/acme.sh/acme.sh \
    KPCC_LISTEN=:8080 \
    KPCC_LOG_LEVEL=info

USER kpccssl
VOLUME ["/data"]
EXPOSE 8080 8081 8443

HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/api/v1/system/health || exit 1

ENTRYPOINT ["/usr/local/bin/kpccssl"]
CMD ["serve"]
