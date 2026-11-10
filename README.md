# LACTSSL

自托管的 SSL 证书管理工具：ACME 申请与自动续期、证书部署、站点证书监控和通知。后端 Go + Gin + SQLite，前端 Vue 3 + Element Plus，前端资源嵌入二进制，单文件部署。

## 功能概览

- 通过 DNS-01 申请单域名、多域名与通配符证书，自动续期
- 证书列表支持搜索筛选、详情分析，以及按状态、时间、域名、CA、来源等条件一键清理证书
- 支持 11 家 DNS 服务商、多种 CA，以及本地目录 / SSH / FTP / Webhook / 阿里云 CDN / 七牛云 CDN / 七牛云 Kodo / 宝塔面板 / 1Panel / 雷池 WAF 部署
- 私有 CA 生成、导入与证书/私钥下载，并可作为证书申请的签发机构
- 邮件、Gotify、ServerChan 等 20 余种通知渠道
- 外部站点证书监控、任务流程编排、操作日志
- 系统设置中一键下载加密备份与恢复数据库、配置，安装页支持直接导入备份
- 安全入口、TOTP 两步验证、CSRF 防护、敏感字段加密，中英文界面

## 快速开始

环境要求：Go 1.27+、Node.js 22+。

```bash
cp config.yaml.example config.yaml && chmod 600 config.yaml
cd web && npm install && cd ..
make build
./bin/lactssl
```

启动后访问日志中打印的安全入口，按提示创建管理员账户。使用 Docker 时执行 `docker compose up -d --build`，或直接拉取发布镜像 `ghcr.io/<owner>/lactssl:latest`。

## 文档

完整说明见 [docs](docs/README.md)：

- [安装与启动](docs/install.md)
- [配置](docs/configuration.md)
- [功能说明](docs/features.md)
- [安全](docs/security.md)
- [运维](docs/operations.md)
- [开发](docs/development.md)

## License

MIT，见 [LICENSE](LICENSE)。
