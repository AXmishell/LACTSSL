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
	"lactssl/internal/pluginexec"
)

const (
	execProtocol      = "v1"
	execManifestLimit = 1 << 20 // 1 MB
)

var (
	idPattern    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	keyPattern   = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	fieldTypes   = map[string]bool{"": true, "text": true, "password": true, "textarea": true, "number": true, "select": true, "switch": true}
	maxProviders = 512
	maxFields    = 64
)

// ExternalProvider 是由 exec helper 提供的服务商元数据。
type ExternalProvider struct {
	ID          string          `json:"id"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Aliases     []string        `json:"aliases,omitempty"`
	Fields      []ManifestField `json:"fields,omitempty"`
	Helper      string          `json:"helper"`
	Version     string          `json:"version,omitempty"`
	// Enabled 表示该服务商已在管理界面启用并注册到运行时。
	Enabled bool `json:"enabled"`
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
		path := filepath.Join(cfg.Dir, name)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 == 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 不是可执行文件", name))
			continue
		}
		if info.Mode()&0o002 != 0 {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: 插件文件允许任意用户写入，已拒绝加载", name))
			continue
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
				ID:          provider.ID,
				Name:        provider.Name,
				Description: provider.Description,
				Aliases:     provider.Aliases,
				Fields:      provider.Fields,
				Helper:      item.path,
				Version:     item.manifest.Version,
				Enabled:     enabled,
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

// ExternalProviderInfo 查找外部服务商元数据。
func ExternalProviderInfo(id string) (ExternalProvider, bool) {
	externalState.RLock()
	defer externalState.RUnlock()
	provider, ok := externalState.providers[normalize(id)]
	return provider, ok
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
		for _, alias := range provider.Aliases {
			if !idPattern.MatchString(alias) {
				return fmt.Errorf("服务商 %s 的别名非法: %q", provider.ID, alias)
			}
		}
		if len(provider.Fields) > maxFields {
			return fmt.Errorf("服务商 %s 的字段数量超过 %d", provider.ID, maxFields)
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
			if len(field.Options) > 64 {
				return fmt.Errorf("服务商 %s 的字段 %s 选项过多", provider.ID, field.Key)
			}
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
}

type execProvider struct {
	helper   string
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
		Env:           []string{"LACTSSL_PLUGIN_PROTOCOL=" + execProtocol},
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
