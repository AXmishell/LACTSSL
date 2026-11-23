# 索引签名与密钥轮换

插件索引使用 Ed25519 分离签名：`index.json` 是原始字节，`index.json.sig` 是 base64 签名。宿主在解析 JSON 前验证原始字节，避免 JSON 重新序列化导致签名失效。

新版本同时发布 `index.json.sig.json` 签名封套，携带 `key_id`、`index_sha256`、`revision` 和可选的多个签名；新宿主优先读取封套，旧宿主继续读取 `index.json.sig`。

封套中的签名输入仍然是 **`index.json` 的原始字节**，不是 `index_sha256` 字符串；`index_sha256` 只是完整性提示。`key_id` 的格式是 `SHA256:` + base64.RawStdEncoding(SHA256(公钥))。`revision` 和 `signed_at` 不单独参与签名，宿主做防重放时必须使用 `index.json` 内的 `revision`；签名工具会校验封套 revision 与索引一致，并按 `key_id` 选择对应公钥验签。

索引中的 `revision` 是单调递增整数，由 `pluginctl index` 基于上一份索引自动加一；宿主会拒绝低于已接受值的索引，用于防止已签名旧索引被重放。首次从没有 `revision` 的历史索引迁移时从 `1` 开始。

`expires_at` 默认是生成时间加 30 天（`-valid-for 720h`）；发布流程要求它存在且晚于 `generated_at`。离线镜像或长期不发布的场景可以调整 `-valid-for`，设为 `0` 时不写该字段，但官方 Release 会拒绝这种索引。Release workflow 每周一自动运行一次；没有插件变更时只会重写并重新签名索引，用于刷新 `revision` 与 `expires_at`。

索引结构由 `scripts/indexschema/index.schema.json` 约束；`pluginctl index` 生成时自动校验，发布流程在签名前还会执行 `pluginctl validate -in index.json`。

## 1. 签名工具

签名使用独立命令，**不导入任何插件包**：

```bash
go run ./cmd/signer sign \
  -key /secure/plugin-index-key.b64 \
  -in index.json \
  -out index.json.sig

go run ./cmd/signer verify \
  -pub "$(cat /secure/plugin-index-public.b64)" \
  -in index.json \
  -sig index.json.sig

# 生成 JSON 签名封套（新宿主优先使用）
go run ./cmd/signer sign -envelope \
  -key /secure/plugin-index-key.b64 \
  -in index.json \
  -out index.json.sig.json

go run ./cmd/signer verify \
  -pub "$(cat /secure/plugin-index-public.b64)" \
  -in index.json \
  -sig index.json.sig.json
```

`cmd/signer` 只依赖标准库，避免 `go run` 时执行 `catalog`、`i18n`、`notify`、`dns`、`deploy` 等包的 `init()`。

**私钥要求**

- 优先使用 HSM/KMS 或独立签名服务；当前 `cmd/signer` 尚未内置 KMS/HSM 接口，CI 会通过临时文件把私钥交给独立签名进程。若接入外部签名服务，应只发送 SHA256 digest。
- 如果只能使用文件：放在受保护 GitHub Environment 或离线签名机，`chmod 600`，不要放在仓库目录/家目录/备份中。
- 不要通过普通环境变量向任意 `go run` 传递私钥。
- 发布工作流会使用临时文件 + `umask 077` + `trap` 清理，并在签名后立即验签。

## 2. 宿主公钥配置

官方宿主的 `plugins.repo.public_key` 支持**一个或多个** base64 公钥，用逗号或空白分隔：

```yaml
plugins:
  repo:
    public_key: "OLDPUBLICKEYBASE64,NEWPUBLICKEYBASE64"
```

验签逻辑是“任意一把受信公钥通过即可”。这样可以在不改配置结构的情况下完成双密钥过渡。

## 3. 轮换流程（不中断旧宿主）

1. **离线生成新密钥**，记录公钥指纹：

   ```bash
   # 推荐使用仓库内独立工具生成（PEM，0600；公钥 base64 同时输出）
   go run ./cmd/signer keygen -out /secure/new-index-key.pem -public-out /secure/new-index-key.pub
   ```

   公钥的 base64（32 字节）用于 `plugins.repo.public_key`，指纹为
   `SHA256:base64(SHA256(pub))`。

2. **先升级宿主**：在新宿主中把 `public_key` 配成 `旧公钥,新公钥`。旧宿主继续只信任旧公钥，行为不变。
3. **双签过渡**：
   - release workflow 用 `PLUGIN_INDEX_SIGNING_KEY` 签 `index.json.sig`（旧钥）；
   - 用 `PLUGIN_INDEX_NEXT_SIGNING_KEY` 签 `index.json.sig.v2`（新钥）；
   - 同时生成 `index.json.sig.json` 封套，并用 `-envelope -merge` 把新钥签名合并进同一封套；
   - 新宿主优先用封套验签，`public_key` 中任一钥通过即可；旧宿主继续读取 `.sig`。
4. **切换主签名**：
   - 把 `index.json.sig` 改为新钥签名；
   - 新宿主 `public_key` 已包含新钥，继续可用；
   - 旧宿主会因 `.sig` 不再由旧钥签名而拒绝更新，需在公告的 EOL 前升级。
5. **移除旧钥**：确认旧宿主 EOL 后，从 `public_key` 和 CI secrets 中删除旧钥。
6. **确认泄露时**：不能双签旧钥，必须硬切新钥、发布紧急宿主版本、公告旧宿主停止接受新索引，并审计历史索引/资产。

## 4. 发布工作流配置

在受保护 GitHub Environment（建议名 `release`）中配置：

| 名称 | 类型 | 说明 |
|---|---|---|
| `PLUGIN_INDEX_SIGNING_KEY` | secret | 当前主签名私钥（base64 seed/private） |
| `PLUGIN_INDEX_NEXT_SIGNING_KEY` | secret（可选） | 过渡期下一把私钥 |
| `PLUGIN_INDEX_PUBLIC_KEY` | variable（可选） | 当前公钥，可用逗号分隔多个；未配置时使用仓库内置官方公钥 |
| `PLUGIN_INDEX_NEXT_PUBLIC_KEY` | variable（可选） | 下一把公钥；配置 `NEXT_SIGNING_KEY` 时必须提供 |

`cmd/signer verify -pub` 同样支持逗号分隔的多个公钥。

发布 job 会：

- 验证旧索引签名后才合并；
- 要求索引覆盖 `plugins.json` 全部插件（`-require-complete`）；
- 用 JSON Schema 校验索引结构，并要求 `revision` 严格递增；
- 拒绝未配置签名密钥的发布；
- 删除旧签名后再生成 `.sig` 与 `.sig.json`，两者都本地验签；
- 仅在完整索引通过校验后 prune 旧资产；
- 同名资产已存在但 SHA256 不同时拒绝覆盖，避免同一版本对应不同二进制。

## 5. 仓库内的 `index.json`

仓库根目录的 `index.json`/`index.json.sig` 不是权威索引。权威索引是 `plugin-index` Release 资产。保留仓库内副本时必须在 CI 中验签并与 `plugins.json` 对齐，否则应删除，避免用户误用过期索引。
