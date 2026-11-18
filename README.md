# LACTSSL

面向企业 IT、安全和运维团队的自托管 SSL 证书管理平台：ACME 申请与自动续期、证书部署、站点证书监控、通知，以及内部私有 CA / ACME 服务。

完整文档：<https://lactssl.gpcn.cc/>

## 适用场景

- 多域名、多业务线证书的集中签发、续期和交付。
- 内网服务的私有 CA 签发，以及面向 certbot / lego / acme.sh 的标准 ACME 端点。
- 证书到期治理、部署失败告警和审计留痕。
- 通过 API 密钥接入 CI/CD、CMDB、巡检和证书分发。

## 交付边界

- 自托管单实例、单租户、单管理员账户。
- API 密钥按 `资源:动作` 分权，适合机器身份；不提供多用户 RBAC、SSO、多租户。
- SQLite 单写，不提供内置双活集群；生产建议单活 + 加密备份 + 恢复演练。
- 私钥在本地数据库加密存储；不提供 HSM/KMS 托管。
- 不附带等保、ISO 27001 等认证证书。

## 文档入口

- [评估与上线](docs/getting-started.md)：试点、验收、生产切换。
- [部署架构](docs/architecture.md)：拓扑、端口、容量、可用性。
- [管理工作台](docs/administration.md)：账户、API 密钥、审计和职责。
- [安全基线](docs/security.md)：身份、加密、审计、供应链和控制项映射。
- [运维手册](docs/operations.md)：备份恢复、监控、变更和演练。
- [API 集成](docs/api.md)：密钥权限和自动化示例。
- [监控与排障](docs/troubleshooting.md)：常见故障和应急处理。

## 仓库结构

- 本仓库：插件宿主、ACME、部署编排、通知、Web UI。
- [LACTSSL-Plugins](https://github.com/AXmishell/LACTSSL-Plugins)：DNS / 部署 / 通知插件实现、官方 helper、插件协议与元数据。

主程序发布包不内置任何 DNS / 部署 / 通知插件。官方插件仓库按类型提供合并包（DNS 常用、DNS 大型云厂商、部署、通知），另提供 DNS 全量·重构包；每个包内含该类多个实现并共享一份 Go 运行时。首次启动后，在「插件」页确认插件仓库地址并点击「刷新索引」，然后在「插件市场」中按需安装；安装、更新、卸载只需登录会话或相应 API 权限。

主程序与插件仓库只通过运行期契约集成：签名索引、插件协议 v1 与 `min_host`。两个仓库可以独立构建、测试和发布，插件仓库版本不再写入主仓库 `go.mod`。本地联调时如需直接构建插件仓库，可临时创建 `go.work`，不要提交。

## License

MIT，见 [LICENSE](LICENSE)。
