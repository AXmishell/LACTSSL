# 快速开始

用 Docker 启动实例、完成初始化，并签发第一张证书。通常 30 分钟内可以跑通；生产环境加固见[安全基线](security.md)。

## 准备

- 一台能运行 Docker 的 Linux 主机（VPS、NAS 或小主机）
- 一个域名和 DNS API 凭据
- 一个部署目标（本地目录、SSH 或面板）

## 启动实例

```bash
git clone https://github.com/AXmishell/LACTSSL.git
cd LACTSSL
mkdir -p config data
cp config.yaml.example config/config.yaml
chmod 600 config/config.yaml
chmod 700 data
docker compose up -d
docker compose logs -f lactssl
```

仓库默认使用 `1panel-network`；不使用 1Panel 时删除 `docker-compose.yml` 中两处 `networks` 段，或先执行 `docker network create 1panel-network`。GHCR 镜像从首个正式 Release 起可用，未发布时按[安装与启动](install.md)本地构建。

## 初始化

启动日志会打印门禁入口和一次性初始化令牌：

```text
门禁入口: http://127.0.0.1:8080/K7mQ2pX9vR4tY8nL6cWz/
初始化令牌（仅本次启动有效）: ...
```

通过门禁入口创建管理员账户，启用 TOTP 或通行密钥，并设置恢复邮箱。初始化完成后令牌立即失效；忘记入口时可在 `config.yaml` 设置 `security.entry` 并重启覆盖。

> 门禁入口不是安全边界。生产环境必须使用 HTTPS、强密码、TOTP/通行密钥和网络访问控制。

## 启用插件

官方 DNS、部署和通知插件已内置，无需安装：

1. 打开「插件」页，确认对应类别已启用。
2. 启用所需的 DNS 服务商、部署目标或通知渠道。
3. 第三方扩展按需上传 helper，见[插件治理](plugins.md)。

DNS 服务商默认全部停用，部署类别默认开启。

## 签发第一张证书

先用私有 CA 跑通链路，不依赖公网 DNS：

1. 在「私有 CA」生成根 CA（ECDSA P-256）。
2. 在「证书申请」创建任务，签发机构选择该私有 CA，填写测试域名。
3. 添加「部署证书」节点，选择「本地目录」，填写证书/私钥路径和可选重载命令。
4. 添加「通知」节点，选择已启用的渠道，触发时机选「始终」。
5. 手动运行任务，确认证书、部署记录和通知都成功。

再补一条公网链路：创建 DNS 账户 → 选择公共 CA → 用测试域名走 DNS-01 → 部署到目标 → 观察一次自动续期。

## 验证

| 动作 | 验证方式 |
| --- | --- |
| 备份与恢复 | 下载加密备份，在隔离实例导入 |
| 日志 | 筛选并导出操作日志与系统日志 |
| API | 用只读密钥调用 `/api/certificates` |
| 通知 | 发送测试通知 |
| 监控 | 添加一个外部站点并读取证书 |

## 生产上线

用 1–2 个真实域名、一个部署目标和一个内部 CA 场景验证链路。进入生产前完成[安全基线](security.md)的上线检查清单，并按[运维手册](operations.md)落实备份、监控与变更流程。

常见失败原因：

- 只把门禁入口当安全边界。
- 使用个人 DNS 账号做 API 凭据。
- 证书更新后未验证重载命令。
- 只备份数据库，不备份 `config.yaml`；丢失 `salt` 后加密凭据无法解密。
