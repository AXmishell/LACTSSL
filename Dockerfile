# 前端构建阶段：固定在构建机平台，多架构构建时只执行一次
FROM --platform=$BUILDPLATFORM node:24-alpine@sha256:116b800c3754b440ee0aa3ed2f20f5bf3d99a554bfd9adeea09ea8017f6e85f5 AS web
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Go 构建阶段：在构建机平台交叉编译到目标平台，避免 arm64 走 QEMU 编译
FROM --platform=$BUILDPLATFORM golang:1.27-alpine@sha256:eb61c3873c97416fff0116553fff65d67a3549f72e11d9758849a689cdd8b523 AS build
RUN apk add --no-cache ca-certificates tzdata
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
# 官方构建传入 32 字节原始公钥（base64）后，镜像内置在线升级验签公钥；
# 自编译留空即可，由管理员在 config.yaml 中显式配置。
ARG PROGRAM_PUBLIC_KEY=
RUN set -eu; \
    build_version="${VERSION:-dev}"; \
    build_os="${TARGETOS:-linux}"; \
    build_arch="${TARGETARCH:-$(go env GOARCH)}"; \
    build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
    ldflags="-s -w -X 'lactssl/internal/api.Version=$build_version' -X 'lactssl/internal/api.BuildTime=$build_time' -X 'lactssl/internal/api.BuildArch=$build_os/$build_arch'"; \
    if [ -n "${PROGRAM_PUBLIC_KEY:-}" ]; then \
      ldflags="$ldflags -X 'lactssl/internal/config.OfficialProgramPublicKey=${PROGRAM_PUBLIC_KEY}'"; \
    fi; \
    CGO_ENABLED=0 GOOS="$build_os" GOARCH="$build_arch" go build -trimpath -tags "embed" \
      -ldflags "$ldflags" \
      -o /lactssl ./cmd/server

# 运行阶段
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
RUN apk add --no-cache ca-certificates tzdata openssh-client su-exec postgresql-client mariadb-client \
    && addgroup -S -g 10001 lactssl \
    && adduser -S -D -H -u 10001 -G lactssl lactssl \
    && mkdir -p /app/data /app/config \
    && chown -R lactssl:lactssl /app
WORKDIR /app
# 主程序不再内嵌插件；插件通过「插件仓库」按需下载到 <data_dir>/plugins。
COPY --from=build /lactssl /app/lactssl
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 0755 /usr/local/bin/docker-entrypoint.sh
ENV TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/app/data", "/app/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q --spider http://127.0.0.1:8080/favicon.svg || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
