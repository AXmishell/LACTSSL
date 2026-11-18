package notifyplugin

import (
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

	"lactssl/internal/plugin/exec"
)

const (
	execProtocol      = "v1"
	execManifestLimit = 1 << 20 // 1 MB
	// manifestOutputKB 让 manifest 输出上限与 execManifestLimit 对齐；
	// 普通 send 调用仍使用配置的 max_output_kb。
	manifestOutputKB = 1024
)

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	keyPattern  = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	fieldTypes  = map[string]bool{"": true, "text": true, "password": true, "textarea": true, "number": true, "select": true, "switch": true}
	maxChannels = 512
	maxFields   = 64
	maxOptions  = 64
)

// ExternalChannel 是由 exec helper 提供的通知渠道元数据。
type ExternalChannel struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Fields      []Field  `json:"fields,omitempty"`
	Helper      string   `json:"helper"`
	Version     string   `json:"version,omitempty"`
	// Enabled 表示该渠道已在管理界面启用并注册到运行时。
	Enabled bool `json:"enabled"`
	// FileEnabled 表示该渠道所属的 helper 文件未被管理员停用。
	FileEnabled bool `json:"file_enabled"`
}

var externalState = struct {
	sync.RWMutex
	channels map[string]ExternalChannel
	helpers  int
}{
	channels: make(map[string]ExternalChannel),
}

// discoverMu 串行化重扫，避免两次扫描交错替换注册表。
var discoverMu sync.Mutex

// Discover 扫描插件目录并重建外部通知渠道注册表。
// 目录不存在时视为未安装插件，不返回错误。
func Discover(cfg DiscoverConfig) DiscoverResult {
	discoverMu.Lock()
	defer discoverMu.Unlock()

	cfg = cfg.normalized()
	result := DiscoverResult{Dir: cfg.Dir}
	enabledSet := make(map[string]bool, len(cfg.EnabledChannels))
	for _, id := range cfg.EnabledChannels {
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
	externalState.channels = make(map[string]ExternalChannel)
	externalState.helpers = 0
	externalState.Unlock()

	if strings.TrimSpace(cfg.Dir) == "" {
		replaceExternal(nil)
		return result
	}
	entries, err := os.ReadDir(cfg.Dir)
	if err != nil {
		if !os.IsNotExist(err) {
			result.Errors = append(result.Errors, fmt.Sprintf("读取通知插件目录失败: %v", err))
		}
		replaceExternal(nil)
		return result
	}

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
		// Lstat 拒绝符号链接；组或其他用户可写的文件一律不加载。
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

	var channels []Notifier
	helpers := make(map[string]bool)
	for _, item := range candidates {
		accepted := 0
		for _, channel := range item.manifest.Channels {
			if cfg.isReserved(channel) {
				result.Skipped = append(result.Skipped, channel.ID)
				continue
			}
			enabled := enabledSet[normalize(channel.ID)]
			if enabled {
				channels = append(channels, &externalNotifier{
					helper:  item.path,
					digest:  item.digest,
					channel: channel,
					version: item.manifest.Version,
					settings: execSettings{
						memoryLimitMB: cfg.MemoryLimitMB,
						timeout:       cfg.Timeout,
						maxOutputKB:   cfg.MaxOutputKB,
					},
				})
				result.Channels++
			} else {
				result.Disabled = append(result.Disabled, channel.ID)
			}
			externalState.Lock()
			externalState.channels[normalize(channel.ID)] = ExternalChannel{
				ID:          channel.ID,
				Name:        channel.Name,
				Description: channel.Description,
				Aliases:     channel.Aliases,
				Fields:      channel.Fields,
				Helper:      item.path,
				Version:     item.manifest.Version,
				Enabled:     enabled,
				FileEnabled: true,
			}
			externalState.Unlock()
			accepted++
		}
		if accepted > 0 {
			helpers[item.path] = true
		}
	}

	_, errs := replaceExternal(channels)
	for _, err := range errs {
		result.Errors = append(result.Errors, err.Error())
	}
	result.Helpers = len(helpers)
	externalState.Lock()
	externalState.helpers = result.Helpers
	externalState.Unlock()
	sort.Strings(result.Skipped)
	sort.Strings(result.Disabled)
	sort.Strings(result.DisabledFiles)
	return result
}

// ExternalChannels 返回已探测到的全部外部通知渠道，按 ID 排序。
// 包含被管理员停用的渠道：它们保留元数据，但不注册到运行时。
func ExternalChannels() []ExternalChannel {
	registered := make(map[string]ExternalChannel)
	for _, plugin := range ListBySource(SourceExec) {
		if provider, ok := plugin.(interface{ Info() ExternalChannel }); ok {
			info := provider.Info()
			info.Enabled = true
			registered[normalize(info.ID)] = info
		}
	}
	externalState.RLock()
	defer externalState.RUnlock()
	out := make([]ExternalChannel, 0, len(externalState.channels)+len(registered))
	seen := make(map[string]bool, len(externalState.channels)+len(registered))
	for id, channel := range externalState.channels {
		seen[id] = true
		if info, ok := registered[id]; ok {
			channel.Enabled = true
			channel = info
		}
		out = append(out, channel)
	}
	for id, channel := range registered {
		if !seen[id] {
			out = append(out, channel)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ExternalEnabledChannels 返回已启用并注册到运行时的外部通知渠道。
func ExternalEnabledChannels() []ExternalChannel {
	all := ExternalChannels()
	out := all[:0]
	for _, channel := range all {
		if channel.Enabled {
			out = append(out, channel)
		}
	}
	return out
}

// ExternalChannelInfo 查找外部通知渠道元数据，支持别名。
// 停用的渠道同样可以查到，便于界面切换与历史配置展示。
func ExternalChannelInfo(id string) (ExternalChannel, bool) {
	key := normalize(id)
	externalState.RLock()
	channel, found := externalState.channels[key]
	if !found {
		// 停用渠道未注册到运行时，别名也要从扫描元数据中解析。
		for _, item := range externalState.channels {
			for _, alias := range item.Aliases {
				if normalize(alias) == key {
					channel, found = item, true
					break
				}
			}
			if found {
				break
			}
		}
	}
	externalState.RUnlock()
	if found {
		return channel, true
	}
	plugin, ok := Lookup(id)
	if !ok || plugin.Source() != SourceExec {
		return ExternalChannel{}, false
	}
	if provider, ok := plugin.(interface{ Info() ExternalChannel }); ok {
		info := provider.Info()
		info.Enabled = true
		return info, true
	}
	return ExternalChannel{}, false
}

// ExternalHelpers 返回已加载的通知 helper 数量。
func ExternalHelpers() int {
	externalState.RLock()
	defer externalState.RUnlock()
	return externalState.helpers
}

func inspectHelper(path string, cfg DiscoverConfig) (Manifest, error) {
	stdout, stderr, err := pluginexec.Run(context.Background(), path, []string{"manifest"}, nil, pluginexec.Options{
		Timeout:       cfg.ManifestTimeout,
		MemoryLimitMB: cfg.MemoryLimitMB,
		MaxOutputKB:   manifestOutputKB,
		Env:           []string{"LACTSSL_NOTIFY_PLUGIN_PROTOCOL=" + execProtocol},
	})
	if errors.Is(err, pluginexec.ErrTimeout) {
		return Manifest{}, fmt.Errorf("manifest 探测超时（%s）", cfg.ManifestTimeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return Manifest{}, fmt.Errorf("manifest 探测内存占用超过上限 %d MB", cfg.MemoryLimitMB)
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("manifest 探测失败: %v%s", err, stderrSuffix(stderr))
	}
	if len(stdout) > execManifestLimit {
		return Manifest{}, fmt.Errorf("manifest 输出超过 %d 字节", execManifestLimit)
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		return Manifest{}, fmt.Errorf("解析 manifest 失败: %v", err)
	}
	return manifest, nil
}

// InspectHelper 探测并校验单个 helper 文件，供插件上传接口在落盘前验证。
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
	if len(manifest.Channels) == 0 {
		return fmt.Errorf("manifest 未声明任何通知渠道")
	}
	if len(manifest.Channels) > maxChannels {
		return fmt.Errorf("manifest 通知渠道数量超过 %d", maxChannels)
	}
	if len(manifest.Name) > 128 || len(manifest.Version) > 64 {
		return fmt.Errorf("manifest 名称或版本号过长")
	}
	seen := map[string]bool{}
	for _, channel := range manifest.Channels {
		if !idPattern.MatchString(channel.ID) {
			return fmt.Errorf("通知渠道 ID 非法: %q", channel.ID)
		}
		if len(channel.Name) > 128 || len(channel.Description) > 1024 {
			return fmt.Errorf("通知渠道 %s 名称或描述过长", channel.ID)
		}
		if len(channel.Aliases) > 64 {
			return fmt.Errorf("通知渠道 %s 的别名数量超过 64", channel.ID)
		}
		if seen[channel.ID] {
			return fmt.Errorf("通知渠道 ID 重复: %q", channel.ID)
		}
		seen[channel.ID] = true
		for _, alias := range channel.Aliases {
			if !idPattern.MatchString(alias) {
				return fmt.Errorf("通知渠道 %s 的别名非法: %q", channel.ID, alias)
			}
			if alias == channel.ID {
				return fmt.Errorf("通知渠道 %s 的别名不能与 ID 相同", channel.ID)
			}
			if seen[alias] {
				return fmt.Errorf("通知渠道 %s 的别名重复: %q", channel.ID, alias)
			}
			seen[alias] = true
		}
		if err := validateFields(channel.ID, channel.Fields); err != nil {
			return err
		}
	}
	return nil
}

func validateFields(channelID string, fields []Field) error {
	if len(fields) > maxFields {
		return fmt.Errorf("通知渠道 %s 的字段数量超过 %d", channelID, maxFields)
	}
	keys := map[string]bool{}
	for _, field := range fields {
		if !keyPattern.MatchString(field.Key) {
			return fmt.Errorf("通知渠道 %s 的字段 key 非法: %q", channelID, field.Key)
		}
		if keys[field.Key] {
			return fmt.Errorf("通知渠道 %s 的字段 key 重复: %q", channelID, field.Key)
		}
		keys[field.Key] = true
		if !fieldTypes[field.Type] {
			return fmt.Errorf("通知渠道 %s 的字段 %s 类型非法: %q", channelID, field.Key, field.Type)
		}
		if len(field.Label) > 128 || len(field.LabelKey) > 128 || len(field.Help) > 1024 || len(field.Placeholder) > 256 {
			return fmt.Errorf("通知渠道 %s 的字段 %s 文本过长", channelID, field.Key)
		}
		if len(field.Options) > maxOptions {
			return fmt.Errorf("通知渠道 %s 的字段 %s 选项过多", channelID, field.Key)
		}
		for _, option := range field.Options {
			if len(option.Label) > 128 || len(option.LabelKey) > 128 || len(option.Value) > 128 {
				return fmt.Errorf("通知渠道 %s 的字段 %s 选项文本过长", channelID, field.Key)
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

type externalNotifier struct {
	helper   string
	digest   string
	channel  ManifestChannel
	version  string
	settings execSettings
}

func (n *externalNotifier) Definition() Definition {
	return Definition{
		ID:          n.channel.ID,
		Name:        n.channel.Name,
		Description: n.channel.Description,
		Aliases:     n.channel.Aliases,
		Fields:      n.channel.Fields,
	}
}

func (n *externalNotifier) Source() Source { return SourceExec }

// Info 返回插件元数据，供注册表在未经 Discover 时也能解析字段定义。
func (n *externalNotifier) Info() ExternalChannel {
	return ExternalChannel{
		ID:          n.channel.ID,
		Name:        n.channel.Name,
		Description: n.channel.Description,
		Aliases:     n.channel.Aliases,
		Fields:      n.channel.Fields,
		Helper:      n.helper,
		Version:     n.version,
		Enabled:     true,
	}
}

type execRequest struct {
	Action   string         `json:"action"`
	Protocol string         `json:"protocol"`
	Channel  string         `json:"channel"`
	Config   map[string]any `json:"config,omitempty"`
	Title    string         `json:"title"`
	Body     string         `json:"body"`
}

type execResult struct {
	OK      *bool  `json:"ok,omitempty"`
	Message string `json:"message,omitempty"`
}

func (n *externalNotifier) Notify(cfg map[string]any, msg Message) error {
	// 扫描与执行之间文件可能被替换。每次发送前重新校验摘要，
	// 避免管理员批准的是旧 helper、实际执行的却是新文件。
	if n.digest != "" {
		current, err := fileSHA256(n.helper)
		if err != nil {
			return fmt.Errorf("插件 %s 无法读取 helper 文件: %v", n.channel.ID, err)
		}
		if current != n.digest {
			return fmt.Errorf("插件 %s 的 helper 文件已变化，请重扫插件目录后再试", n.channel.ID)
		}
	}

	body, err := json.Marshal(execRequest{
		Action:   "send",
		Protocol: execProtocol,
		Channel:  n.channel.ID,
		Config:   cloneConfig(cfg),
		Title:    msg.Title,
		Body:     msg.Body,
	})
	if err != nil {
		return fmt.Errorf("编码插件请求失败: %w", err)
	}

	stdout, stderr, err := pluginexec.Run(context.Background(), n.helper, []string{"send"}, body, pluginexec.Options{
		Timeout:       n.settings.timeout,
		MemoryLimitMB: n.settings.memoryLimitMB,
		MaxOutputKB:   n.settings.maxOutputKB,
		Env:           []string{"LACTSSL_NOTIFY_PLUGIN_PROTOCOL=" + execProtocol},
	})
	secrets := redactionSecrets(cfg)
	if errors.Is(err, pluginexec.ErrTimeout) {
		return fmt.Errorf("插件 %s 发送超时（%s）", n.channel.ID, n.settings.timeout)
	}
	var memoryErr *pluginexec.MemoryLimitError
	if errors.As(err, &memoryErr) {
		return fmt.Errorf("插件 %s 内存占用超过上限 %d MB，已强制结束", n.channel.ID, n.settings.memoryLimitMB)
	}
	if err != nil {
		message := pluginexec.Redact(stderr, secrets)
		return fmt.Errorf("插件 %s 发送通知失败: %v%s", n.channel.ID, err, suffix(message))
	}

	output := strings.TrimSpace(stdout)
	if output == "" {
		return nil
	}
	var result execResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return fmt.Errorf("插件 %s 返回了非法结果: %s", n.channel.ID, pluginexec.Redact(pluginexec.Limit(output, 512), secrets))
	}
	if result.OK != nil && !*result.OK {
		message := strings.TrimSpace(result.Message)
		if message == "" {
			message = "插件未提供错误信息"
		}
		return fmt.Errorf("插件 %s 发送通知失败: %s", n.channel.ID, pluginexec.Redact(pluginexec.Limit(message, 512), secrets))
	}
	return nil
}

// redactionSecrets 收集可能出现在 helper 输出中的敏感值。
func redactionSecrets(cfg map[string]any) map[string]string {
	out := make(map[string]string, len(cfg)+1)
	for key, value := range cfg {
		if text, ok := value.(string); ok && len(text) >= 8 {
			out["config."+key] = text
		}
	}
	return out
}

func cloneConfig(cfg map[string]any) map[string]any {
	if len(cfg) == 0 {
		return nil
	}
	out := make(map[string]any, len(cfg))
	for key, value := range cfg {
		out[key] = value
	}
	return out
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
