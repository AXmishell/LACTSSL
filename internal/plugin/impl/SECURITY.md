# 安全策略

## 报告漏洞

请通过 GitHub 的私有漏洞报告功能联系维护者，不要公开提交 issue。  
报告中请包含：

- 受影响的插件或组件（DNS / deploy / notify / SDK / 构建链）；
- 复现步骤和影响范围；
- 是否涉及凭据泄露、SSRF、证书私钥、签名索引或供应链；
- 建议的修复方式（可选）。

## 处理原则

- 确认为安全问题后，维护者会尽快给出修复计划；
- 修复发布前不在公开渠道披露细节；
- 涉及索引签名、发布密钥或供应链的问题优先处理。

## 贡献者注意

- 不要在 PR、测试 fixture、日志中提交真实 token、私钥或账号信息；
- 用户可控 URL 必须使用 `outbound` 的 SSRF 防护；
- 错误信息不得包含 secret；
- 集成测试凭据只放在受保护的 GitHub Environment 中。

## 签名与 CI

- 索引签名私钥只能放在受保护 GitHub Environment、HSM/KMS 或离线签名机；不得进入仓库、普通家目录或未经审查的 `go run` 环境。
- 签名必须使用独立的 `cmd/signer`，该工具不得导入插件包；签名后必须立即验签。
- PR 工作流不得接触仓库 secrets 或写 token；生成物校验只读，bot 回写通过受信任的 post-merge 流程创建 PR。
- 发布 tag 应受 ruleset 保护，发布 job 应使用 required reviewers，并校验 tag 提交位于 `main`。
- 旧索引在合并前必须用固定公钥验签；索引缺少插件、artifact 缺失或签名失败时必须 fail-closed。
