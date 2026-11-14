# 前端构建阶段：固定在构建机平台，多架构构建时只执行一次
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:0e1754758c2df4cfa3e68d59e2e1b7374bb7fd9ea6328eaaacf15cc934af8202 AS web
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Go 构建阶段：在构建机平台交叉编译到目标平台，避免 arm64 走 QEMU 编译
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS build
RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./internal/webui/dist

# TARGETOS/TARGETARCH 由 BuildKit 自动注入；版本号由 release workflow、
# scripts/release.sh 或 docker compose 通过 --build-arg VERSION 传入，缺省为 dev。
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
# 构建标签：默认 embed（主程序不内置 DNS 服务商）；使用 --build-arg TAGS="embed dns_all" 构建全量版。
ARG TAGS=embed

RUN set -eu; \
    build_version="${VERSION:-dev}"; \
    build_os="${TARGETOS:-linux}"; \
    build_arch="${TARGETARCH:-$(go env GOARCH)}"; \
    build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
    CGO_ENABLED=0 GOOS="$build_os" GOARCH="$build_arch" go build -trimpath -tags dns_extra \
      -ldflags "-s -w -X 'lactssl/internal/api.Version=$build_version'" \
      -o /lactssl-dns-helper-extra ./cmd/dns-helper; \
    mkdir -p internal/dnsplugin/bundled; \
    cp /lactssl-dns-helper-extra internal/dnsplugin/bundled/lactssl-dns-helper-extra; \
    server_tags="$TAGS"; \
    case "$server_tags" in *dns_all*) ;; *) server_tags="$server_tags embed_helper" ;; esac; \
    CGO_ENABLED=0 GOOS="$build_os" GOARCH="$build_arch" go build -trimpath -tags "$server_tags" \
      -ldflags "-s -w -X 'lactssl/internal/api.Version=$build_version' -X 'lactssl/internal/api.BuildTime=$build_time' -X 'lactssl/internal/api.BuildArch=$build_os/$build_arch'" \
      -o /lactssl ./cmd/server; \
    rm -f internal/dnsplugin/bundled/lactssl-dns-helper-extra; \
    CGO_ENABLED=0 GOOS="$build_os" GOARCH="$build_arch" go build -trimpath \
      -ldflags "-s -w -X 'lactssl/internal/api.Version=$build_version'" \
      -o /lactssl-deploy-helper ./cmd/deploy-helper

# 运行阶段
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates tzdata openssh-client su-exec \
    && addgroup -S -g 10001 lactssl \
    && adduser -S -D -H -u 10001 -G lactssl lactssl \
    && mkdir -p /app/data /app/config \
    && chown -R lactssl:lactssl /app
WORKDIR /app
# 精简版主程序内嵌 DNS helper，首次启动会自动安装到 <data_dir>/plugins/dns。
COPY --from=build /lactssl /app/lactssl
# 部署插件参考 helper：主程序只内置本地文件与 SSH，其余目标可按需安装到
# <data_dir>/plugins/deploy 并在配置中启用 deploy_plugins。
COPY --from=build /lactssl-deploy-helper /app/lactssl-deploy-helper
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh /app/lactssl-deploy-helper
ENV LACTSSL_DATA=/app/data \
    LACTSSL_CONFIG=/app/config/config.yaml \
    TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/app/data", "/app/config"]
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
