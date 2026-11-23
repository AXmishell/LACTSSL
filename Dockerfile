# syntax=docker/dockerfile:1

# CA 证书阶段：只在构建机平台上执行一次，最终镜像仅复制生成的证书包。
FROM --platform=$BUILDPLATFORM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf AS certs
RUN apk add --no-cache ca-certificates

# 运行阶段：程序二进制由 CI（.github/workflows/release.yml）或
# scripts/release.sh 在宿主机交叉编译后放入 dist/docker/linux-<arch>/lactssl。
# 备份/恢复由 Go 原生实现，镜像内不需要数据库客户端，也不需要 QEMU。
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

ARG TARGETARCH
ARG VERSION=dev

LABEL org.opencontainers.image.title="LACTSSL" \
      org.opencontainers.image.version="${VERSION}"

WORKDIR /app

# docker-entrypoint.sh 在仓库中是 0644，复制时补上可执行位。
COPY --chmod=0755 dist/docker/linux-${TARGETARCH}/lactssl /app/lactssl
COPY --chmod=0755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/app/data", "/app/config"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q --spider http://127.0.0.1:8080/favicon.svg || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
