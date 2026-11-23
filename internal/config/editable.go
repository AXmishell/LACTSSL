package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrConfigConflict 表示配置文件在读取后被其他会话或服务器上的手工编辑修改。
var ErrConfigConflict = errors.New("config revision conflict")

// ErrConfigBackupFailed 表示保存前创建配置快照失败。
var ErrConfigBackupFailed = errors.New("config backup failed")

// PreparedConfigUpdate 是一次白名单配置更新的预检结果。
// Original 是读取到的原始文件内容，Next 是通过校验、可以直接原子写回的候选内容。
type PreparedConfigUpdate struct {
	Original      []byte
	Next          []byte
	ChangedFields []string
	// EffectiveAddr 是候选配置最终会使用的监听地址，用于端口预检。
	EffectiveAddr string
}

// EditableUpdateResult 描述一次配置保存或恢复的结果。
type EditableUpdateResult struct {
	Revision      string            `json:"revision"`
	ChangedFields []string          `json:"changed_fields"`
	Backup        *ConfigBackupInfo `json:"backup,omitempty"`
	Noop          bool              `json:"noop"`
}

// ConfigRevision 返回配置文件内容哈希，用于乐观并发控制。
func ConfigRevision(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("读取配置文件 %s 失败: %w", path, err)
	}
	return configRevision(data), nil
}

func configRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// EffectiveAddr 返回配置文件最终会使用的监听地址。
func EffectiveAddr(path string) (string, error) {
	fc, _, err := loadFile(path)
	if err != nil {
		return "", err
	}
	return resolveAddr(fc)
}

// PrepareConfigUpdate 读取配置文件、应用白名单补丁并完成全部校验。
// expectedRevision 非空时，文件内容必须与它一致，否则返回 ErrConfigConflict。
func PrepareConfigUpdate(path string, patch SetupConfigPatch, expectedRevision string) (*PreparedConfigUpdate, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("配置文件不存在: %s", path)
	}
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}
	if expectedRevision != "" && configRevision(data) != expectedRevision {
		return nil, ErrConfigConflict
	}
	normalized, err := normalizeSetupPatch(patch)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	next, changed, err := prepareConfigBytes(data, normalized)
	if err != nil {
		return nil, err
	}
	nextFC := defaults()
	effective := ""
	if err := yaml.Unmarshal(next, nextFC); err == nil {
		effective, _ = resolveAddr(nextFC)
	}
	return &PreparedConfigUpdate{
		Original:      data,
		Next:          next,
		ChangedFields: changed,
		EffectiveAddr: effective,
	}, nil
}

func prepareConfigBytes(data []byte, patch SetupConfigPatch) ([]byte, []string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	root, err := documentMapping(&doc)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	resetFields := presentResetFields(root, patch.Reset)
	if err := applySetupPatch(root, patch); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, nil, fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, fmt.Errorf("序列化配置失败: %w", err)
	}

	fc := defaults()
	if err := yaml.Unmarshal(buf.Bytes(), fc); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	if err := validateFileValues(fc); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	if err := validateSetupValues(fc); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}

	before := defaults()
	if err := yaml.Unmarshal(data, before); err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrSetupConfigInvalid, err)
	}
	changed := diffSetupConfig(setupConfigFromFile(before), setupConfigFromFile(fc))
	for _, field := range resetFields {
		found := false
		for _, existing := range changed {
			if existing == field {
				found = true
				break
			}
		}
		if !found {
			changed = append(changed, field)
		}
	}
	sort.Strings(changed)
	return buf.Bytes(), changed, nil
}

func normalizeSetupPatch(patch SetupConfigPatch) (SetupConfigPatch, error) {
	if patch.TrustedProxies != nil {
		values, err := NormalizeTrustedProxies(*patch.TrustedProxies)
		if err != nil {
			return patch, err
		}
		patch.TrustedProxies = &values
	}
	if len(patch.Reset) > 0 {
		values := make([]string, 0, len(patch.Reset))
		seen := make(map[string]bool, len(patch.Reset))
		for _, item := range patch.Reset {
			value := strings.TrimSpace(item)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
		patch.Reset = values
	}
	return patch, nil
}

// NormalizeTrustedProxies 规范化可信代理列表：校验、去重、统一 IPv4/IPv6 写法。
func NormalizeTrustedProxies(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, item := range values {
		value, err := NormalizeTrustedProxy(item)
		if err != nil {
			return nil, err
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out, nil
}

// NormalizeTrustedProxy 校验并规范化单个可信代理 IP/CIDR。
// 单地址返回不带前缀的 IP，网段返回掩码后的 CIDR；拒绝 /0 与带 zone 的地址。
func NormalizeTrustedProxy(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("server.trusted_proxies 不能包含空值")
	}
	if strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return "", fmt.Errorf("server.trusted_proxies 包含无效 CIDR: %s", value)
		}
		if prefix.Addr().Zone() != "" {
			return "", fmt.Errorf("server.trusted_proxies 不支持带网卡 zone 的地址: %s", value)
		}
		if prefix.Addr().Is4In6() {
			if prefix.Bits() < 96 {
				return "", fmt.Errorf("server.trusted_proxies 的 IPv4-mapped 网段前缀过短: %s", value)
			}
			prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
		}
		if prefix.Bits() == 0 {
			return "", fmt.Errorf("server.trusted_proxies 不允许使用 /0 网段: %s", value)
		}
		return prefix.Masked().String(), nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return "", fmt.Errorf("server.trusted_proxies 包含无效 IP: %s", value)
	}
	if addr.Zone() != "" {
		return "", fmt.Errorf("server.trusted_proxies 不支持带网卡 zone 的地址: %s", value)
	}
	return addr.Unmap().String(), nil
}

// TrustedProxyContains 判断地址是否已被可信代理列表覆盖。
func TrustedProxyContains(values []string, addr netip.Addr) bool {
	addr = addr.Unmap()
	for _, item := range values {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.Contains(item, "/") {
			if prefix, err := netip.ParsePrefix(item); err == nil && prefix.Contains(addr) {
				return true
			}
			continue
		}
		if parsed, err := netip.ParseAddr(item); err == nil && parsed.Unmap() == addr {
			return true
		}
	}
	return false
}

// SetupConfigPatchFrom 把完整白名单配置转换为局部更新补丁。
// 用于从配置文件快照恢复：密钥与路径字段不在白名单内，因此不会被覆盖。
func SetupConfigPatchFrom(cfg *SetupConfig) SetupConfigPatch {
	if cfg == nil {
		return SetupConfigPatch{}
	}
	addr := cfg.Server.Addr
	host := cfg.Server.Host
	port := cfg.Server.Port
	clientIPMode := cfg.Server.ClientIPMode
	proxyProtocol := cfg.Server.ProxyProtocol
	proxies := append([]string(nil), cfg.TrustedProxies...)
	publicURL := cfg.PublicURL
	sessionPersistence := cfg.SessionPersistence
	rateLimitMaxKeys := cfg.RateLimit.MaxKeys
	rateLimitRules := make(map[string]SetupRateLimitRulePatch, len(cfg.RateLimit.Rules))
	for name, rule := range cfg.RateLimit.Rules {
		max, window := rule.Max, rule.WindowSeconds
		rateLimitRules[name] = SetupRateLimitRulePatch{Max: &max, WindowSeconds: &window}
	}

	acmeEnabled := cfg.ACME.Enabled
	acmeAddr := cfg.ACME.Addr
	acmeBaseURL := cfg.ACME.BaseURL
	acmeTLSCert := cfg.ACME.TLS.Cert
	acmeTLSKey := cfg.ACME.TLS.Key
	acmeMTLSEnabled := cfg.ACME.MTLS.Enabled
	acmeMTLSClientCA := cfg.ACME.MTLS.ClientCA

	logLevel := cfg.Log.Level
	logFormat := cfg.Log.Format
	logAlsoStdout := cfg.Log.AlsoStdout
	logMaxSizeMB := cfg.Log.MaxSizeMB
	logMaxBackups := cfg.Log.MaxBackups
	logMaxAgeDays := cfg.Log.MaxAgeDays
	logCompress := cfg.Log.Compress
	logAccessLog := cfg.Log.AccessLog
	logSystemEnabled := cfg.Log.System.Enabled
	logSystemLevel := cfg.Log.System.Level
	logSystemAccess := cfg.Log.System.Access
	logSystemRetentionDays := cfg.Log.System.RetentionDays
	logSystemMaxRows := cfg.Log.System.MaxRows
	logArchiveEnabled := cfg.Log.Archive.Enabled
	logArchiveCompress := cfg.Log.Archive.Compress
	logArchiveRetentionDays := cfg.Log.Archive.RetentionDays
	logArchiveMaxTotalMB := cfg.Log.Archive.MaxTotalMB
	logArchiveQuotaPolicy := cfg.Log.Archive.QuotaPolicy

	upgradeAuto := cfg.Upgrade.Auto
	upgradeAutoOptimize := cfg.Upgrade.AutoOptimize
	upgradeVacuum := cfg.Upgrade.Vacuum
	programEnabled := cfg.Upgrade.Program.Enabled
	programChannel := cfg.Upgrade.Program.Channel
	programCheckInterval := cfg.Upgrade.Program.CheckIntervalHours
	programMaxDownloadMB := cfg.Upgrade.Program.MaxDownloadMB
	programAllowUnsigned := cfg.Upgrade.Program.AllowUnsigned

	pluginsEnabled := cfg.Plugins.Enabled
	pluginsMemoryLimitMB := cfg.Plugins.MemoryLimitMB
	pluginsTimeoutSeconds := cfg.Plugins.TimeoutSeconds
	pluginsMaxOutputKB := cfg.Plugins.MaxOutputKB

	deployEnabled := cfg.DeployPlugins.Enabled
	deployMemoryLimitMB := cfg.DeployPlugins.MemoryLimitMB
	deployTimeoutSeconds := cfg.DeployPlugins.TimeoutSeconds
	deployMaxOutputKB := cfg.DeployPlugins.MaxOutputKB

	notifyEnabled := cfg.NotifyPlugins.Enabled
	notifyMemoryLimitMB := cfg.NotifyPlugins.MemoryLimitMB
	notifyTimeoutSeconds := cfg.NotifyPlugins.TimeoutSeconds
	notifyMaxOutputKB := cfg.NotifyPlugins.MaxOutputKB

	return SetupConfigPatch{
		Server: &SetupServerPatch{
			Addr:          &addr,
			Host:          &host,
			Port:          &port,
			ClientIPMode:  &clientIPMode,
			ProxyProtocol: &proxyProtocol,
		},
		TrustedProxies:     &proxies,
		PublicURL:          &publicURL,
		SessionPersistence: &sessionPersistence,
		RateLimit: &SetupRateLimitPatch{
			MaxKeys: &rateLimitMaxKeys,
			Rules:   rateLimitRules,
		},
		ACME: &SetupACMEPatch{
			Enabled: &acmeEnabled,
			Addr:    &acmeAddr,
			BaseURL: &acmeBaseURL,
			TLS: &SetupTLSPatch{
				Cert: &acmeTLSCert,
				Key:  &acmeTLSKey,
			},
			MTLS: &SetupMTLSPatch{
				Enabled:  &acmeMTLSEnabled,
				ClientCA: &acmeMTLSClientCA,
			},
		},
		Log: &SetupLogPatch{
			Level:      &logLevel,
			Format:     &logFormat,
			AlsoStdout: &logAlsoStdout,
			MaxSizeMB:  &logMaxSizeMB,
			MaxBackups: &logMaxBackups,
			MaxAgeDays: &logMaxAgeDays,
			Compress:   &logCompress,
			AccessLog:  &logAccessLog,
			System: &SetupLogSystemPatch{
				Enabled:       &logSystemEnabled,
				Level:         &logSystemLevel,
				Access:        &logSystemAccess,
				RetentionDays: &logSystemRetentionDays,
				MaxRows:       &logSystemMaxRows,
			},
			Archive: &SetupLogArchivePatch{
				Enabled:       &logArchiveEnabled,
				Compress:      &logArchiveCompress,
				RetentionDays: &logArchiveRetentionDays,
				MaxTotalMB:    &logArchiveMaxTotalMB,
				QuotaPolicy:   &logArchiveQuotaPolicy,
			},
		},
		Upgrade: &SetupUpgradePatch{
			Auto:         &upgradeAuto,
			AutoOptimize: &upgradeAutoOptimize,
			Vacuum:       &upgradeVacuum,
			Program: &SetupProgramPatch{
				Enabled:            &programEnabled,
				Channel:            &programChannel,
				CheckIntervalHours: &programCheckInterval,
				MaxDownloadMB:      &programMaxDownloadMB,
				AllowUnsigned:      &programAllowUnsigned,
			},
		},
		Plugins: &SetupPluginsPatch{
			Enabled:        &pluginsEnabled,
			MemoryLimitMB:  &pluginsMemoryLimitMB,
			TimeoutSeconds: &pluginsTimeoutSeconds,
			MaxOutputKB:    &pluginsMaxOutputKB,
		},
		DeployPlugins: &SetupPluginRuntimePatch{
			Enabled:        &deployEnabled,
			MemoryLimitMB:  &deployMemoryLimitMB,
			TimeoutSeconds: &deployTimeoutSeconds,
			MaxOutputKB:    &deployMaxOutputKB,
		},
		NotifyPlugins: &SetupPluginRuntimePatch{
			Enabled:        &notifyEnabled,
			MemoryLimitMB:  &notifyMemoryLimitMB,
			TimeoutSeconds: &notifyTimeoutSeconds,
			MaxOutputKB:    &notifyMaxOutputKB,
		},
	}
}

// diffSetupConfig 比较两份白名单配置，返回发生变化的字段名。
func diffSetupConfig(before, after *SetupConfig) []string {
	left := flattenConfigJSON(before)
	right := flattenConfigJSON(after)
	keys := make(map[string]bool, len(left)+len(right))
	for key := range left {
		keys[key] = true
	}
	for key := range right {
		keys[key] = true
	}
	changed := make([]string, 0)
	for key := range keys {
		if fmt.Sprint(left[key]) != fmt.Sprint(right[key]) {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}

func flattenConfigJSON(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return map[string]any{}
	}
	out := make(map[string]any)
	flattenConfigValue("", decoded, out)
	return out
}

func flattenConfigValue(prefix string, value any, out map[string]any) {
	if section, ok := value.(map[string]any); ok {
		for key, child := range section {
			next := key
			if prefix != "" {
				next = prefix + "." + key
			}
			flattenConfigValue(next, child, out)
		}
		return
	}
	out[prefix] = value
}
