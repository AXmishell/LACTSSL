package dnsplugin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"lactssl/internal/plugin/exec"
	pluginproto "lactssl/internal/plugin/proto"
)

const (
	execProtocol      = "v1"
	execManifestLimit = 1 << 20 // 1 MB
)

var (
	idPattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	keyPattern    = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	localePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	fieldTypes    = map[string]bool{"": true, "text": true, "password": true, "textarea": true, "number": true, "select": true, "switch": true}
	maxProviders  = 512
	maxFields     = 64
	maxLocales    = 32
	maxAliases    = 64
)

// ExternalProvider 是由 exec helper 提供的服务商元数据。
type ExternalProvider struct {
	ID              string            `json:"id"`
	Name            string            `json:"name,omitempty"`
	NameI18n        map[string]string `json:"name_i18n,omitempty"`
	Description     string            `json:"description,omitempty"`
	DescriptionI18n map[string]string `json:"description_i18n,omitempty"`
	Aliases         []string          `json:"aliases,omitempty"`
	Fields          []ManifestField   `json:"fields,omitempty"`
	Helper          string            `json:"helper"`
	Version         string            `json:"version,omitempty"`
	// Capabilities 是该服务商声明的能力位，例如 verify-v1。
	Capabilities []string `json:"capabilities,omitempty"`
	// Enabled 表示该服务商已在管理界面启用并注册到运行时。
	Enabled bool `json:"enabled"`
	// FileEnabled 表示该服务商所属的 helper 文件未被管理员停用。
	// 文件停用时条目仍会以元数据形式展示，但不能启用。
	FileEnabled bool `json:"file_enabled"`
}

var externalState = struct {
	sync.RWMutex
	providers map[string]ExternalProvider
	helpers   int
	dir       string
}{
	providers: make(map[string]ExternalProvider),
}

// discoverMu 串行化重扫，避免两次扫描交错替换注册表。
var discoverMu sync.Mutex

// Discover 扫描插件目录并重建外部插件注册表。
// 目录不存在时视为未安装插件，不返回错误。
func Discover(cfg DiscoverConfig) DiscoverResult {
	discoverMu.Lock()
	defer discoverMu.Unlock()

	cfg = cfg.normalized()
	result := DiscoverResult{Dir: cfg.Dir}
	// 白名单默认空：插件服务商默认全部停用，需要管理员显式启用。
	enabledSet := make(map[string]bool, len(cfg.EnabledProviders))
	for _, id := range cfg.EnabledProviders {
		if id = normalize(id); id != "" {
			enabledSet[id] = true
		}
	}
	// 文件级停用：连 manifest 都不探测，避免执行管理员已隔离的 helper。
	disabledSet := make(map[string]bool, len(cfg.DisabledHelpers))
	for _, name := range cfg.DisabledHelpers {
		if name = strings.TrimSpace(name); name != "" {
			disabledSet[strings.ToLower(name)] = true
		}
	}
	externalState.Lock()
	externalState.providers = make(map[string]ExternalProvider)
	externalState.helpers = 0
	externalState.dir = cfg.Dir
	externalState.Unlock()

	if strings.TrimSpace(cfg.Dir) == "" {
		UnregisterSource(SourceExec)
		return result
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("读取插件目录失败: %v", err))
		}
		UnregisterSource(SourceExec)
		return result
	}

	// 先完整探测，再替换注册表，避免扫描中途失败留下半套插件。
	type candidate struct {
		path     string
		manifest Manifest
		digest   string
	}
	var candidates []candidate
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if disabledSet[strings.ToLower(name)] {
			result.DisabledFiles = append(result.DisabledFiles, name)
			continue
		}
		path := filepath.Join(cfg.Dir, name)
		// 使用 Lstat 拒绝符号链接，并统一要求组/其他用户不可写，
		// 避免插件目录被替换为指向目录外的文件。
		info, err := os.Lstat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if !info.Mode().IsRegular() {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 不是普通文件", name))
			continue
		}
		if info.Mode()&0o111 == 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 不是可执行文件", name))
			continue
		}
		if info.Mode()&0o022 != 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 插件文件允许组或其他用户写入，已拒绝加载", name))
			continue
		}
		// 仓库安装记录是本地完整性锚点：先计算摘要，命中记录后再执行
		// manifest，避免被替换的文件在校验前执行。
		if expected := strings.TrimSpace(cfg.ExpectedDigests[name]); expected != "" {
			digest, err := fileSHA256(path)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
				continue
			}
			if !strings.EqualFold(expected, digest) {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: 文件摘要与安装记录不一致，已拒绝加载", name))
				continue
			}
		}
		manifest, err := inspectHelper(path, cfg)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		digest, err := fileSHA256(path)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		if manifest.SHA256 != "" && !strings.EqualFold(manifest.SHA256, digest) {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: sha256 校验失败", name))
			continue
		}
		if err := validateManifest(manifest); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", name, err))
			continue
		}
		candidates = append(candidates, candidate{path: path, manifest: manifest, digest: digest})
	}

	UnregisterSource(SourceExec)
	for _, item := range candidates {
		accepted := 0
		for _, provider := range item.manifest.Providers {
			if existing, found := Lookup(provider.ID); found && existing.Source() != SourceExec {
				result.Skipped = append(result.Skipped, provider.ID)
				continue
			}
			enabled := enabledSet[provider.ID]
			if enabled {
				plugin := &externalPlugin{
					helper:   item.path,
					digest:   item.digest,
					provider: provider,
					version:  item.manifest.Version,
					settings: execSettings{
						memoryLimitMB: cfg.MemoryLimitMB,
						timeout:       cfg.Timeout,
						maxOutputKB:   cfg.MaxOutputKB,
					},
				}
				if err := RegisterExternal(plugin); err != nil {
					result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", filepath.Base(item.path), err))
					continue
				}
				result.Providers++
			} else {
				result.Disabled = append(result.Disabled, provider.ID)
			}
			externalState.Lock()
			externalState.providers[provider.ID] = ExternalProvider{
				ID:              provider.ID,
				Name:            provider.Name,
				NameI18n:        provider.NameI18n,
				Description:     provider.Description,
				DescriptionI18n: provider.DescriptionI18n,
				Aliases:         provider.Aliases,
				Fields:          provider.Fields,
				Capabilities:    provider.Capabilities,
				Helper:          item.path,
				Version:         item.manifest.Version,
				Enabled:         enabled,
				FileEnabled:     true,
			}
			externalState.Unlock()
			accepted++
		}
		// 只统计真正提供了服务商的 helper；被内置实现完全覆盖的不计。
		if accepted > 0 {
			result.Helpers++
		}
	}
	externalState.Lock()
	externalState.helpers = result.Helpers
	externalState.Unlock()
	sort.Strings(result.DisabledFiles)
	return result
}

// ExternalProviders 返回已加载的外部服务商，按 ID 排序。
func ExternalProviders() []ExternalProvider {
	externalState.RLock()
	defer externalState.RUnlock()
	out := make([]ExternalProvider, 0, len(externalState.providers))
	for _, provider := range externalState.providers {
		out = append(out, provider)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ExternalEnabledProviders 返回已启用并注册到运行时的外部服务商。
func ExternalEnabledProviders() []ExternalProvider {
	all := ExternalProviders()
	out := make([]ExternalProvider, 0, len(all))
	for _, provider := range all {
		if provider.Enabled {
			out = append(out, provider)
		}
	}
	return out
}

// ExternalProviderInfo 查找外部服务商元数据，支持别名。
func ExternalProviderInfo(id string) (ExternalProvider, bool) {
	key := normalize(id)
	externalState.RLock()
	defer externalState.RUnlock()
	if provider, ok := externalState.providers[key]; ok {
		return provider, true
	}
	for _, provider := range externalState.providers {
		for _, alias := range provider.Aliases {
			if normalize(alias) == key {
				return provider, true
			}
		}
	}
	return ExternalProvider{}, false
}

// ExternalHelpers 返回已加载的 helper 数量。
func ExternalHelpers() int {
	externalState.RLock()
	defer externalState.RUnlock()
	return externalState.helpers
}

func inspectHelper(path string, cfg DiscoverConfig) (Manifest, error) {
	// manifest 可能包含数百个服务商，不能复用普通调用的 max_output_kb（默认 64 KB），
	// 否则全量 helper 的清单会在截断后变成非法 JSON。上限与 execManifestLimit 对齐。
	manifestCfg := cfg
	manifestCfg.MaxOutputKB = execManifestLimit >> 10
	stdout, stderr, err := runHelper(path, "manifest", nil, cfg.ManifestTimeout, manifestCfg)
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest 探测失败: %v%s", err, stderrSuffix(stderr))
	}
	if len(stdout) > execManifestLimit {
		return Manifest{}, fmt.Errorf("manifest 输出超过 %d 字节", execManifestLimit)
	}
	var manifest Manifest
	if err := json.Unmarshal(stdout, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 manifest 失败: %v", err)
	}
	return manifest, nil
}

// InspectHelper 探测并校验单个 helper 文件，供插件上传接口在落盘前验证。
// 返回值中的 digest 是文件的实际 sha256，便于调用方记录与展示。
func InspectHelper(path string, cfg DiscoverConfig) (Manifest, string, error) {
	cfg = cfg.normalized()
	manifest, err := inspectHelper(path, cfg)
	if err != nil {
		return Manifest{}, "", err
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return Manifest{}, "", err
	}
	if manifest.SHA256 != "" && !strings.EqualFold(manifest.SHA256, digest) {
		return Manifest{}, "", fmt.Errorf("manifest 声明的 sha256 与文件不一致")
	}
	if err := validateManifest(manifest); err != nil {
		return Manifest{}, "", err
	}
	return manifest, digest, nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Protocol != execProtocol {
		return fmt.Errorf("不支持的插件协议 %q，需要 %q", manifest.Protocol, execProtocol)
	}
	if len(manifest.Providers) == 0 {
		return fmt.Errorf("manifest 未声明任何服务商")
	}
	if len(manifest.Providers) > maxProviders {
		return fmt.Errorf("manifest 服务商数量超过 %d", maxProviders)
	}
	seen := map[string]bool{}
	for _, provider := range manifest.Providers {
		if !idPattern.MatchString(provider.ID) {
			return fmt.Errorf("服务商 ID 非法: %q", provider.ID)
		}
		if seen[provider.ID] {
			return fmt.Errorf("服务商 ID 重复: %q", provider.ID)
		}
		seen[provider.ID] = true
		if len(provider.Aliases) > maxAliases {
			return fmt.Errorf("服务商 %s 的别名数量超过 %d", provider.ID, maxAliases)
		}
		for _, alias := range provider.Aliases {
			if !idPattern.MatchString(alias) {
				return fmt.Errorf("服务商 %s 的别名非法: %q", provider.ID, alias)
			}
			if alias == provider.ID {
				return fmt.Errorf("服务商 %s 的别名不能与 ID 相同", provider.ID)
			}
			// 同一 helper 内的别名也占用运行时注册表，必须与 ID/其他别名
			// 一起去重，否则发现阶段只会部分注册，文件却已经落盘。
			if seen[alias] {
				return fmt.Errorf("服务商 %s 的别名重复: %q", provider.ID, alias)
			}
			seen[alias] = true
		}
		if len(provider.Fields) > maxFields {
			return fmt.Errorf("服务商 %s 的字段数量超过 %d", provider.ID, maxFields)
		}
		if err := validateI18nMap(provider.ID, "名称", provider.NameI18n, 128); err != nil {
			return err
		}
		if err := validateI18nMap(provider.ID, "描述", provider.DescriptionI18n, 1024); err != nil {
			return err
		}
		for _, field := range provider.Fields {
			if !keyPattern.MatchString(field.Key) {
				return fmt.Errorf("服务商 %s 的字段 key 非法: %q", provider.ID, field.Key)
			}
			if !fieldTypes[field.Type] {
				return fmt.Errorf("服务商 %s 的字段 %s 类型非法: %q", provider.ID, field.Key, field.Type)
			}
			if len(field.Label) > 128 || len(field.Help) > 1024 || len(field.Placeholder) > 256 {
				return fmt.Errorf("服务商 %s 的字段 %s 文本过长", provider.ID, field.Key)
			}
			if err := validateI18nMap(provider.ID, "字段 "+field.Key+" 标签", field.LabelI18n, 128); err != nil {
				return err
			}
			if err := validateI18nMap(provider.ID, "字段 "+field.Key+" 占位符", field.PlaceholderI18n, 256); err != nil {
				return err
			}
			if err := validateI18nMap(provider.ID, "字段 "+field.Key+" 帮助", field.HelpI18n, 1024); err != nil {
				return err
			}
			if len(field.Options) > 64 {
				return fmt.Errorf("服务商 %s 的字段 %s 选项过多", provider.ID, field.Key)
			}
			for _, option := range field.Options {
				if len(option.Label) > 128 || len(option.Value) > 128 {
					return fmt.Errorf("服务商 %s 的字段 %s 选项文本过长", provider.ID, field.Key)
				}
				if err := validateI18nMap(provider.ID, "字段 "+field.Key+" 选项", option.LabelI18n, 128); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateI18nMap 限制 manifest 中多语言表的语言数量、语言标识与文本长度。
// 新字段是可选的，但恶意 helper 仍可能通过超大映射消耗宿主内存或数据库空间。
func validateI18nMap(providerID, what string, values map[string]string, maxLen int) error {
	if len(values) > maxLocales {
		return fmt.Errorf("服务商 %s 的%s语言数量超过 %d", providerID, what, maxLocales)
	}
	for locale, text := range values {
		if len(locale) > 32 || !localePattern.MatchString(locale) {
			return fmt.Errorf("服务商 %s 的%s语言标识非法: %q", providerID, what, locale)
		}
		if len(text) > maxLen {
			return fmt.Errorf("服务商 %s 的%s文本过长: %s", providerID, what, locale)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type execSettings struct {
	memoryLimitMB int
	timeout       time.Duration
	maxOutputKB   int
}

type externalPlugin struct {
	helper   string
	digest   string
	provider ManifestProvider
	version  string
	settings execSettings
}

func (p *externalPlugin) ID() string        { return p.provider.ID }
func (p *externalPlugin) Aliases() []string { return p.provider.Aliases }
func (p *externalPlugin) Source() Source    { return SourceExec }
func (p *externalPlugin) New(cfg map[string]string) (challenge.Provider, error) {
	return &execProvider{
		helper:   p.helper,
		digest:   p.digest,
		provider: p.provider.ID,
		cfg:      cloneConfig(cfg),
		settings: p.settings,
	}, nil
}

type execRequest struct {
	Action   string            `json:"action"`
	Provider string            `json:"provider"`
	Config   map[string]string `json:"config,omitempty"`
	Domain   string            `json:"domain,omitempty"`
	Token    string            `json:"token,omitempty"`
	KeyAuth  string            `json:"key_auth,omitempty"`
	Mode     string            `json:"mode,omitempty"`
	Locale   string            `json:"locale,omitempty"`
}

// ErrVerifyUnsupported 表示 helper 版本或该服务商未声明 verify-v1 能力。
var ErrVerifyUnsupported = errors.New("dns provider does not support verify")

// Verifier 是 DNS 插件可选的只读探测接口。
// mode 目前固定为 read；locale 用于让 helper 返回对应语言的检查消息。
type Verifier interface {
	Verify(ctx context.Context, cfg map[string]string, domain, mode, locale string) (pluginproto.VerifyResponse, error)
}

func hasCapability(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

// Verify 调用外部 helper 的 verify 子命令。
// mode=read 执行只读的认证与 zone 检查；mode=write 请求 helper 在单个进程内
// 完成临时 TXT 的写入与清理，避免 present/cleanup 两次进程启动与重复鉴权。
func (p *externalPlugin) Verify(ctx context.Context, cfg map[string]string, domain, mode, locale string) (pluginproto.VerifyResponse, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	feature := pluginproto.FeatureVerify
	if mode == "write" {
		feature = pluginproto.FeatureVerifyWrite
	}
	if !hasCapability(p.provider.Capabilities, feature) {
		return pluginproto.VerifyResponse{}, ErrVerifyUnsupported
	}
	if p.digest != "" {
		current, err := fileSHA256(p.helper)
		if err != nil {
			return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 无法读取 helper 文件: %v", p.provider.ID, err)
		}
		if current != p.digest {
			return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 的 helper 文件已变化，请重扫插件目录后再试", p.provider.ID)
		}
	}
	body, err := json.Marshal(execRequest{
		Action:   "verify",
		Provider: p.provider.ID,
		Config:   cloneConfig(cfg),
		Domain:   domain,
		Mode:     mode,
		Locale:   locale,
	})
	if err != nil {
		return pluginproto.VerifyResponse{}, fmt.Errorf("编码插件验证请求失败: %w", err)
	}
	timeout := p.settings.timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	runCtx := ctx
	if mode == "write" {
		// 写测试必须保证 Present 成功后一定执行 CleanUp，不能因为浏览器关闭
		// 或请求取消而在 DNS 上留下临时记录。与旧 present/cleanup 路径一致，
		// 这里脱离请求上下文，只受单次执行超时约束；启动前仍检查一次取消状态，
		// 避免已经放弃的请求再触发写入。
		if err := ctx.Err(); err != nil {
			return pluginproto.VerifyResponse{}, err
		}
		runCtx = context.Background()
		// 一个进程内完成写入和清理，超时预算按两次旧调用计算。
		timeout *= 2
		if timeout > 120*time.Second {
			timeout = 120 * time.Second
		}
	} else if timeout > 30*time.Second {
		timeout = 30 * time.Second
	}
	stdout, stderr, err := pluginexec.Run(runCtx, p.helper, []string{"verify"}, body, pluginexec.Options{
		Timeout:       timeout,
		MemoryLimitMB: p.settings.memoryLimitMB,
		MaxOutputKB:   p.settings.maxOutputKB,
		Env: []string{
			"LACTSSL_PLUGIN_PROTOCOL=" + execProtocol,
			"LACTSSL_DNS_PLUGIN_FEATURES=" + pluginproto.FeatureVerify + "," + pluginproto.FeatureVerifyWrite,
		},
	})
	if errors.Is(err, pluginexec.ErrTimeout) {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 验证超时（%s）", p.provider.ID, timeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 内存占用超过上限 %d MB，已强制结束", p.provider.ID, p.settings.memoryLimitMB)
	}
	if err != nil {
		message := pluginexec.Redact(stderr, cfg)
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 执行验证失败: %v%s", p.provider.ID, err, suffix(message))
	}
	output := strings.TrimSpace(stdout)
	if output == "" {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 未返回验证结果", p.provider.ID)
	}
	var result pluginproto.VerifyResponse
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return pluginproto.VerifyResponse{}, fmt.Errorf("插件 %s 返回了非法验证结果: %s", p.provider.ID, pluginexec.Redact(pluginexec.Limit(output, 512), cfg))
	}
	if result.Checks == nil {
		result.Checks = []pluginproto.VerifyCheck{}
	}
	return result, nil
}

type execProvider struct {
	helper   string
	digest   string
	provider string
	cfg      map[string]string
	settings execSettings
}

func (p *execProvider) Present(domain, token, keyAuth string) error {
	return p.call("present", domain, token, keyAuth)
}

func (p *execProvider) CleanUp(domain, token, keyAuth string) error {
	return p.call("cleanup", domain, token, keyAuth)
}

// Timeout 返回 DNS 传播等待时间与轮询间隔，与 lego 默认值一致。
func (p *execProvider) Timeout() (time.Duration, time.Duration) {
	return 180 * time.Second, 2 * time.Second
}

func (p *execProvider) call(action, domain, token, keyAuth string) error {
	// 扫描与执行之间文件可能被替换；DNS helper 与部署/通知插件一致，
	// 每次 present/cleanup 前重新校验摘要。
	if p.digest != "" {
		current, err := fileSHA256(p.helper)
		if err != nil {
			return fmt.Errorf("插件 %s 无法读取 helper 文件: %v", p.provider, err)
		}
		if current != p.digest {
			return fmt.Errorf("插件 %s 的 helper 文件已变化，请重扫插件目录后再试", p.provider)
		}
	}
	body, err := json.Marshal(execRequest{
		Action:   action,
		Provider: p.provider,
		Config:   p.cfg,
		Domain:   domain,
		Token:    token,
		KeyAuth:  keyAuth,
	})
	if err != nil {
		return fmt.Errorf("编码插件请求失败: %w", err)
	}
	stdout, stderr, err := runHelper(p.helper, action, body, p.settings.timeout, DiscoverConfig{
		MemoryLimitMB: p.settings.memoryLimitMB,
		MaxOutputKB:   p.settings.maxOutputKB,
	})
	if err != nil {
		message := pluginexec.Redact(stderr, p.cfg)
		return fmt.Errorf("插件 %s 执行 %s 失败: %v%s", p.provider, action, err, suffix(message))
	}
	if len(bytes.TrimSpace(stdout)) > 0 {
		// helper 的 stdout 只用于协议输出；present/cleanup 出现内容时记录到错误信息，
		// 避免把可能含密钥的输出直接吞掉。
		return fmt.Errorf("插件 %s 返回了非空输出: %s", p.provider, pluginexec.Redact(pluginexec.Limit(string(stdout), 512), p.cfg))
	}
	return nil
}

// runHelper 以一次性子进程执行 helper，并施加超时、输出上限与 RSS 内存上限。
// 不使用 RLIMIT_AS：Go 运行时会预留大量虚拟地址空间，按地址空间限制会直接启动失败。
func runHelper(path, action string, stdin []byte, timeout time.Duration, cfg DiscoverConfig) ([]byte, string, error) {
	cfg = cfg.normalized()
	stdout, stderr, err := pluginexec.Run(context.Background(), path, []string{action}, stdin, pluginexec.Options{
		Timeout:       timeout,
		MemoryLimitMB: cfg.MemoryLimitMB,
		MaxOutputKB:   cfg.MaxOutputKB,
		Env: []string{
			"LACTSSL_PLUGIN_PROTOCOL=" + execProtocol,
			"LACTSSL_DNS_PLUGIN_FEATURES=" + pluginproto.FeatureVerify + "," + pluginproto.FeatureVerifyWrite,
		},
	})
	if errors.Is(err, pluginexec.ErrTimeout) {
		err = fmt.Errorf("插件执行超时（%s）", timeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		err = fmt.Errorf("插件内存占用超过上限 %d MB，已强制结束", cfg.MemoryLimitMB)
	}
	return []byte(stdout), stderr, err
}

func stderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + pluginexec.Limit(stderr, 512)
}

func suffix(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	return ": " + message
}

func cloneConfig(cfg map[string]string) map[string]string {
	out := make(map[string]string, len(cfg))
	for key, value := range cfg {
		out[key] = value
	}
	return out
}
