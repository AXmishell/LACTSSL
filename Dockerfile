# 前端构建阶段：固定在构建机平台，多架构构建时只执行一次
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Go 构建阶段：在构建机平台交叉编译到目标平台，避免 arm64 走 QEMU 编译
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
RUN apk add --no-cache git ca-certificates tzdata
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/web/dist ./internal/webui/dist

# TARGETOS/TARGETARCH 由 BuildKit 自动注入；VERSION 可由 --build-arg 覆盖
ARG TARGETOS
ARG TARGETARCH
ARG VERSION

RUN set -eu; \
    build_version="${VERSION:-$(tr -d '[:space:]' < VERSION)}"; \
    build_os="${TARGETOS:-linux}"; \
    build_arch="${TARGETARCH:-$(go env GOARCH)}"; \
    build_time="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
    CGO_ENABLED=0 GOOS="$build_os" GOARCH="$build_arch" go build -trimpath -tags embed \
      -ldflags "-s -w -X 'lactssl/internal/api.Version=$build_version' -X 'lactssl/internal/api.BuildTime=$build_time' -X 'lactssl/internal/api.BuildArch=$build_os/$build_arch'" \
      -o /lactssl ./cmd/server

# 运行阶段
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata openssh-client
WORKDIR /app
COPY --from=build /lactssl /app/lactssl
ENV LACTSSL_DATA=/app/data \
    LACTSSL_CONFIG=/app/config/config.yaml \
    TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/app/data", "/app/config"]
ENTRYPOINT ["/app/lactssl"]
