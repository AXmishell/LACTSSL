package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConfigChange 描述配置文件优化中的一处变更。
type ConfigChange struct {
	Key    string `json:"key"`
	Action string `json:"action"` // added、normalized
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// OptimizeReport 是配置文件优化结果。Apply 为 false 时只做预检，不写入磁盘。
type OptimizeReport struct {
	Path     string         `json:"path"`
	Changed  bool           `json:"changed"`
	Applied  bool           `json:"applied"`
	Changes  []ConfigChange `json:"changes"`
	Warnings []string       `json:"warnings"`
}

// OptimizeFile 为历史配置补齐新版本字段、规范化等价写法，并保留原有注释。
// 在写入前会按启动加载逻辑完整校验，避免生成无法启动的配置。
func OptimizeFile(path string, apply bool) (OptimizeReport, error) {
	report := OptimizeReport{Path: path, Changes: []ConfigChange{}, Warnings: []string{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		report.Warnings = append(report.Warnings, "配置文件不存在，跳过配置优化")
		return report, nil
	}
	if err != nil {
		return report, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return report, fmt.Errorf("解析配置文件失败: %w", err)
	}
	root, err := documentMapping(&doc)
	if err != nil {
		return report, err
	}

	ensureDefaultNode(root, defaultSpec(), "", &report)
	normalizeConfigNode(root, "", &report)
	collectConfigWarnings(root, &report)

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return report, fmt.Errorf("序列化配置失败: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return report, fmt.Errorf("序列化配置失败: %w", err)
	}

	// 与启动时一致：先套默认值再解析，最后执行完整校验。
	fc := defaults()
	if err := yaml.Unmarshal(buf.Bytes(), fc); err != nil {
		return report, fmt.Errorf("校验配置失败: %w", err)
	}
	if err := validateFileValues(fc); err != nil {
		return report, fmt.Errorf("校验配置失败: %w", err)
	}

	report.Changed = len(report.Changes) > 0
	if !apply || !report.Changed {
		return report, nil
	}
	if err := writeFileAtomic(path, buf.Bytes()); err != nil {
		return report, err
	}
	report.Applied = true
	return report, nil
}

// defaultSpec 返回新版本配置的完整默认结构，用于补齐缺失字段。
func defaultSpec() map[string]any {
	fc := defaults()
	return map[string]any{
		"server": map[string]any{
			"addr":            fc.Server.Addr,
			"host":            fc.Server.Host,
			"port":            fc.Server.Port,
			"trusted_proxies": []string{},
			"client_ip_mode":  fc.Server.ClientIPMode,
			"proxy_protocol":  fc.Server.ProxyProtocol,
			"tls": map[string]any{
				"cert":            "",
				"key":             "",
				"proxy_client_ca": "",
			},
		},
		"data_dir": fc.DataDir,
		"database": map[string]any{
			"driver":                    fc.Database.Driver,
			"path":                      fc.Database.Path,
			"dsn":                       fc.Database.DSN,
			"max_open_conns":            fc.Database.MaxOpenConns,
			"max_idle_conns":            fc.Database.MaxIdleConns,
			"conn_max_lifetime_minutes": fc.Database.ConnMaxLifetime,
			"allow_multi_instance":      fc.Database.AllowMultiInstance,
		},
		"security": map[string]any{
			"entry":               fc.Security.Entry,
			"public_url":          fc.Security.PublicURL,
			"session_persistence": fc.Security.SessionPersistence,
			"jwt_secret":          fc.Security.JWTSecret,
			"salt":                fc.Security.Salt,
			"rate_limit": map[string]any{
				"max_keys": fc.Security.RateLimit.MaxKeys,
				// 动态规则表：旧配置缺少该节点时补一个空表，由运行时使用内置默认值。
				"rules": map[string]any{},
			},
		},
		"acme": map[string]any{
			"enabled":  fc.ACME.Enabled,
			"addr":     fc.ACME.Addr,
			"base_url": fc.ACME.BaseURL,
			"tls": map[string]any{
				"cert": fc.ACME.TLS.Cert,
				"key":  fc.ACME.TLS.Key,
			},
			"mtls": map[string]any{
				"enabled":   fc.ACME.MTLS.Enabled,
				"client_ca": fc.ACME.MTLS.ClientCA,
			},
		},
		"plugins": map[string]any{
			"enabled":         fc.Plugins.Enabled,
			"dir":             fc.Plugins.Dir,
			"memory_limit_mb": fc.Plugins.MemoryLimitMB,
			"timeout_seconds": fc.Plugins.TimeoutSeconds,
			"max_output_kb":   fc.Plugins.MaxOutputKB,
			"repo": map[string]any{
				"enabled":              fc.Plugins.Repo.Enabled,
				"url":                  fc.Plugins.Repo.URL,
				"channel":              fc.Plugins.Repo.Channel,
				"auto_update":          fc.Plugins.Repo.AutoUpdate,
				"public_key":           fc.Plugins.Repo.PublicKey,
				"proxy":                fc.Plugins.Repo.Proxy,
				"timeout_seconds":      fc.Plugins.Repo.TimeoutSeconds,
				"max_download_mb":      fc.Plugins.Repo.MaxDownloadMB,
				"check_interval_hours": fc.Plugins.Repo.CheckIntervalHours,
			},
		},
		"deploy_plugins": map[string]any{
			"enabled":         fc.DeployPlugins.Enabled,
			"dir":             fc.DeployPlugins.Dir,
			"memory_limit_mb": fc.DeployPlugins.MemoryLimitMB,
			"timeout_seconds": fc.DeployPlugins.TimeoutSeconds,
			"max_output_kb":   fc.DeployPlugins.MaxOutputKB,
		},
		"notify_plugins": map[string]any{
			"enabled":         fc.NotifyPlugins.Enabled,
			"dir":             fc.NotifyPlugins.Dir,
			"memory_limit_mb": fc.NotifyPlugins.MemoryLimitMB,
			"timeout_seconds": fc.NotifyPlugins.TimeoutSeconds,
			"max_output_kb":   fc.NotifyPlugins.MaxOutputKB,
		},
		"log": map[string]any{
			"level":        fc.Log.Level,
			"format":       fc.Log.Format,
			"file":         fc.Log.File,
			"also_stdout":  fc.Log.AlsoStdout,
			"max_size_mb":  fc.Log.MaxSizeMB,
			"max_backups":  fc.Log.MaxBackups,
			"max_age_days": fc.Log.MaxAgeDays,
			"compress":     fc.Log.Compress,
			"access_log":   fc.Log.AccessLog,
			"system": map[string]any{
				"enabled":        fc.Log.System.Enabled,
				"level":          fc.Log.System.Level,
				"access":         fc.Log.System.Access,
				"retention_days": fc.Log.System.RetentionDays,
				"max_rows":       fc.Log.System.MaxRows,
			},
			"archive": map[string]any{
				"enabled":        fc.Log.Archive.Enabled,
				"dir":            fc.Log.Archive.Dir,
				"compress":       fc.Log.Archive.Compress,
				"retention_days": fc.Log.Archive.RetentionDays,
				"max_total_mb":   fc.Log.Archive.MaxTotalMB,
				"quota_policy":   fc.Log.Archive.QuotaPolicy,
			},
		},
		"upgrade": map[string]any{
			"auto":          fc.Upgrade.Auto,
			"auto_optimize": fc.Upgrade.AutoOptimize,
			"vacuum":        fc.Upgrade.Vacuum,
			"program": map[string]any{
				"enabled":              fc.Upgrade.Program.Enabled,
				"repo":                 fc.Upgrade.Program.Repo,
				"channel":              fc.Upgrade.Program.Channel,
				"check_interval_hours": fc.Upgrade.Program.CheckIntervalHours,
				"max_download_mb":      fc.Upgrade.Program.MaxDownloadMB,
				"public_key":           fc.Upgrade.Program.PublicKey,
				"allow_unsigned":       fc.Upgrade.Program.AllowUnsigned,
				"token":                fc.Upgrade.Program.Token,
			},
		},
	}
}

// ensureDefaultNode 递归补齐缺失字段。已存在的字段保持原值，只补结构不覆盖用户配置。
func ensureDefaultNode(parent *yaml.Node, spec map[string]any, prefix string, report *OptimizeReport) {
	keys := make([]string, 0, len(spec))
	for key := range spec {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		full := key
		if prefix != "" {
			full = prefix + "." + key
		}
		switch value := spec[key].(type) {
		case map[string]any:
			child, err := ensureMapping(parent, key)
			if err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s 结构不正确，已跳过: %v", full, err))
				continue
			}
			ensureDefaultNode(child, value, full, report)
		case []string:
			if node, _ := mappingValue(parent, key); node != nil {
				continue
			}
			if err := setStringSequence(parent, key, value); err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s 结构不正确，已跳过: %v", full, err))
				continue
			}
			report.Changes = append(report.Changes, ConfigChange{Key: full, Action: "added", After: valueString(value)})
		default:
			if node, _ := mappingValue(parent, key); node != nil {
				continue
			}
			tag := "!!str"
			text := fmt.Sprintf("%v", value)
			switch value.(type) {
			case bool:
				tag = "!!bool"
			case int:
				tag = "!!int"
			}
			if err := setScalar(parent, key, tag, text); err != nil {
				report.Warnings = append(report.Warnings, fmt.Sprintf("%s 结构不正确，已跳过: %v", full, err))
				continue
			}
			report.Changes = append(report.Changes, ConfigChange{Key: full, Action: "added", After: displayValue(full, text)})
		}
	}
}

// normalizeConfigNode 规范化等价写法：空白、大小写、末尾斜杠与重复代理地址。
func normalizeConfigNode(root *yaml.Node, prefix string, report *OptimizeReport) {
	normalizeScalarValue(root, []string{"server"}, "addr", prefix, trimValue, report)
	normalizeScalarValue(root, []string{"server"}, "host", prefix, trimValue, report)
	normalizeScalarValue(root, []string{"security"}, "public_url", prefix, trimRightSlash, report)
	normalizeScalarValue(root, []string{"acme"}, "addr", prefix, trimValue, report)
	normalizeScalarValue(root, []string{"acme"}, "base_url", prefix, trimRightSlash, report)
	normalizeScalarValue(root, []string{"log"}, "level", prefix, lowerValue, report)
	normalizeScalarValue(root, []string{"log"}, "format", prefix, lowerValue, report)
	normalizeScalarValue(root, []string{"log", "system"}, "level", prefix, lowerValue, report)
	normalizeScalarValue(root, []string{"log", "archive"}, "quota_policy", prefix, lowerValue, report)
	normalizeScalarValue(root, []string{"plugins", "repo"}, "channel", prefix, lowerValue, report)
	normalizeScalarValue(root, []string{"upgrade", "program"}, "channel", prefix, lowerValue, report)
	normalizeScalarValue(root, []string{"upgrade", "program"}, "repo", prefix, trimValue, report)
	normalizeTrustedProxies(root, prefix, report)
}

func normalizeScalarValue(root *yaml.Node, section []string, key string, prefix string, transform func(string) string, report *OptimizeReport) {
	parent := root
	for _, item := range section {
		child, _ := mappingValue(parent, item)
		if child == nil || child.Kind != yaml.MappingNode {
			return
		}
		parent = child
	}
	node, _ := mappingValue(parent, key)
	if node == nil || node.Kind != yaml.ScalarNode || node.Tag == "!!null" {
		return
	}
	normalized := transform(node.Value)
	if normalized == node.Value {
		return
	}
	full := key
	if prefix != "" {
		full = prefix + "."
	}
	for _, item := range section {
		full += item + "."
	}
	full += key
	before := node.Value
	node.Value = normalized
	report.Changes = append(report.Changes, ConfigChange{
		Key:    full,
		Action: "normalized",
		Before: displayValue(full, before),
		After:  displayValue(full, normalized),
	})
}

func normalizeTrustedProxies(root *yaml.Node, prefix string, report *OptimizeReport) {
	server, _ := mappingValue(root, "server")
	if server == nil || server.Kind != yaml.MappingNode {
		return
	}
	node, _ := mappingValue(server, "trusted_proxies")
	if node == nil || node.Kind != yaml.SequenceNode {
		return
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		if item.Kind != yaml.ScalarNode {
			return
		}
		value := strings.TrimSpace(item.Value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	changed := len(out) != len(node.Content)
	if !changed {
		for index, item := range node.Content {
			if strings.TrimSpace(item.Value) != out[index] {
				changed = true
				break
			}
		}
	}
	if !changed {
		return
	}
	before := valueString(sequenceValues(node))
	node.Content = node.Content[:0]
	for _, value := range out {
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	key := "server.trusted_proxies"
	if prefix != "" {
		key = prefix + "." + key
	}
	report.Changes = append(report.Changes, ConfigChange{
		Key:    key,
		Action: "normalized",
		Before: before,
		After:  valueString(out),
	})
}

func collectConfigWarnings(root *yaml.Node, report *OptimizeReport) {
	knownTop := map[string]bool{
		"server": true, "data_dir": true, "database": true, "security": true,
		"acme": true, "plugins": true, "deploy_plugins": true,
		"notify_plugins": true, "log": true, "upgrade": true,
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if !knownTop[root.Content[i].Value] {
			report.Warnings = append(report.Warnings, fmt.Sprintf("发现未知配置段 %s，已保留但不会生效", root.Content[i].Value))
		}
	}

	acme, _ := mappingValue(root, "acme")
	if enabled, ok := scalarValue(acme, "enabled"); ok && enabled == "true" {
		security, _ := mappingValue(root, "security")
		if publicURL, _ := scalarValue(security, "public_url"); strings.TrimSpace(publicURL) == "" {
			report.Warnings = append(report.Warnings, "ACME 已启用但 security.public_url 为空，签发资源 URL 可能随请求 Host 变化；建议配置固定对外地址")
		}
	}
	plugins, _ := mappingValue(root, "plugins")
	repo, _ := mappingValue(plugins, "repo")
	if publicKey, _ := scalarValue(repo, "public_key"); strings.TrimSpace(publicKey) == "" {
		report.Warnings = append(report.Warnings, "plugins.repo.public_key 为空，插件仓库索引仅依赖 HTTPS 与资产 SHA-256 校验；生产环境建议配置 Ed25519 公钥")
	}
	deployPlugins, _ := mappingValue(root, "deploy_plugins")
	if enabled, ok := scalarValue(deployPlugins, "enabled"); ok && enabled == "true" {
		report.Warnings = append(report.Warnings, "deploy_plugins 已启用，外部部署插件会接触证书私钥，请只安装可信 helper")
	}
	security, _ := mappingValue(root, "security")
	for _, key := range []string{"jwt_secret", "salt"} {
		value, _ := scalarValue(security, key)
		value = strings.TrimSpace(value)
		if value != "" && len(value) < 32 {
			report.Warnings = append(report.Warnings, fmt.Sprintf("security.%s 长度不足 32 个字符，启动会被拒绝；请在 /upgrade 中重新生成前先备份", key))
		}
	}
	dataDir, _ := scalarValue(root, "data_dir")
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "data"
	}
	if _, err := os.Stat(filepath.Join(dataDir, "jwt.secret")); err == nil {
		report.Warnings = append(report.Warnings, "检测到旧版 data/jwt.secret；升级工具会在确认 config.yaml 已保存密钥后将其移入备份目录")
	}
}

func scalarValue(parent *yaml.Node, key string) (string, bool) {
	if parent == nil || parent.Kind != yaml.MappingNode {
		return "", false
	}
	node, _ := mappingValue(parent, key)
	if node == nil || node.Kind != yaml.ScalarNode {
		return "", false
	}
	return node.Value, true
}

func sequenceValues(node *yaml.Node) []string {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(node.Content))
	for _, item := range node.Content {
		out = append(out, item.Value)
	}
	return out
}

func trimValue(value string) string      { return strings.TrimSpace(value) }
func trimRightSlash(value string) string { return strings.TrimRight(strings.TrimSpace(value), "/") }
func lowerValue(value string) string     { return strings.ToLower(strings.TrimSpace(value)) }

func valueString(value []string) string {
	if len(value) == 0 {
		return "[]"
	}
	return "[" + strings.Join(value, ", ") + "]"
}

// displayValue 避免把密钥写入报告或日志。
func displayValue(key, value string) string {
	if isSecretKey(key) {
		if strings.TrimSpace(value) == "" {
			return "空"
		}
		return "已设置"
	}
	return value
}

func isSecretKey(key string) bool {
	switch key {
	case "security.jwt_secret", "security.salt":
		return true
	default:
		return strings.Contains(key, "password") || strings.Contains(key, "secret") || strings.Contains(key, "token")
	}
}
