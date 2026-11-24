# 独立 helper 与发布工具

`tools/helpers/` 保留独立 helper 与 signer 入口，用于兼容旧的插件包/索引发布流程：

- `dns-helper-v5`
- `deploy-helper`
- `notify-helper`
- `signer`

默认主程序已经内置官方实现，不依赖这些 helper。日常开发、测试和默认构建不需要运行它们；仅在需要生成独立插件包或签名索引时使用。

构建入口仍由 `internal/plugin/Makefile` 提供：

```bash
cd internal/plugin
make bundle
make bundle-lego-v5
make plugin KIND=dns ID=all-lego-v5 GOOS=linux GOARCH=amd64
```
