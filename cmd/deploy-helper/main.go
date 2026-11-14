// Command deploy-helper 是证书部署插件协议的参考实现。
//
// 主程序只内置本地文件与 SSH 部署目标；FTP/FTPS、Webhook、阿里云 CDN、
// 七牛云、宝塔、1Panel、雷池 WAF 等目标都由本 helper 通过插件协议提供：
//
//	make deploy-helper
//	cp bin/lactssl-deploy-helper data/plugins/deploy/deploy-helper
//	chmod 0755 data/plugins/deploy/deploy-helper
//
// 安装后需在配置中启用 deploy_plugins，并在「插件」页重扫插件目录。
//
// 主程序通过一次性子进程调用它：
//
//	deploy-helper manifest   # 输出插件清单 JSON
//	deploy-helper deploy     # 从 stdin 读取 JSON 请求并执行部署
//
// 注意：helper 进程没有主程序的 Manager/Store；本 helper 不提供 SSH 目标，
// SSH 仍由主程序内置实现负责。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"lactssl/internal/deployplugin"
	_ "lactssl/internal/deployplugin/providers/aliyuncdn"
	_ "lactssl/internal/deployplugin/providers/ftp"
	_ "lactssl/internal/deployplugin/providers/panels"
	_ "lactssl/internal/deployplugin/providers/qiniu"
	_ "lactssl/internal/deployplugin/providers/webhook"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

type request struct {
	Action      string                   `json:"action"`
	Protocol    string                   `json:"protocol"`
	Provider    string                   `json:"provider"`
	Auth        map[string]string        `json:"auth"`
	Config      map[string]string        `json:"config"`
	Certificate deployplugin.Certificate `json:"certificate"`
}

func main() {
	applyMemoryLimit()
	if len(os.Args) < 2 {
		fail("用法: deploy-helper manifest|deploy")
	}
	switch os.Args[1] {
	case "manifest", "list":
		writeManifest()
	case "deploy":
		handle()
	default:
		fail("未知子命令: " + os.Args[1])
	}
}

func writeManifest() {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(buildManifest()); err != nil {
		fail("输出 manifest 失败: " + err.Error())
	}
}

// buildManifest 按主程序声明的特性输出部署目标。
// 新版主程序只接收合并后的规范 ID；旧版主程序仍接收旧 ID，避免版本错配后
// 七牛云、宝塔或 1Panel 目标整体不可用。
func buildManifest() deployplugin.Manifest {
	merged := hasFeature(deployplugin.FeatureMergedDeployers)
	mergedCanonical := make(map[string]bool)
	if !merged {
		for _, canonical := range service.DeprecatedDeployTypeCanonical {
			mergedCanonical[canonical] = true
		}
	}

	plugins := deployplugin.ListBySource(deployplugin.SourceBuiltin)
	manifest := deployplugin.Manifest{
		Protocol:  "v1",
		Name:      "lactssl deploy helper",
		Version:   serviceVersion(),
		Deployers: make([]deployplugin.ManifestDeployer, 0, len(plugins)),
	}
	for _, plugin := range plugins {
		def, ok := service.PluginDeployTypeInfo(plugin.ID())
		if !ok {
			continue
		}
		if merged && def.Deprecated {
			continue
		}
		if !merged && mergedCanonical[plugin.ID()] {
			continue
		}
		localized := def.Localized(i18n.Default)
		manifest.Deployers = append(manifest.Deployers, deployplugin.ManifestDeployer{
			ID:           plugin.ID(),
			Name:         localized.Label,
			Description:  localized.Desc,
			Aliases:      plugin.Aliases(),
			Fields:       manifestFields(localized.Fields),
			DeployFields: manifestFields(localized.DeployFields),
		})
	}
	return manifest
}

func hasFeature(name string) bool {
	for _, item := range strings.Split(os.Getenv("LACTSSL_DEPLOY_PLUGIN_FEATURES"), ",") {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	return false
}

func manifestFields(fields []service.FieldDef) []deployplugin.ManifestField {
	out := make([]deployplugin.ManifestField, 0, len(fields))
	for _, field := range fields {
		item := deployplugin.ManifestField{
			Key:         field.Key,
			Label:       field.Label,
			Type:        field.Type,
			Required:    field.Required,
			Secret:      field.Secret,
			Placeholder: field.Placeholder,
			Help:        field.Help,
			Default:     field.Default,
		}
		for _, option := range field.Options {
			item.Options = append(item.Options, deployplugin.ManifestOption{Label: option.Label, Value: option.Value})
		}
		if field.ShowIf != nil {
			item.ShowIf = &deployplugin.ManifestCondition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out = append(out, item)
	}
	return out
}

func handle() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fail("解析请求失败: " + err.Error())
	}
	if req.Protocol != "" && req.Protocol != "v1" {
		fail("不支持的插件协议: " + req.Protocol)
	}
	if req.Provider == "" {
		fail("请求缺少 provider")
	}
	plugin, ok := deployplugin.Lookup(req.Provider)
	if !ok || plugin.Source() != deployplugin.SourceBuiltin {
		fail("不支持的部署目标: " + req.Provider)
	}
	if err := plugin.Deploy(context.Background(), deployplugin.Request{
		Auth:        req.Auth,
		Config:      req.Config,
		Certificate: req.Certificate,
	}); err != nil {
		fail(err.Error())
	}
}

func serviceVersion() string {
	// 不引入 api 包，避免把整个 HTTP 层带进 helper。
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

// applyMemoryLimit 根据主程序传入的预算设置 Go 堆软上限。
// 硬性的常驻内存限制由主程序轮询 RSS 并在超限时结束进程实现。
func applyMemoryLimit() {
	raw := os.Getenv("LACTSSL_PLUGIN_MEMORY_LIMIT")
	if raw == "" {
		return
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		return
	}
	debug.SetMemoryLimit(limit * 4 / 5)
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
