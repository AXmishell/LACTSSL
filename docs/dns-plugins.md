# DNS 插件

LACTSSL 的 DNS 服务商统一通过 DNS 插件提供：

- **默认主程序不再编译任何 DNS 服务商**，只负责加载 DNS 插件，二进制更小、内存更低。
- **迁移前内置的 35 家服务商**（阿里云、Cloudflare、DNSPod、腾讯云、华为云、火山引擎、Route53、Azure 等）改由 helper 插件提供，并**默认启用**，升级后行为保持不变。
- **lego 其余服务商**同样由 helper 提供，默认停用，可在插件页按需启用。
- **插件集合**：`helper-extra` 与 `helper-full` 都包含全部 194 家服务商。
- **完整版**仍可使用 `-tags dns_all` 构建，把 lego 全部 DNS 服务商编译进主程序。
- **外部插件**通过一次性 exec helper 提供服务商，无需重新构建主程序；新增服务商也可以直接上传 helper。

## 构建版本

```bash
make build        # 精简版 bin/lactssl（不内置服务商，通过插件加载）
make build-full   # 完整版 bin/lactssl-full（全部 194 家编译进主程序）
make helper       # 参考 helper（全部 194 家）
make helper-extra # 插件版 helper（全部 194 家）
make helper-full  # 参考 helper（全部服务商）
make gen-plugins  # 重新生成插件包装包与 profile
```

构建 profile 通过构建标签互斥选择：

| 标签 | 内容 | 用途 |
|---|---|---|
| 无 | 空 | 默认主程序（仅加载插件） |
| `dns_extra` | 全部 194 家 | `helper-extra` / `helper` 插件 |
| `dns_all` | 全部 194 家 | 完整版主程序 / helper-full |
| `embed_helper` | 内嵌 helper | 发布版精简主程序（与 `embed` 一起使用） |

精简集合由 `scripts/dns_plugins.json` 的 `lite` 字段决定，默认为空；确需把某些服务商直接编进主程序时，在 `lite` 中追加 ID 并执行 `make gen-plugins`。

未安装 helper 时 DNS 服务商列表为空；安装 helper 后，迁移前内置的 35 家会自动出现在列表中。历史账号配置不会丢失，服务商不可用时签发会给出明确错误。

## 插件目录

DNS 外部插件放在 `<data_dir>/plugins/dns`（默认为 `data/plugins/dns`），也可以在配置中指定。部署插件使用兄弟目录 `<data_dir>/plugins/deploy`，两类 helper 不会混放：

```yaml
plugins:
  enabled: true
  dir: ""                 # 留空表示 <data_dir>/plugins/dns
  memory_limit_mb: 128    # 单个 helper 常驻内存上限
  timeout_seconds: 60     # 单次 present/cleanup 超时
  max_output_kb: 64       # helper 输出上限
```

启动时自动扫描；新增插件后可以在「插件」页面点击「重扫插件」，或调用：

```bash
curl -X POST https://<host>/<entry>/api/plugins/rescan
```

Release 包中的精简版 `lactssl` 已内嵌 `lactssl-dns-helper-extra`：首次启动会自动安装到 `<data_dir>/plugins/dns`，内置 helper 版本变化时也会自动更新，因此无需手动复制即可使用内置服务商。Docker 镜像使用同样的内嵌方式。

也可以手动安装或替换 helper；如果使用其他文件名，不会被内置 helper 覆盖：

```bash
mkdir -p data/plugins/dns
cp lactssl-dns-helper-extra data/plugins/dns/
chmod 0755 data/plugins/dns/lactssl-dns-helper-extra
```

完整版（`dns_all`）已把全部服务商编译进主程序，不需要 helper。

## 上传插件

「插件」页的 DNS 插件卡片右上角提供「上传插件」，可以直接安装 helper 可执行文件，无需登录服务器复制文件：

- 仅接受单个 helper 可执行文件，最大 256 MB；上传后会先以临时文件探测 `manifest` 并校验协议、服务商 ID 与 `sha256`，通过后才写入插件目录并立即重扫。
- 保存文件名可自定义，只能包含字母、数字、点、下划线和连字符，且需以字母或数字开头。
- 默认拒绝覆盖同名文件；升级插件时勾选「允许覆盖同名插件」，旧文件会先备份，写入失败时自动恢复。
- 上传的 helper 会以服务账号执行，因此该操作始终要求二次验证，不随「需要二次验证的操作」开关关闭。
- 非法或无法执行的文件不会被安装，插件目录不会留下半成品。
- 自动化接口：`POST /<entry>/api/plugins/upload`，multipart 字段为 `kind`（`dns` 或 `deploy`）、`file`、`name`、`overwrite`；需要先完成二次验证并在 `X-LACTSSL-VERIFY` 请求头中携带 token。

## 启用与停用服务商

服务商启用状态由插件白名单管理：

- 首次启动（settings 中尚无白名单）时，迁移前内置的 35 家服务商默认启用，其余插件服务商默认停用。
- 默认启用的 35 家：alidns、aliesa、cloudflare、dnspod、tencentcloud、huaweicloud、volcengine、baiducloud、jdcloud、digitalocean、godaddy、namecheap、namesilo、porkbun、spaceship、cloudns、bunny、rainyun、westcn、pdns、dnsimple、dnsmadeeasy、route53、gcloud、azuredns、netlify、vercel、duckdns、freemyip、vultr、linode、hetzner、gandi、gandiv5、rfc2136。
- 启用状态保存在 settings 表的 `plugins.dns.enabled_providers`，只记录已启用的服务商 ID，重启和重扫后保持。
- 只有已启用的插件服务商才会出现在 DNS 服务商列表和账户创建下拉框中；停用后立即隐藏。
- 默认启用的服务商同样可以停用；停用结果会持久化，不会因为 helper 重启或重新扫描而恢复。
- 历史 DNS 账户与任务配置不会丢失，服务商不可用时账户页会提示，签发时返回明确错误。
- 接口：`PUT /<entry>/api/plugins/providers/{id}`，请求体 `{"enabled": true}`；批量操作为 `PUT /<entry>/api/plugins/providers`，请求体 `{"enabled": true, "ids": []}`（`ids` 为空表示当前发现的全部插件服务商）。
- 启用或停用后会立即重新探测插件目录并重建注册表，与「重扫插件」使用同一套校验流程。

## Helper 协议 v1

helper 是一个可执行文件，支持三个子命令：

```bash
dns-helper manifest   # 输出 JSON 清单
dns-helper present    # 从 stdin 读取请求，添加 TXT 记录
dns-helper cleanup    # 从 stdin 读取请求，删除 TXT 记录
```

`manifest` 输出示例：

```json
{
  "protocol": "v1",
  "name": "example dns helper",
  "version": "1.0.0",
  "sha256": "可选的 helper 文件校验和",
  "providers": [
    {
      "id": "example-dns",
      "name": "Example DNS",
      "description": "示例服务商",
      "aliases": ["example"],
      "fields": [
        {"key": "api_token", "label": "API Token", "type": "password", "required": true}
      ]
    }
  ]
}
```

`present` / `cleanup` 的 stdin：

```json
{
  "action": "present",
  "provider": "example-dns",
  "config": {"api_token": "..."},
  "domain": "example.com",
  "token": "acme-token",
  "key_auth": "key-authorization"
}
```

约定：

- 退出码 `0` 表示成功，非 `0` 表示失败；stderr 会进入任务日志（敏感值会被脱敏）。
- stdout 保持为空；`present/cleanup` 的输出会被视为错误。
- helper 从 `LACTSSL_PLUGIN_MEMORY_LIMIT`（字节）获得内存预算，建议在启动时调用 `debug.SetMemoryLimit`。
- 主程序会周期性检查 helper 的 `VmRSS`，超过 `plugins.memory_limit_mb` 时结束进程并记录错误。
- 主程序只传递最小环境变量集合（PATH、HOME、代理、TLS 证书等），不会泄漏服务端密钥。
- helper 文件不能允许其他用户写入；`manifest.sha256` 非空时会校验文件摘要。
- `version` 仅用于展示，应表示 helper/服务商实现自身的版本；参考 helper 返回 lego 上游版本，而不是 LACTSSL 的构建版本。界面只在版本号尚未带 `v` 前缀时补一个 `v`。

## 复用 lego 服务商

参考实现 `cmd/dns-helper` 直接把已编译的 lego 服务商暴露为插件。默认构建与插件集合互补：

```bash
make helper-extra
mkdir -p data/plugins/dns
cp bin/lactssl-dns-helper-extra data/plugins/dns/dns-helper-extra
chmod 0755 data/plugins/dns/dns-helper-extra
```

`helper-extra` 包含全部 194 家服务商；签发时按需启动 helper，任务结束进程退出，不常驻内存。如果希望主程序直接内置全部服务商，可使用完整版：

```bash
make build-full
# 或
make helper-full
cp bin/lactssl-dns-helper-full data/plugins/dns/dns-helper-full
chmod 0755 data/plugins/dns/dns-helper-full
```

完整版（`dns_all`）会把服务商直接编译进主程序，内置 ID 优先于 helper，helper 中重复的 ID 会被忽略并在重扫结果中列出。Release 产物中已包含 `lactssl-dns-helper-extra`（Windows 为 `.exe`），安装后放入插件目录并重扫即可。

## 安全建议

- 插件目录只允许服务运行用户写入，优先 `0700`。
- 只安装可信来源的 helper，并在 manifest 中填写 `sha256`。
- 设置合理的 `memory_limit_mb` 与 `timeout_seconds`，避免恶意或异常插件拖垮主机。
- 插件以服务账号运行，不要给 `lactssl` 用户额外权限。
