package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"lactssl/internal/ratelimit"
)

// ErrSetupConfigInvalid 表示初始化页面提交的配置内容不合法。
// API 层据此区分「用户填写错误」和「文件读写失败」。
var ErrSetupConfigInvalid = errors.New("setup config invalid")

// SetupConfig 是初始化页面允许查看和修改的 config.yaml 白名单字段。
// 不包含 jwt_secret、salt、门禁入口、data_dir、database.path、日志文件、插件目录、
// 程序仓库/签名公钥/token 等敏感或高风险字段；这些字段只能在服务器上直接编辑 config.yaml。
type SetupConfig struct {
	Server             SetupServerConfig        `json:"server"`
	TrustedProxies     []string                 `json:"trusted_proxies"`
	PublicURL          string                   `json:"public_url"`
	SessionPersistence bool                     `json:"session_persistence"`
	RateLimit          SetupRateLimitConfig     `json:"rate_limit"`
	ACME               SetupACMEConfig          `json:"acme"`
	Log                SetupLogConfig           `json:"log"`
	Upgrade            SetupUpgradeConfig       `json:"upgrade"`
	Plugins            SetupPluginsConfig       `json:"plugins"`
	DeployPlugins      SetupPluginRuntimeConfig `json:"deploy_plugins"`
	NotifyPlugins      SetupPluginRuntimeConfig `json:"notify_plugins"`
}

type SetupServerConfig struct {
	Addr string `json:"addr"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

type SetupACMEConfig struct {
	Enabled bool            `json:"enabled"`
	Addr    string          `json:"addr"`
	BaseURL string          `json:"base_url"`
	TLS     SetupTLSConfig  `json:"tls"`
	MTLS    SetupMTLSConfig `json:"mtls"`
}

type SetupTLSConfig struct {
	Cert string `json:"cert"`
	Key  string `json:"key"`
}

type SetupMTLSConfig struct {
	Enabled  bool   `json:"enabled"`
	ClientCA string `json:"client_ca"`
}

// SetupRateLimitConfig 是限流阈值的完整生效值（默认值 + 配置文件覆盖）。
type SetupRateLimitConfig struct {
	MaxKeys int                           `json:"max_keys"`
	Rules   map[string]SetupRateLimitRule `json:"rules"`
}

// SetupRateLimitRule 是单条限流规则的生效值。
type SetupRateLimitRule struct {
	Max           int `json:"max"`
	WindowSeconds int `json:"window_seconds"`
}

type SetupLogConfig struct {
	Level      string               `json:"level"`
	Format     string               `json:"format"`
	AlsoStdout bool                 `json:"also_stdout"`
	MaxSizeMB  int                  `json:"max_size_mb"`
	MaxBackups int                  `json:"max_backups"`
	MaxAgeDays int                  `json:"max_age_days"`
	Compress   bool                 `json:"compress"`
	AccessLog  bool                 `json:"access_log"`
	System     SetupLogSystemConfig `json:"system"`
}

type SetupLogSystemConfig struct {
	Enabled       bool   `json:"enabled"`
	Level         string `json:"level"`
	Access        bool   `json:"access"`
	RetentionDays int    `json:"retention_days"`
	MaxRows       int    `json:"max_rows"`
}

type SetupUpgradeConfig struct {
	Auto         bool               `json:"auto"`
	AutoOptimize bool               `json:"auto_optimize"`
	Vacuum       bool               `json:"vacuum"`
	Program      SetupProgramConfig `json:"program"`
}

type SetupProgramConfig struct {
	Enabled            bool   `json:"enabled"`
	Channel            string `json:"channel"`
	CheckIntervalHours int    `json:"check_interval_hours"`
	MaxDownloadMB      int    `json:"max_download_mb"`
	AllowUnsigned      bool   `json:"allow_unsigned"`
}

type SetupPluginsConfig struct {
	Enabled        bool                  `json:"enabled"`
	MemoryLimitMB  int                   `json:"memory_limit_mb"`
	TimeoutSeconds int                   `json:"timeout_seconds"`
	MaxOutputKB    int                   `json:"max_output_kb"`
	Repo           SetupPluginRepoConfig `json:"repo"`
}

type SetupPluginRepoConfig struct {
	Enabled            bool   `json:"enabled"`
	URL                string `json:"url"`
	Channel            string `json:"channel"`
	AutoUpdate         bool   `json:"auto_update"`
	TimeoutSeconds     int    `json:"timeout_seconds"`
	MaxDownloadMB      int    `json:"max_download_mb"`
	CheckIntervalHours int    `json:"check_interval_hours"`
}

type SetupPluginRuntimeConfig struct {
	Enabled        bool `json:"enabled"`
	MemoryLimitMB  int  `json:"memory_limit_mb"`
	TimeoutSeconds int  `json:"timeout_seconds"`
	MaxOutputKB    int  `json:"max_output_kb"`
}

// SetupConfigPatch 是初始化页面的局部更新请求。
// 使用指针区分「未提交」和「提交了零值」。
type SetupConfigPatch struct {
	Server             *SetupServerPatch        `json:"server"`
	TrustedProxies     *[]string                `json:"trusted_proxies"`
	PublicURL          *string                  `json:"public_url"`
	SessionPersistence *bool                    `json:"session_persistence"`
	RateLimit          *SetupRateLimitPatch     `json:"rate_limit"`
	ACME               *SetupACMEPatch          `json:"acme"`
	Log                *SetupLogPatch           `json:"log"`
	Upgrade            *SetupUpgradePatch       `json:"upgrade"`
	Plugins            *SetupPluginsPatch       `json:"plugins"`
	DeployPlugins      *SetupPluginRuntimePatch `json:"deploy_plugins"`
	NotifyPlugins      *SetupPluginRuntimePatch `json:"notify_plugins"`
}

type SetupServerPatch struct {
	Addr *string `json:"addr"`
	Host *string `json:"host"`
	Port *int    `json:"port"`
}

type SetupACMEPatch struct {
	Enabled *bool           `json:"enabled"`
	Addr    *string         `json:"addr"`
	BaseURL *string         `json:"base_url"`
	TLS     *SetupTLSPatch  `json:"tls"`
	MTLS    *SetupMTLSPatch `json:"mtls"`
}

type SetupTLSPatch struct {
	Cert *string `json:"cert"`
	Key  *string `json:"key"`
}

type SetupMTLSPatch struct {
	Enabled  *bool   `json:"enabled"`
	ClientCA *string `json:"client_ca"`
}

// SetupRateLimitPatch 是限流阈值的局部更新请求。
type SetupRateLimitPatch struct {
	MaxKeys *int                               `json:"max_keys"`
	Rules   map[string]SetupRateLimitRulePatch `json:"rules"`
}

// SetupRateLimitRulePatch 只写入显式提交的字段。
type SetupRateLimitRulePatch struct {
	Max           *int `json:"max"`
	WindowSeconds *int `json:"window_seconds"`
}

type SetupLogPatch struct {
	Level      *string              `json:"level"`
	Format     *string              `json:"format"`
	AlsoStdout *bool                `json:"also_stdout"`
	MaxSizeMB  *int                 `json:"max_size_mb"`
	MaxBackups *int                 `json:"max_backups"`
	MaxAgeDays *int                 `json:"max_age_days"`
	Compress   *bool                `json:"compress"`
	AccessLog  *bool                `json:"access_log"`
	System     *SetupLogSystemPatch `json:"system"`
}

type SetupLogSystemPatch struct {
	Enabled       *bool   `json:"enabled"`
	Level         *string `json:"level"`
	Access        *bool   `json:"access"`
	RetentionDays *int    `json:"retention_days"`
	MaxRows       *int    `json:"max_rows"`
}

type SetupUpgradePatch struct {
	Auto         *bool              `json:"auto"`
	AutoOptimize *bool              `json:"auto_optimize"`
	Vacuum       *bool              `json:"vacuum"`
	Program      *SetupProgramPatch `json:"program"`
}

type SetupProgramPatch struct {
	Enabled            *bool   `json:"enabled"`
	Channel            *string `json:"channel"`
	CheckIntervalHours *int    `json:"check_interval_hours"`
	MaxDownloadMB      *int    `json:"max_download_mb"`
	AllowUnsigned      *bool   `json:"allow_unsigned"`
}

type SetupPluginsPatch struct {
	Enabled        *bool                 `json:"enabled"`
	MemoryLimitMB  *int                  `json:"memory_limit_mb"`
	TimeoutSeconds *int                  `json:"timeout_seconds"`
	MaxOutputKB    *int                  `json:"max_output_kb"`
	Repo           *SetupPluginRepoPatch `json:"repo"`
}

type SetupPluginRepoPatch struct {
	Enabled            *bool   `json:"enabled"`
	URL                *string `json:"url"`
	Channel            *string `json:"channel"`
	AutoUpdate         *bool   `json:"auto_update"`
	TimeoutSeconds     *int    `json:"timeout_seconds"`
	MaxDownloadMB      *int    `json:"max_download_mb"`
	CheckIntervalHours *int    `json:"check_interval_hours"`
}

type SetupPluginRuntimePatch struct {
	Enabled        *bool `json:"enabled"`
	MemoryLimitMB  *int  `json:"memory_limit_mb"`
	TimeoutSeconds *int  `json:"timeout_seconds"`
	MaxOutputKB    *int  `json:"max_output_kb"`
}

// Empty 表示没有任何需要写入的字段。
func (p SetupConfigPatch) Empty() bool {
	if p.Server != nil && !p.Server.empty() {
		return false
	}
	if p.ACME != nil && !p.ACME.empty() {
		return false
	}
	if p.Log != nil && !p.Log.empty() {
		return false
	}
	if p.Upgrade != nil && !p.Upgrade.empty() {
		return false
	}
	if p.Plugins != nil && !p.Plugins.empty() {
		return false
	}
	if p.DeployPlugins != nil && !p.DeployPlugins.empty() {
		return false
	}
	if p.NotifyPlugins != nil && !p.NotifyPlugins.empty() {
		return false
	}
	if p.RateLimit != nil && !p.RateLimit.empty() {
		return false
	}
	return p.TrustedProxies == nil && p.PublicURL == nil && p.SessionPersistence == nil
}

func (p SetupServerPatch) empty() bool {
	return p.Addr == nil && p.Host == nil && p.Port == nil
}

func (p SetupACMEPatch) empty() bool {
	if p.Enabled != nil || p.Addr != nil || p.BaseURL != nil {
		return false
	}
	if p.TLS != nil && (p.TLS.Cert != nil || p.TLS.Key != nil) {
		return false
	}
	if p.MTLS != nil && (p.MTLS.Enabled != nil || p.MTLS.ClientCA != nil) {
		return false
	}
	return true
}

func (p SetupLogPatch) empty() bool {
	if p.Level != nil || p.Format != nil || p.AlsoStdout != nil || p.MaxSizeMB != nil ||
		p.MaxBackups != nil || p.MaxAgeDays != nil || p.Compress != nil || p.AccessLog != nil {
		return false
	}
	if p.System != nil && (p.System.Enabled != nil || p.System.Level != nil || p.System.Access != nil ||
		p.System.RetentionDays != nil || p.System.MaxRows != nil) {
		return false
	}
	return true
}

func (p SetupUpgradePatch) empty() bool {
	if p.Auto != nil || p.AutoOptimize != nil || p.Vacuum != nil {
		return false
	}
	if p.Program != nil && (p.Program.Enabled != nil || p.Program.Channel != nil ||
		p.Program.CheckIntervalHours != nil || p.Program.MaxDownloadMB != nil) {
		return false
	}
	return true
}

func (p SetupPluginsPatch) empty() bool {
	if p.Enabled != nil || p.MemoryLimitMB != nil || p.TimeoutSeconds != nil || p.MaxOutputKB != nil {
		return false
	}
	if p.Repo != nil && (p.Repo.Enabled != nil || p.Repo.URL != nil || p.Repo.Channel != nil ||
		p.Repo.AutoUpdate != nil || p.Repo.TimeoutSeconds != nil ||
		p.Repo.MaxDownloadMB != nil || p.Repo.CheckIntervalHours != nil) {
		return false
	}
	return true
}

func (p SetupPluginRuntimePatch) empty() bool {
	return p.Enabled == nil && p.MemoryLimitMB == nil && p.TimeoutSeconds == nil && p.MaxOutputKB == nil
}

func (p SetupRateLimitPatch) empty() bool {
	return p.MaxKeys == nil && p.Rules == nil
}

// LoadSetupConfig 读取初始化页面可编辑的配置项。
func LoadSetupConfig(path string) (*SetupConfig, error) {
	fc, _, err := loadFile(path)
	if err != nil {
		return nil, err
	}
	return setupConfigFromFile(fc), nil
}

// setupConfigFromFile 把文件结构转换为对外的可编辑配置。
// 只复制白名单字段，避免密钥、路径等敏感项进入 API 响应。
func setupConfigFromFile(fc *fileConfig) *SetupConfig {
	proxies := make([]string, len(fc.Server.TrustedProxies))
	copy(proxies, fc.Server.TrustedProxies)
	port := fc.Server.Port
	if port == 0 {
		// 与 resolveAddr 保持一致：0 表示使用默认端口。
		port = 8080
	}
	return &SetupConfig{
		Server: SetupServerConfig{
			Addr: strings.TrimSpace(fc.Server.Addr),
			Host: strings.TrimSpace(fc.Server.Host),
			Port: port,
		},
		TrustedProxies:     proxies,
		PublicURL:          strings.TrimSpace(fc.Security.PublicURL),
		SessionPersistence: fc.Security.SessionPersistence,
		RateLimit:          setupRateLimitConfig(fc),
		ACME: SetupACMEConfig{
			Enabled: fc.ACME.Enabled,
			Addr:    strings.TrimSpace(fc.ACME.Addr),
			BaseURL: strings.TrimSpace(fc.ACME.BaseURL),
			TLS: SetupTLSConfig{
				Cert: strings.TrimSpace(fc.ACME.TLS.Cert),
				Key:  strings.TrimSpace(fc.ACME.TLS.Key),
			},
			MTLS: SetupMTLSConfig{
				Enabled:  fc.ACME.MTLS.Enabled,
				ClientCA: strings.TrimSpace(fc.ACME.MTLS.ClientCA),
			},
		},
		Log: SetupLogConfig{
			Level:      strings.ToLower(strings.TrimSpace(fc.Log.Level)),
			Format:     strings.ToLower(strings.TrimSpace(fc.Log.Format)),
			AlsoStdout: fc.Log.AlsoStdout,
			MaxSizeMB:  fc.Log.MaxSizeMB,
			MaxBackups: fc.Log.MaxBackups,
			MaxAgeDays: fc.Log.MaxAgeDays,
			Compress:   fc.Log.Compress,
			AccessLog:  fc.Log.AccessLog,
			System: SetupLogSystemConfig{
				Enabled:       fc.Log.System.Enabled,
				Level:         strings.ToLower(strings.TrimSpace(fc.Log.System.Level)),
				Access:        fc.Log.System.Access,
				RetentionDays: fc.Log.System.RetentionDays,
				MaxRows:       fc.Log.System.MaxRows,
			},
		},
		Upgrade: SetupUpgradeConfig{
			Auto:         fc.Upgrade.Auto,
			AutoOptimize: fc.Upgrade.AutoOptimize,
			Vacuum:       fc.Upgrade.Vacuum,
			Program: SetupProgramConfig{
				Enabled:            fc.Upgrade.Program.Enabled,
				Channel:            strings.ToLower(strings.TrimSpace(fc.Upgrade.Program.Channel)),
				CheckIntervalHours: fc.Upgrade.Program.CheckIntervalHours,
				MaxDownloadMB:      fc.Upgrade.Program.MaxDownloadMB,
				AllowUnsigned:      fc.Upgrade.Program.AllowUnsigned,
			},
		},
		Plugins: SetupPluginsConfig{
			Enabled:        fc.Plugins.Enabled,
			MemoryLimitMB:  fc.Plugins.MemoryLimitMB,
			TimeoutSeconds: fc.Plugins.TimeoutSeconds,
			MaxOutputKB:    fc.Plugins.MaxOutputKB,
			Repo: SetupPluginRepoConfig{
				Enabled:            fc.Plugins.Repo.Enabled,
				URL:                strings.TrimSpace(fc.Plugins.Repo.URL),
				Channel:            strings.ToLower(strings.TrimSpace(fc.Plugins.Repo.Channel)),
				AutoUpdate:         fc.Plugins.Repo.AutoUpdate,
				TimeoutSeconds:     fc.Plugins.Repo.TimeoutSeconds,
				MaxDownloadMB:      fc.Plugins.Repo.MaxDownloadMB,
				CheckIntervalHours: fc.Plugins.Repo.CheckIntervalHours,
			},
		},
		DeployPlugins: SetupPluginRuntimeConfig{
			Enabled:        fc.DeployPlugins.Enabled,
			MemoryLimitMB:  fc.DeployPlugins.MemoryLimitMB,
			TimeoutSeconds: fc.DeployPlugins.TimeoutSeconds,
			MaxOutputKB:    fc.DeployPlugins.MaxOutputKB,
		},
		NotifyPlugins: SetupPluginRuntimeConfig{
			Enabled:        fc.NotifyPlugins.Enabled,
			MemoryLimitMB:  fc.NotifyPlugins.MemoryLimitMB,
			TimeoutSeconds: fc.NotifyPlugins.TimeoutSeconds,
			MaxOutputKB:    fc.NotifyPlugins.MaxOutputKB,
		},
	}
}

// setupRateLimitConfig 返回合并默认值后的完整限流配置，供设置页面展示。
func setupRateLimitConfig(fc *fileConfig) SetupRateLimitConfig {
	cfg, err := resolveRateLimit(fc)
	if err != nil {
		// 单独读取页面时不让一条非法规则导致整个页面打不开；
		// 保存时仍会经过完整校验并拒绝非法值。
		cfg = ratelimit.DefaultConfig()
	}
	rules := make(map[string]SetupRateLimitRule, len(ratelimit.Names()))
	for _, name := range ratelimit.Names() {
		rule := cfg.For(name)
		rules[name] = SetupRateLimitRule{
			Max:           rule.Max,
			WindowSeconds: int(rule.Window.Seconds()),
		}
	}
	return SetupRateLimitConfig{MaxKeys: cfg.MaxKeys, Rules: rules}
}

// UpdateSetupConfig 只更新白名单字段，保留配置文件中的其他内容和注释。
func UpdateSetupConfig(path string, patch SetupConfigPatch) error {
	if patch.Empty() {
		return nil
	}
	prepared, err := PrepareConfigUpdate(path, patch, "")
	if err != nil {
		return err
	}
	if len(prepared.ChangedFields) == 0 {
		return nil
	}
	return writeFileAtomic(path, prepared.Next)
}

func documentMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("配置文件为空")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("配置文件根节点必须是映射")
	}
	return root, nil
}

func applySetupPatch(root *yaml.Node, patch SetupConfigPatch) error {
	if patch.Server != nil {
		section, err := ensureMapping(root, "server")
		if err != nil {
			return err
		}
		if patch.Server.Port != nil {
			port := *patch.Server.Port
			if port < 0 || port > 65535 {
				return fmt.Errorf("server.port 必须在 0-65535 之间")
			}
		}
		if err := setOptString(section, "addr", patch.Server.Addr); err != nil {
			return err
		}
		if err := setOptString(section, "host", patch.Server.Host); err != nil {
			return err
		}
		if err := setOptInt(section, "port", patch.Server.Port); err != nil {
			return err
		}
	}
	if patch.TrustedProxies != nil {
		section, err := ensureMapping(root, "server")
		if err != nil {
			return err
		}
		if err := setStringSequence(section, "trusted_proxies", *patch.TrustedProxies); err != nil {
			return err
		}
	}
	if patch.PublicURL != nil || patch.SessionPersistence != nil {
		section, err := ensureMapping(root, "security")
		if err != nil {
			return err
		}
		if patch.PublicURL != nil {
			value := strings.TrimRight(strings.TrimSpace(*patch.PublicURL), "/")
			if err := setScalar(section, "public_url", "!!str", value); err != nil {
				return err
			}
		}
		if err := setOptBool(section, "session_persistence", patch.SessionPersistence); err != nil {
			return err
		}
	}
	if patch.RateLimit != nil {
		securitySection, err := ensureMapping(root, "security")
		if err != nil {
			return err
		}
		section, err := ensureMapping(securitySection, "rate_limit")
		if err != nil {
			return err
		}
		if err := setOptInt(section, "max_keys", patch.RateLimit.MaxKeys); err != nil {
			return err
		}
		if patch.RateLimit.Rules != nil {
			rulesSection, err := ensureMapping(section, "rules")
			if err != nil {
				return err
			}
			names := make([]string, 0, len(patch.RateLimit.Rules))
			for name := range patch.RateLimit.Rules {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if !ratelimit.Known(name) {
					return fmt.Errorf("security.rate_limit.rules.%s 不是已知的限流规则", name)
				}
				rule := patch.RateLimit.Rules[name]
				ruleSection, err := ensureMapping(rulesSection, name)
				if err != nil {
					return err
				}
				if err := setOptInt(ruleSection, "max", rule.Max); err != nil {
					return err
				}
				if err := setOptInt(ruleSection, "window_seconds", rule.WindowSeconds); err != nil {
					return err
				}
			}
		}
	}
	if patch.ACME != nil {
		section, err := ensureMapping(root, "acme")
		if err != nil {
			return err
		}
		if err := setOptBool(section, "enabled", patch.ACME.Enabled); err != nil {
			return err
		}
		if err := setOptString(section, "addr", patch.ACME.Addr); err != nil {
			return err
		}
		if patch.ACME.BaseURL != nil {
			value := strings.TrimRight(strings.TrimSpace(*patch.ACME.BaseURL), "/")
			if err := setScalar(section, "base_url", "!!str", value); err != nil {
				return err
			}
		}
		if patch.ACME.TLS != nil {
			tlsSection, err := ensureMapping(section, "tls")
			if err != nil {
				return err
			}
			if err := setOptString(tlsSection, "cert", patch.ACME.TLS.Cert); err != nil {
				return err
			}
			if err := setOptString(tlsSection, "key", patch.ACME.TLS.Key); err != nil {
				return err
			}
		}
		if patch.ACME.MTLS != nil {
			mtlsSection, err := ensureMapping(section, "mtls")
			if err != nil {
				return err
			}
			if err := setOptBool(mtlsSection, "enabled", patch.ACME.MTLS.Enabled); err != nil {
				return err
			}
			if err := setOptString(mtlsSection, "client_ca", patch.ACME.MTLS.ClientCA); err != nil {
				return err
			}
		}
	}
	if patch.Log != nil {
		section, err := ensureMapping(root, "log")
		if err != nil {
			return err
		}
		if err := setOptLower(section, "level", patch.Log.Level); err != nil {
			return err
		}
		if err := setOptLower(section, "format", patch.Log.Format); err != nil {
			return err
		}
		if err := setOptBool(section, "also_stdout", patch.Log.AlsoStdout); err != nil {
			return err
		}
		if err := setOptInt(section, "max_size_mb", patch.Log.MaxSizeMB); err != nil {
			return err
		}
		if err := setOptInt(section, "max_backups", patch.Log.MaxBackups); err != nil {
			return err
		}
		if err := setOptInt(section, "max_age_days", patch.Log.MaxAgeDays); err != nil {
			return err
		}
		if err := setOptBool(section, "compress", patch.Log.Compress); err != nil {
			return err
		}
		if err := setOptBool(section, "access_log", patch.Log.AccessLog); err != nil {
			return err
		}
		if patch.Log.System != nil {
			systemSection, err := ensureMapping(section, "system")
			if err != nil {
				return err
			}
			if err := setOptBool(systemSection, "enabled", patch.Log.System.Enabled); err != nil {
				return err
			}
			if err := setOptLower(systemSection, "level", patch.Log.System.Level); err != nil {
				return err
			}
			if err := setOptBool(systemSection, "access", patch.Log.System.Access); err != nil {
				return err
			}
			if err := setOptInt(systemSection, "retention_days", patch.Log.System.RetentionDays); err != nil {
				return err
			}
			if err := setOptInt(systemSection, "max_rows", patch.Log.System.MaxRows); err != nil {
				return err
			}
		}
	}
	if patch.Upgrade != nil {
		section, err := ensureMapping(root, "upgrade")
		if err != nil {
			return err
		}
		if err := setOptBool(section, "auto", patch.Upgrade.Auto); err != nil {
			return err
		}
		if err := setOptBool(section, "auto_optimize", patch.Upgrade.AutoOptimize); err != nil {
			return err
		}
		if err := setOptBool(section, "vacuum", patch.Upgrade.Vacuum); err != nil {
			return err
		}
		if patch.Upgrade.Program != nil {
			programSection, err := ensureMapping(section, "program")
			if err != nil {
				return err
			}
			if err := setOptBool(programSection, "enabled", patch.Upgrade.Program.Enabled); err != nil {
				return err
			}
			if err := setOptLower(programSection, "channel", patch.Upgrade.Program.Channel); err != nil {
				return err
			}
			if err := setOptInt(programSection, "check_interval_hours", patch.Upgrade.Program.CheckIntervalHours); err != nil {
				return err
			}
			if err := setOptInt(programSection, "max_download_mb", patch.Upgrade.Program.MaxDownloadMB); err != nil {
				return err
			}
			if err := setOptBool(programSection, "allow_unsigned", patch.Upgrade.Program.AllowUnsigned); err != nil {
				return err
			}
		}
	}
	if patch.Plugins != nil {
		section, err := ensureMapping(root, "plugins")
		if err != nil {
			return err
		}
		if err := applyPluginRuntimePatch(section, SetupPluginRuntimePatch{
			Enabled:        patch.Plugins.Enabled,
			MemoryLimitMB:  patch.Plugins.MemoryLimitMB,
			TimeoutSeconds: patch.Plugins.TimeoutSeconds,
			MaxOutputKB:    patch.Plugins.MaxOutputKB,
		}); err != nil {
			return err
		}
		if patch.Plugins.Repo != nil {
			repoSection, err := ensureMapping(section, "repo")
			if err != nil {
				return err
			}
			if err := setOptBool(repoSection, "enabled", patch.Plugins.Repo.Enabled); err != nil {
				return err
			}
			if err := setOptString(repoSection, "url", patch.Plugins.Repo.URL); err != nil {
				return err
			}
			if err := setOptLower(repoSection, "channel", patch.Plugins.Repo.Channel); err != nil {
				return err
			}
			if err := setOptBool(repoSection, "auto_update", patch.Plugins.Repo.AutoUpdate); err != nil {
				return err
			}
			if err := setOptInt(repoSection, "timeout_seconds", patch.Plugins.Repo.TimeoutSeconds); err != nil {
				return err
			}
			if err := setOptInt(repoSection, "max_download_mb", patch.Plugins.Repo.MaxDownloadMB); err != nil {
				return err
			}
			if err := setOptInt(repoSection, "check_interval_hours", patch.Plugins.Repo.CheckIntervalHours); err != nil {
				return err
			}
		}
	}
	for _, item := range []struct {
		key   string
		patch *SetupPluginRuntimePatch
	}{
		{"deploy_plugins", patch.DeployPlugins},
		{"notify_plugins", patch.NotifyPlugins},
	} {
		if item.patch == nil {
			continue
		}
		section, err := ensureMapping(root, item.key)
		if err != nil {
			return err
		}
		if err := applyPluginRuntimePatch(section, *item.patch); err != nil {
			return err
		}
	}
	return nil
}

// applyPluginRuntimePatch 写入插件通用字段（开关、资源限制）。
func applyPluginRuntimePatch(section *yaml.Node, patch SetupPluginRuntimePatch) error {
	if err := setOptBool(section, "enabled", patch.Enabled); err != nil {
		return err
	}
	if err := setOptInt(section, "memory_limit_mb", patch.MemoryLimitMB); err != nil {
		return err
	}
	if err := setOptInt(section, "timeout_seconds", patch.TimeoutSeconds); err != nil {
		return err
	}
	return setOptInt(section, "max_output_kb", patch.MaxOutputKB)
}

// setOptString 写入字符串字段；value 为 nil 时保持原值。
func setOptString(parent *yaml.Node, key string, value *string) error {
	if value == nil {
		return nil
	}
	return setScalar(parent, key, "!!str", strings.TrimSpace(*value))
}

// setOptLower 写入小写字符串字段；value 为 nil 时保持原值。
func setOptLower(parent *yaml.Node, key string, value *string) error {
	if value == nil {
		return nil
	}
	return setScalar(parent, key, "!!str", strings.ToLower(strings.TrimSpace(*value)))
}

// setOptInt 写入整数字段；value 为 nil 时保持原值。
func setOptInt(parent *yaml.Node, key string, value *int) error {
	if value == nil {
		return nil
	}
	return setScalar(parent, key, "!!int", strconv.Itoa(*value))
}

// setOptBool 写入布尔字段；value 为 nil 时保持原值。
func setOptBool(parent *yaml.Node, key string, value *bool) error {
	if value == nil {
		return nil
	}
	return setScalar(parent, key, "!!bool", strconv.FormatBool(*value))
}

func validateSetupValues(fc *fileConfig) error {
	if strings.TrimSpace(fc.Server.Addr) == "" {
		host := strings.TrimSpace(fc.Server.Host)
		if host != "" && strings.ContainsAny(host, " /\\\t\r\n") {
			return fmt.Errorf("server.host 格式不正确")
		}
		if fc.Server.Port < 0 || fc.Server.Port > 65535 {
			return fmt.Errorf("server.port 必须在 0-65535 之间")
		}
	}
	if _, err := NormalizeTrustedProxies(fc.Server.TrustedProxies); err != nil {
		return err
	}
	if strings.TrimSpace(fc.ACME.Addr) != "" {
		mainAddr, err := resolveAddr(fc)
		if err != nil {
			return err
		}
		if listenAddrConflict(mainAddr, strings.TrimSpace(fc.ACME.Addr)) {
			return fmt.Errorf("acme.addr 不能与主监听地址相同")
		}
	}
	switch channel := strings.ToLower(strings.TrimSpace(fc.Plugins.Repo.Channel)); channel {
	case "", "stable", "beta":
	default:
		return fmt.Errorf("plugins.repo.channel 必须是 stable 或 beta")
	}
	return nil
}

// listenAddrConflict 判断两个监听地址是否会争用同一个端口。
// 任一地址使用通配主机时，同端口的其他地址视为冲突。
func listenAddrConflict(left, right string) bool {
	leftHost, leftPort, leftErr := net.SplitHostPort(strings.TrimSpace(left))
	rightHost, rightPort, rightErr := net.SplitHostPort(strings.TrimSpace(right))
	if leftErr != nil || rightErr != nil {
		return strings.TrimSpace(left) == strings.TrimSpace(right)
	}
	if leftPort != rightPort {
		return false
	}
	if isWildcardListenHost(leftHost) || isWildcardListenHost(rightHost) {
		return true
	}
	return strings.EqualFold(strings.Trim(leftHost, "[]"), strings.Trim(rightHost, "[]"))
}

func isWildcardListenHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	return host == "" || host == "0.0.0.0" || host == "::"
}

func ensureMapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	value, index := mappingValue(parent, key)
	if value == nil {
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		parent.Content = append(parent.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
			node,
		)
		return node, nil
	}
	switch value.Kind {
	case yaml.MappingNode:
		return value, nil
	case yaml.ScalarNode:
		if value.Tag == "!!null" || value.Value == "" {
			replacement := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			parent.Content[index] = replacement
			return replacement, nil
		}
	}
	return nil, fmt.Errorf("%s 必须是映射", key)
}

func mappingValue(parent *yaml.Node, key string) (*yaml.Node, int) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1], i + 1
		}
	}
	return nil, -1
}

func setScalar(parent *yaml.Node, key, tag, value string) error {
	existing, index := mappingValue(parent, key)
	if existing != nil && existing.Kind != yaml.ScalarNode {
		return fmt.Errorf("%s 必须是标量", key)
	}
	node := &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	if existing != nil {
		node.HeadComment = existing.HeadComment
		node.LineComment = existing.LineComment
		node.FootComment = existing.FootComment
		parent.Content[index] = node
		return nil
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		node,
	)
	return nil
}

func setStringSequence(parent *yaml.Node, key string, values []string) error {
	existing, index := mappingValue(parent, key)
	if existing != nil && existing.Kind != yaml.SequenceNode && !(existing.Kind == yaml.ScalarNode && existing.Tag == "!!null") {
		return fmt.Errorf("%s 必须是列表", key)
	}
	node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, value := range values {
		node.Content = append(node.Content, &yaml.Node{
			Kind:  yaml.ScalarNode,
			Tag:   "!!str",
			Value: strings.TrimSpace(value),
		})
	}
	if existing != nil {
		node.HeadComment = existing.HeadComment
		node.LineComment = existing.LineComment
		node.FootComment = existing.FootComment
		parent.Content[index] = node
		return nil
	}
	parent.Content = append(parent.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		node,
	)
	return nil
}

// writeFileAtomic 先写临时文件，再重命名，避免配置写入中途失败留下截断文件。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建配置目录失败: %w", err)
		}
		_ = os.Chmod(dir, 0o700)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时配置文件失败: %w", err)
	}
	tmpName := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置临时配置文件权限失败: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时配置文件失败: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("同步临时配置文件失败: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时配置文件失败: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		// Windows 不允许 rename 覆盖已存在文件，退化为删除后重试。
		if removeErr := os.Remove(path); removeErr == nil || errors.Is(removeErr, os.ErrNotExist) {
			if retryErr := os.Rename(tmpName, path); retryErr == nil {
				removeTemp = false
				_ = os.Chmod(path, 0o600)
				return nil
			}
		}
		return fmt.Errorf("替换配置文件失败: %w", err)
	}
	removeTemp = false
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("设置配置文件权限失败: %w", err)
	}
	// 尽量同步目录项，降低掉电后 rename 尚未落盘的概率。
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
