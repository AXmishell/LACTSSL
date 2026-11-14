// Command dns-helper 是外部 DNS 插件协议的参考实现。
//
// 它可以按构建标签编译成插件版或全量版：
//
//	make helper        # 插件版，支持全部 lego 服务商（dns_extra）
//	make helper-full   # 全量版，支持全部 lego 服务商（dns_all）
//
// 主程序通过一次性子进程调用它：
//
//	dns-helper manifest                 # 输出插件清单 JSON
//	dns-helper present                  # 从 stdin 读取 JSON 请求并添加 TXT 记录
//	dns-helper cleanup                  # 从 stdin 读取 JSON 请求并删除 TXT 记录
//
// 内存上限由主程序通过 LACTSSL_PLUGIN_MEMORY_LIMIT 传入，helper 启动时会
// 用 setrlimit 自我限制，即使主程序没有 prlimit 也能生效。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/go-acme/lego/v4/challenge"
	"lactssl/internal/dnsplugin"
	"lactssl/internal/i18n"
	"lactssl/internal/service"
)

type request struct {
	Action   string            `json:"action"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config"`
	Domain   string            `json:"domain"`
	Token    string            `json:"token"`
	KeyAuth  string            `json:"key_auth"`
}

func main() {
	applyMemoryLimit()
	if len(os.Args) < 2 {
		fail("用法: dns-helper manifest|present|cleanup")
	}
	switch os.Args[1] {
	case "manifest", "list":
		writeManifest()
	case "present", "cleanup":
		handle(os.Args[1])
	default:
		fail("未知子命令: " + os.Args[1])
	}
}

func writeManifest() {
	manifest := dnsplugin.Manifest{
		Protocol:  "v1",
		Name:      "lactssl dns helper",
		Version:   serviceVersion(),
		Providers: make([]dnsplugin.ManifestProvider, 0, len(dnsplugin.List())),
	}
	for _, plugin := range dnsplugin.ListBySource(dnsplugin.SourceBuiltin) {
		def, ok := service.DNSProviderInfo(plugin.ID())
		if !ok {
			continue
		}
		localized := def.Localized(i18n.Default)
		provider := dnsplugin.ManifestProvider{
			ID:          plugin.ID(),
			Name:        localized.Label,
			Description: localized.Desc,
			Aliases:     plugin.Aliases(),
			Fields:      manifestFields(localized.Fields),
		}
		manifest.Providers = append(manifest.Providers, provider)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		fail("输出 manifest 失败: " + err.Error())
	}
}

func manifestFields(fields []service.FieldDef) []dnsplugin.ManifestField {
	out := make([]dnsplugin.ManifestField, 0, len(fields))
	for _, field := range fields {
		item := dnsplugin.ManifestField{
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
			item.Options = append(item.Options, dnsplugin.ManifestOption{Label: option.Label, Value: option.Value})
		}
		if field.ShowIf != nil {
			item.ShowIf = &dnsplugin.ManifestCondition{Key: field.ShowIf.Key, Value: field.ShowIf.Value}
		}
		out = append(out, item)
	}
	return out
}

func handle(action string) {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fail("解析请求失败: " + err.Error())
	}
	if req.Provider == "" {
		fail("请求缺少 provider")
	}
	plugin, ok := dnsplugin.Lookup(req.Provider)
	if !ok || plugin.Source() != dnsplugin.SourceBuiltin {
		fail("不支持的内置服务商: " + req.Provider)
	}
	envs, ok := service.EnvForProvider(plugin.ID(), req.Config)
	if !ok {
		fail("服务商字段映射失败: " + req.Provider)
	}
	for key, value := range envs {
		if value == "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			fail("设置环境变量失败: " + err.Error())
		}
	}
	provider, err := plugin.New(req.Config)
	if err != nil {
		fail("初始化服务商失败: " + err.Error())
	}
	if err := run(provider, action, req); err != nil {
		fail(err.Error())
	}
}

func run(provider challenge.Provider, action string, req request) error {
	switch action {
	case "present":
		return provider.Present(req.Domain, req.Token, req.KeyAuth)
	case "cleanup":
		return provider.CleanUp(req.Domain, req.Token, req.KeyAuth)
	default:
		return fmt.Errorf("未知动作: %s", action)
	}
}

const legoModule = "github.com/go-acme/lego/v4"

func serviceVersion() string {
	// manifest 的 version 用于展示服务商实现自身的版本。内置服务商来自 lego，
	// 因此这里取 lego 的上游版本，而不是本项目的构建版本——后者来自
	// git describe，会变成 1.1.1-dirty 之类，与插件本身无关。
	// 不引入 api 包，避免把整个 HTTP 层带进 helper。
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path != legoModule {
				continue
			}
			if dep.Replace != nil && dep.Replace.Version != "" {
				return dep.Replace.Version
			}
			if dep.Version != "" {
				return dep.Version
			}
		}
	}
	return "dev"
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
