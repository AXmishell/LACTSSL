# syntax=docker/dockerfile:1

# CA 证书阶段：只在构建机平台上执行一次，最终镜像仅复制生成的证书包。
# 运行阶段的 apk add 会安装数据库客户端，多架构构建 arm64 时需要 QEMU。
FROM --platform=$BUILDPLATFORM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf AS certs
RUN apk add --no-cache ca-certificates

# 运行阶段：程序二进制由 CI（.github/workflows/release.yml）或
# scripts/release.sh 在宿主机交叉编译后放入 dist/docker/linux-<arch>/lactssl。
# 运行时只保留 alpine 基础层、程序本体、CA 证书，以及数据库备份/恢复所需的
# pg_dump/pg_restore/mysqldump/mysql 精简客户端。
FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

ARG TARGETARCH
ARG VERSION=dev

LABEL org.opencontainers.image.title="LACTSSL" \
      org.opencontainers.image.version="${VERSION}"

# mariadb-client 完整安装约 37MB，其中大部分是 mariadb-admin/import/show 等
# 本程序不用的工具。这里只保留 dump/restore 需要的 mariadb、mariadb-dump
# 及其 mysql/mysqldump 兼容软链，并删除 PostgreSQL 客户端的其余命令行工具。
RUN set -eu; \
    apk add --no-cache postgresql-client mariadb-client; \
    find /usr/bin -maxdepth 1 -type f \
      \( -name 'mariadb-*' -o -name 'mysql*' -o -name 'myisam_*' \) \
      ! -name 'mariadb' ! -name 'mariadb-dump' -delete; \
    find /usr/libexec/postgresql18 -maxdepth 1 -type f \
      ! -name 'pg_dump' ! -name 'pg_restore' -delete; \
    for f in /usr/bin/*; do \
      if [ -L "$f" ] && [ ! -e "$f" ]; then rm -f "$f"; fi; \
    done; \
    rm -rf /var/cache/apk/* /usr/share/man /usr/share/doc

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
