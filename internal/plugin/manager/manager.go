// Package manager 负责插件发现与运行时状态管理。
//
// host 包只保存注册表；本包组合 host、builtin 与 runtime 配置，持有发现结果和
// 并发锁。API 层只通过本包触发重扫和读取快照。
package manager

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"lactssl/internal/model"
	"lactssl/internal/plugin/builtin"
	deployplugin "lactssl/internal/plugin/host/deploy"
	dnsplugin "lactssl/internal/plugin/host/dns"
	notifyplugin "lactssl/internal/plugin/host/notify"
)

// 插件类别。
const (
	KindDNS    = "dns"
	KindDeploy = "deploy"
	KindNotify = "notify"
)

// 文件级停用 helper 的设置键。
const (
	SettingDNSDisabledHelpers    = "plugins.dns.disabled_helpers"
	SettingDeployDisabledHelpers = "plugins.deploy.disabled_helpers"
	SettingNotifyDisabledHelpers = "plugins.notify.disabled_helpers"
)

// DisabledHelpersSetting 返回某类插件的文件级停用设置键。
func DisabledHelpersSetting(kind string) string {
	switch kind {
	case KindDNS:
		return SettingDNSDisabledHelpers
	case KindDeploy:
		return SettingDeployDisabledHelpers
	case KindNotify:
		return SettingNotifyDisabledHelpers
	default:
		return ""
	}
}

// Store 是插件发现需要的最小存储接口。
type Store interface {
	ListPluginInstallations() ([]model.PluginInstallation, error)
	ListPluginFileMeta(kind string) ([]model.PluginFileMeta, error)
	GetSetting(key string) (string, error)
}

// KindOptions 是单类插件的发现与执行配置。
type KindOptions struct {
	Enabled       bool
	Dir           string
	Sandbox       string
	MemoryLimitMB int
	Timeout       time.Duration
	MaxOutputKB   int
}

// Options 是插件管理器配置。
type Options struct {
	BuiltinEnabled bool
	DNS            KindOptions
	Deploy         KindOptions
	Notify         KindOptions

	// EnabledItems 返回某类插件当前启用的条目 ID。
	EnabledItems func(kind string) []string
	// KnownIDs 返回某类插件额外需要识别的内置 ID（部署/通知使用）。
	KnownIDs func(kind string) map[string]bool
	// SyncFileMeta / SyncMetadata 在发现成功后刷新文件与条目元数据。
	SyncFileMeta func(kind string)
	SyncMetadata func(kind string)
	// AfterDeployDiscover 在部署插件发现完成后触发幂等迁移。
	AfterDeployDiscover func()
}

// Manager 持有三类插件的发现结果与发现锁。
type Manager struct {
	opts  Options
	store Store

	discoverMu sync.Mutex

	mu           sync.RWMutex
	dnsResult    dnsplugin.DiscoverResult
	deployResult deployplugin.DiscoverResult
	notifyResult notifyplugin.DiscoverResult
}

// New 创建插件管理器。
func New(opts Options, store Store) *Manager {
	return &Manager{opts: opts, store: store}
}

// Configure 更新运行期配置。
// Server 的插件开关在构造后仍可能被测试或嵌入式调用修改，因此每次发现前
// 都重新同步一次；discoverMu 保证配置更新不会与发现过程交错。
func (m *Manager) Configure(opts Options) {
	m.discoverMu.Lock()
	m.opts = opts
	m.discoverMu.Unlock()
}

// DiscoverDNS 重新扫描 DNS 插件。
func (m *Manager) DiscoverDNS() dnsplugin.DiscoverResult {
	m.discoverMu.Lock()
	defer m.discoverMu.Unlock()

	dnsplugin.UnregisterSource(dnsplugin.SourceExec)
	dnsBuiltin := m.opts.BuiltinEnabled && m.opts.DNS.Enabled
	var syncErrors []string
	if err := builtin.SyncDNS(m.opts.BuiltinEnabled, dnsBuiltin, builtin.IDSet(m.enabledItems(KindDNS))); err != nil {
		syncErrors = append(syncErrors, "内置 DNS 插件注册失败: "+err.Error())
	}
	cfg := dnsplugin.DiscoverConfig{
		UploadSandboxProfile: m.opts.DNS.Sandbox,
		MemoryLimitMB:        m.opts.DNS.MemoryLimitMB,
		Timeout:              m.opts.DNS.Timeout,
		MaxOutputKB:          m.opts.DNS.MaxOutputKB,
		ExpectedDigests:      m.expectedPluginDigests(KindDNS),
	}
	if m.opts.DNS.Enabled {
		cfg.Dir = m.opts.DNS.Dir
		cfg.EnabledProviders = m.enabledItems(KindDNS)
		cfg.DisabledHelpers = m.DisabledHelpers(KindDNS)
	}
	result := dnsplugin.Discover(cfg)
	result.Errors = append(syncErrors, result.Errors...)
	m.mu.Lock()
	m.dnsResult = result
	m.mu.Unlock()
	m.afterDiscover(KindDNS)
	return result
}

// DiscoverDeploy 重新扫描部署插件。
func (m *Manager) DiscoverDeploy() deployplugin.DiscoverResult {
	m.discoverMu.Lock()
	defer m.discoverMu.Unlock()

	deployplugin.UnregisterSource(deployplugin.SourceExec)
	deployBuiltin := m.opts.BuiltinEnabled && m.opts.Deploy.Enabled
	var syncErrors []string
	if err := builtin.SyncDeploy(m.opts.BuiltinEnabled, deployBuiltin, builtin.IDSet(m.enabledItems(KindDeploy))); err != nil {
		syncErrors = append(syncErrors, "内置部署插件注册失败: "+err.Error())
	}
	knownIDs := m.knownIDs(KindDeploy)
	for id := range deployplugin.BuiltinDeployerIDs() {
		knownIDs[id] = true
	}
	cfg := deployplugin.DiscoverConfig{
		UploadSandboxProfile: m.opts.Deploy.Sandbox,
		MemoryLimitMB:        m.opts.Deploy.MemoryLimitMB,
		Timeout:              m.opts.Deploy.Timeout,
		MaxOutputKB:          m.opts.Deploy.MaxOutputKB,
		KnownIDs:             knownIDs,
		ExpectedDigests:      m.expectedPluginDigests(KindDeploy),
	}
	if m.opts.Deploy.Enabled {
		cfg.Dir = m.opts.Deploy.Dir
		cfg.EnabledDeployers = m.enabledItems(KindDeploy)
		cfg.DisabledHelpers = m.DisabledHelpers(KindDeploy)
	}
	result := deployplugin.Discover(cfg)
	result.Errors = append(syncErrors, result.Errors...)
	m.mu.Lock()
	m.deployResult = result
	m.mu.Unlock()
	m.afterDiscover(KindDeploy)
	if m.opts.AfterDeployDiscover != nil {
		m.opts.AfterDeployDiscover()
	}
	return result
}

// DiscoverNotify 重新扫描通知插件。
func (m *Manager) DiscoverNotify() notifyplugin.DiscoverResult {
	m.discoverMu.Lock()
	defer m.discoverMu.Unlock()

	notifyplugin.UnregisterSource(notifyplugin.SourceExec)
	notifyBuiltin := m.opts.BuiltinEnabled && m.opts.Notify.Enabled
	var syncErrors []string
	if err := builtin.SyncNotify(m.opts.BuiltinEnabled, notifyBuiltin, builtin.IDSet(m.enabledItems(KindNotify))); err != nil {
		syncErrors = append(syncErrors, "内置通知插件注册失败: "+err.Error())
	}
	cfg := notifyplugin.DiscoverConfig{
		UploadSandboxProfile: m.opts.Notify.Sandbox,
		MemoryLimitMB:        m.opts.Notify.MemoryLimitMB,
		Timeout:              m.opts.Notify.Timeout,
		MaxOutputKB:          m.opts.Notify.MaxOutputKB,
		KnownIDs:             m.knownIDs(KindNotify),
		ExpectedDigests:      m.expectedPluginDigests(KindNotify),
	}
	if m.opts.Notify.Enabled {
		cfg.Dir = m.opts.Notify.Dir
		cfg.EnabledChannels = m.enabledItems(KindNotify)
		cfg.DisabledHelpers = m.DisabledHelpers(KindNotify)
	}
	result := notifyplugin.Discover(cfg)
	result.Errors = append(syncErrors, result.Errors...)
	m.mu.Lock()
	m.notifyResult = result
	m.mu.Unlock()
	m.afterDiscover(KindNotify)
	return result
}

// DNSResult 返回最近一次 DNS 发现结果。
func (m *Manager) DNSResult() dnsplugin.DiscoverResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneDNSResult(m.dnsResult)
}

// DeployResult 返回最近一次部署发现结果。
func (m *Manager) DeployResult() deployplugin.DiscoverResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneDeployResult(m.deployResult)
}

// NotifyResult 返回最近一次通知发现结果。
func (m *Manager) NotifyResult() notifyplugin.DiscoverResult {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneNotifyResult(m.notifyResult)
}

// DisabledHelpers 返回某类插件被停用的 helper 文件名，供 Discover 跳过探测。
func (m *Manager) DisabledHelpers(kind string) []string {
	key := DisabledHelpersSetting(kind)
	if key == "" || m.store == nil {
		return nil
	}
	raw, err := m.store.GetSetting(key)
	if err != nil || strings.TrimSpace(raw) == "" {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *Manager) afterDiscover(kind string) {
	if m.opts.SyncFileMeta != nil {
		m.opts.SyncFileMeta(kind)
	}
	if m.opts.SyncMetadata != nil {
		m.opts.SyncMetadata(kind)
	}
}

func (m *Manager) enabledItems(kind string) []string {
	if m.opts.EnabledItems == nil {
		return nil
	}
	return m.opts.EnabledItems(kind)
}

func (m *Manager) knownIDs(kind string) map[string]bool {
	out := make(map[string]bool)
	if m.opts.KnownIDs != nil {
		for id := range m.opts.KnownIDs(kind) {
			out[id] = true
		}
	}
	return out
}

// expectedPluginDigests 返回 helper 文件名到可执行文件摘要的映射。
// 安装记录优先；上传文件没有安装记录时回退到 plugin_file_meta 的摘要。
func (m *Manager) expectedPluginDigests(kind string) map[string]string {
	out := make(map[string]string)
	if m.store == nil {
		return out
	}
	list, err := m.store.ListPluginInstallations()
	if err != nil {
		return out
	}
	for _, record := range list {
		if record.Kind == kind && strings.TrimSpace(record.Filename) != "" && strings.TrimSpace(record.SHA256) != "" {
			out[record.Filename] = record.SHA256
		}
	}
	if metas, err := m.store.ListPluginFileMeta(kind); err == nil {
		for _, meta := range metas {
			filename := strings.TrimSpace(meta.Filename)
			digest := strings.TrimSpace(meta.SHA256)
			if filename == "" || digest == "" {
				continue
			}
			if _, exists := out[filename]; !exists {
				out[filename] = digest
			}
		}
	}
	return out
}

func cloneStrings(in []string) []string {
	return append([]string(nil), in...)
}

func cloneDNSResult(in dnsplugin.DiscoverResult) dnsplugin.DiscoverResult {
	out := in
	out.Errors = cloneStrings(in.Errors)
	out.Skipped = cloneStrings(in.Skipped)
	out.Disabled = cloneStrings(in.Disabled)
	out.DisabledFiles = cloneStrings(in.DisabledFiles)
	return out
}

func cloneDeployResult(in deployplugin.DiscoverResult) deployplugin.DiscoverResult {
	out := in
	out.Errors = cloneStrings(in.Errors)
	out.Skipped = cloneStrings(in.Skipped)
	out.Disabled = cloneStrings(in.Disabled)
	out.DisabledFiles = cloneStrings(in.DisabledFiles)
	return out
}

func cloneNotifyResult(in notifyplugin.DiscoverResult) notifyplugin.DiscoverResult {
	out := in
	out.Errors = cloneStrings(in.Errors)
	out.Skipped = cloneStrings(in.Skipped)
	out.Disabled = cloneStrings(in.Disabled)
	out.DisabledFiles = cloneStrings(in.DisabledFiles)
	return out
}
