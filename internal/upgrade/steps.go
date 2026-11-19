package upgrade

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"lactssl/internal/db"
	"lactssl/internal/service"
)

// StepResult 是单个迁移步骤的执行结果。
type StepResult struct {
	Changed bool
	Skipped bool
	Details []string
}

// Step 描述一个可重复执行的迁移步骤。
type Step struct {
	ID       string
	Version  int
	TitleKey string
	DescKey  string
	Run      func(context.Context, *Manager) (StepResult, error)
}

// steps 是升级工具的迁移计划。顺序即执行顺序，新增步骤必须追加到末尾。
var steps = []Step{
	{ID: "legacy_secrets", Version: 1, TitleKey: "upgrade.step.secrets.title", DescKey: "upgrade.step.secrets.desc", Run: stepLegacySecrets},
	{ID: "legacy_crypto", Version: 1, TitleKey: "upgrade.step.crypto.title", DescKey: "upgrade.step.crypto.desc", Run: stepLegacyCrypto},
	{ID: "legacy_eab", Version: 2, TitleKey: "upgrade.step.eab.title", DescKey: "upgrade.step.eab.desc", Run: stepLegacyEAB},
	{ID: "deploy_config_split", Version: 2, TitleKey: "upgrade.step.deployConfig.title", DescKey: "upgrade.step.deployConfig.desc", Run: stepDeployConfigSplit},
	{ID: "notify_channels", Version: 2, TitleKey: "upgrade.step.notify.title", DescKey: "upgrade.step.notify.desc", Run: stepNotifyChannels},
	{ID: "deploy_targets", Version: 3, TitleKey: "upgrade.step.deployTargets.title", DescKey: "upgrade.step.deployTargets.desc", Run: stepDeployTargets},
	{ID: "plugin_settings", Version: 3, TitleKey: "upgrade.step.plugins.title", DescKey: "upgrade.step.plugins.desc", Run: stepPluginSettings},
	{ID: "settings_cleanup", Version: 3, TitleKey: "upgrade.step.settings.title", DescKey: "upgrade.step.settings.desc", Run: stepSettingsCleanup},
	{ID: "runtime_cleanup", Version: 3, TitleKey: "upgrade.step.runtime.title", DescKey: "upgrade.step.runtime.desc", Run: stepRuntimeCleanup},
	{ID: "db_indexes", Version: 3, TitleKey: "upgrade.step.indexes.title", DescKey: "upgrade.step.indexes.desc", Run: stepDatabaseIndexes},
	{ID: "logs_unified", Version: 4, TitleKey: "upgrade.step.logsUnified.title", DescKey: "upgrade.step.logsUnified.desc", Run: stepLogsUnified},
}

// stepLegacySecrets 清理旧版 data/jwt.secret。
// config.Load 为了兼容旧部署仍会在密钥为空时读取该文件；当 config.yaml 已保存
// jwt_secret 与 salt 后，这里把遗留文件移入升级备份目录并从数据目录移除。
func stepLegacySecrets(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	legacyPath := m.legacySecretFile
	if legacyPath == "" {
		legacyPath = filepath.Join(m.dataDir, "jwt.secret")
	}
	data, err := os.ReadFile(legacyPath)
	if os.IsNotExist(err) {
		result.Skipped = true
		result.Details = append(result.Details, "未发现旧版 jwt.secret")
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("读取旧版密钥文件失败: %w", err)
	}
	if m.configPath == "" {
		result.Skipped = true
		result.Details = append(result.Details, "未指定配置文件，保留旧版密钥文件")
		return result, nil
	}
	configData, err := os.ReadFile(m.configPath)
	if err != nil {
		return result, fmt.Errorf("读取配置文件失败: %w", err)
	}
	var doc struct {
		Security struct {
			JWTSecret string `yaml:"jwt_secret"`
			Salt      string `yaml:"salt"`
		} `yaml:"security"`
	}
	if err := yaml.Unmarshal(configData, &doc); err != nil {
		return result, fmt.Errorf("解析配置文件失败: %w", err)
	}
	jwtSecret := strings.TrimSpace(doc.Security.JWTSecret)
	salt := strings.TrimSpace(doc.Security.Salt)
	if jwtSecret == "" || salt == "" {
		result.Skipped = true
		result.Details = append(result.Details, "config.yaml 尚未保存完整密钥，保留旧版文件作为回退")
		return result, nil
	}
	legacy := strings.TrimSpace(string(data))
	if legacy != "" && legacy != jwtSecret && legacy != salt {
		result.Skipped = true
		result.Details = append(result.Details, "旧版密钥与当前配置不一致，保留文件以便人工核对")
		return result, nil
	}
	baseDir := filepath.Join(m.dataDir, "upgrade-backups")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return result, fmt.Errorf("创建密钥备份目录失败: %w", err)
	}
	backupDir, err := os.MkdirTemp(baseDir, "legacy-"+time.Now().UTC().Format("20060102-150405")+"-")
	if err != nil {
		return result, fmt.Errorf("创建密钥备份目录失败: %w", err)
	}
	target := filepath.Join(backupDir, "jwt.secret")
	if err := os.Rename(legacyPath, target); err != nil {
		// 跨文件系统或权限异常时退化为复制后删除。
		if writeErr := os.WriteFile(target, data, 0o600); writeErr != nil {
			return result, fmt.Errorf("备份旧版密钥文件失败: %w", err)
		}
		if removeErr := os.Remove(legacyPath); removeErr != nil {
			return result, fmt.Errorf("删除旧版密钥文件失败: %w", removeErr)
		}
	}
	_ = os.Chmod(target, 0o600)
	result.Changed = true
	result.Details = append(result.Details, "旧版 data/jwt.secret 已移入 "+target)
	return result, nil
}

// stepLegacyCrypto 把旧版本明文保存的 TOTP 密钥与证书记录私钥加密。
func stepLegacyCrypto(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	totpBefore, err := countPlaintext(m.db, "users", "totp_secret")
	if err != nil {
		return result, err
	}
	keyBefore, err := countPlaintext(m.db, "certificates", "key_pem")
	if err != nil {
		return result, err
	}
	if totpBefore == 0 && keyBefore == 0 {
		result.Skipped = true
		result.Details = append(result.Details, "没有发现明文敏感字段")
		return result, nil
	}
	if err := m.store.MigrateTOTPSecrets(); err != nil {
		return result, fmt.Errorf("迁移 TOTP 密钥失败: %w", err)
	}
	if err := m.store.MigrateCertificateKeys(); err != nil {
		return result, fmt.Errorf("迁移证书私钥失败: %w", err)
	}
	result.Changed = true
	result.Details = append(result.Details, fmt.Sprintf("已加密 %d 个 TOTP 密钥、%d 个证书私钥", totpBefore, keyBefore))
	return result, nil
}

func countPlaintext(db *db.Handle, table, column string) (int64, error) {
	// table/column 只来自本包内的固定常量，不接收用户输入。
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE %s<>'' AND %s NOT LIKE 'enc:v1:%%'`, table, column, column)
	var count int64
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return 0, fmt.Errorf("统计 %s.%s 明文记录失败: %w", table, column, err)
	}
	return count, nil
}

// stepLegacyEAB 把旧版全局 EAB 凭据迁移到默认 CA 的独立配置。
func stepLegacyEAB(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	legacyKid, _ := m.store.GetSetting("acme.eab_kid")
	legacyHMAC, _ := m.store.GetSecret("acme.eab_hmac")
	if strings.TrimSpace(legacyKid) == "" && strings.TrimSpace(legacyHMAC) == "" {
		result.Skipped = true
		result.Details = append(result.Details, "没有旧版全局 EAB 凭据")
		return result, nil
	}
	caValue, _ := m.store.GetSetting("acme.ca")
	if strings.TrimSpace(caValue) == "" {
		caValue = "letsencrypt"
	}
	ca, found := service.CAInfo(caValue)
	if !found {
		result.Skipped = true
		result.Details = append(result.Details, "默认 CA 无法识别，保留旧版 EAB 凭据")
		return result, nil
	}
	conflict := false
	if kid := strings.TrimSpace(legacyKid); kid != "" {
		key := service.CAEABKidSettingKey(ca.Value)
		if existing, _ := m.store.GetSetting(key); strings.TrimSpace(existing) == "" {
			if err := m.store.SetSetting(key, kid); err != nil {
				return result, err
			}
			result.Changed = true
		} else if existing != kid {
			conflict = true
		}
	}
	if hmac := strings.TrimSpace(legacyHMAC); hmac != "" {
		key := service.CAEABHMACSettingKey(ca.Value)
		if existing, _ := m.store.GetSecret(key); strings.TrimSpace(existing) == "" {
			if err := m.store.SetSecret(key, hmac); err != nil {
				return result, err
			}
			result.Changed = true
		} else if existing != hmac {
			conflict = true
		}
	}
	if conflict {
		result.Details = append(result.Details, "默认 CA 已有不同的 EAB 配置，保留旧版全局凭据以便核对")
		return result, nil
	}
	if err := m.store.DeleteSetting("acme.eab_kid"); err != nil {
		return result, err
	}
	if err := m.store.DeleteSetting("acme.eab_hmac"); err != nil {
		return result, err
	}
	result.Changed = true
	result.Details = append(result.Details, "旧版全局 EAB 凭据已迁移到 "+ca.Value)
	return result, nil
}

func stepDeployConfigSplit(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	if value, _ := m.store.GetSetting("migration.deploy_config_split"); value == "1" {
		result.Skipped = true
		result.Details = append(result.Details, "部署参数拆分已迁移")
		return result, nil
	}
	if err := m.service.MigrateDeployConfigSplit(); err != nil {
		return result, err
	}
	result.Changed = true
	result.Details = append(result.Details, "旧部署凭据中的路径/站点参数已拆分到任务流程节点")
	return result, nil
}

func stepNotifyChannels(ctx context.Context, m *Manager) (StepResult, error) {
	before, _ := m.store.GetSetting("notify.channels")
	if err := m.service.MigrateNotifyChannels(); err != nil {
		return StepResult{}, err
	}
	after, _ := m.store.GetSetting("notify.channels")
	result := StepResult{}
	if before == after {
		result.Skipped = true
		result.Details = append(result.Details, "通知渠道无需迁移")
		return result, nil
	}
	result.Changed = true
	result.Details = append(result.Details, "旧版 Gotify / Server酱 配置已迁移为通用通知渠道")
	return result, nil
}

func stepDeployTargets(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	if value, _ := m.store.GetSetting("migration.deploy_target_merge"); value == "1" {
		result.Skipped = true
		result.Details = append(result.Details, "部署目标合并已迁移")
		return result, nil
	}
	if err := m.service.MigrateDeployTargets(); err != nil {
		return result, err
	}
	if value, _ := m.store.GetSetting("migration.deploy_target_merge"); value != "1" {
		// 合并目标依赖对应的部署 helper。helper 尚未安装时保留旧类型，
		// 主程序在插件发现完成后会再次调用同一幂等迁移。
		result.Skipped = true
		result.Details = append(result.Details, "等待部署插件 helper 可用后继续迁移")
		return result, nil
	}
	result.Changed = true
	result.Details = append(result.Details, "已合并的部署目标已更新为规范 ID，并保留网站/面板与 CDN/Kodo 选择")
	return result, nil
}

// stepPluginSettings 初始化插件启用白名单并清理旧版停用列表。
// 与接口层使用相同的默认值，保证升级后内置服务商/部署目标/通知渠道不会消失。
func stepPluginSettings(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	changed := false

	dnsDefaults := map[string]bool{}
	for _, id := range service.BuiltinDNSProviderIDs() {
		dnsDefaults[strings.ToLower(strings.TrimSpace(id))] = true
	}
	if legacy, _ := m.store.GetSetting("plugins.dns.disabled_providers"); strings.TrimSpace(legacy) != "" {
		var disabled []string
		if err := json.Unmarshal([]byte(legacy), &disabled); err == nil {
			for _, id := range disabled {
				delete(dnsDefaults, strings.ToLower(strings.TrimSpace(id)))
			}
		}
		if err := m.store.SetSetting("plugins.dns.enabled_providers", jsonList(dnsDefaults)); err == nil {
			changed = true
		}
		if err := m.store.DeleteSetting("plugins.dns.disabled_providers"); err != nil {
			return result, err
		}
		result.Details = append(result.Details, "旧版 DNS 服务商停用列表已转换为启用白名单")
	} else if raw, _ := m.store.GetSetting("plugins.dns.enabled_providers"); strings.TrimSpace(raw) == "" {
		if err := m.store.SetSetting("plugins.dns.enabled_providers", jsonList(dnsDefaults)); err != nil {
			return result, err
		}
		changed = true
		result.Details = append(result.Details, "已初始化 DNS 插件启用白名单")
	}

	deployDefaults := map[string]bool{}
	for _, id := range service.DefaultPluginDeployIDs() {
		deployDefaults[id] = true
	}
	if raw, _ := m.store.GetSetting("plugins.deploy.enabled_deployers"); strings.TrimSpace(raw) == "" {
		if err := m.store.SetSetting("plugins.deploy.enabled_deployers", jsonList(deployDefaults)); err != nil {
			return result, err
		}
		changed = true
		result.Details = append(result.Details, "已初始化部署插件启用白名单")
	} else {
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err == nil {
			set := map[string]bool{}
			for _, id := range ids {
				set[strings.ToLower(strings.TrimSpace(id))] = true
			}
			promoted := false
			for legacy, canonical := range service.DeprecatedDeployTypeCanonical {
				if set[legacy] {
					delete(set, legacy)
					set[canonical] = true
					promoted = true
				}
			}
			if promoted {
				if err := m.store.SetSetting("plugins.deploy.enabled_deployers", jsonList(set)); err != nil {
					return result, err
				}
				changed = true
				result.Details = append(result.Details, "旧部署目标白名单已更新为规范 ID")
			}
		}
	}

	notifyDefaults := map[string]bool{}
	for _, id := range service.DefaultNotifyChannelIDs() {
		notifyDefaults[strings.ToLower(strings.TrimSpace(id))] = true
	}
	if raw, _ := m.store.GetSetting("plugins.notify.enabled_channels"); strings.TrimSpace(raw) == "" {
		if err := m.store.SetSetting("plugins.notify.enabled_channels", jsonList(notifyDefaults)); err != nil {
			return result, err
		}
		changed = true
		result.Details = append(result.Details, "已初始化通知插件渠道白名单")
	}

	result.Changed = changed
	if !changed {
		result.Skipped = true
		if len(result.Details) == 0 {
			result.Details = append(result.Details, "插件启用白名单已是最新")
		}
	}
	return result, nil
}

func jsonList(set map[string]bool) string {
	ids := make([]string, 0, len(set))
	for id, enabled := range set {
		if enabled && id != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	data, _ := json.Marshal(ids)
	return string(data)
}

// stepSettingsCleanup 删除已迁移完成的旧设置键，并修正旧验证模式。
func stepSettingsCleanup(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{}
	legacyKeys := []string{
		"gotify.url", "gotify.token", "gotify.priority", "gotify.enabled",
		"serverchan.sendkey", "serverchan.version", "serverchan.enabled",
		"plugins.dns.disabled_providers",
	}
	removed := 0
	for _, key := range legacyKeys {
		if value, _ := m.store.GetSetting(key); strings.TrimSpace(value) != "" {
			if err := m.store.DeleteSetting(key); err != nil {
				return result, err
			}
			removed++
		}
	}
	if mode, _ := m.store.GetSetting("security.verify_mode"); mode != "" {
		_ = m.store.DeleteSetting("security.verify_mode")
		removed++
	}
	for _, action := range []string{
		"deploy_execute", "task_write", "account_write", "settings_write",
		"notification_test", "acme_write", "private_ca_write", "certificate_key_export",
		"certificate_write", "monitor_write",
	} {
		key := "security.step_up." + action
		if value, _ := m.store.GetSetting(key); strings.TrimSpace(value) != "" {
			_ = m.store.DeleteSetting(key)
			removed++
		}
	}
	result.Changed = removed > 0
	if removed == 0 {
		result.Skipped = true
		result.Details = append(result.Details, "没有需要清理的旧设置")
	} else {
		result.Details = append(result.Details, fmt.Sprintf("已清理 %d 个历史设置键", removed))
	}
	return result, nil
}

// stepRuntimeCleanup 在跨版本升级后清空登录会话并恢复中断任务。
// 这会强制管理员重新登录，避免旧版本令牌或旧 schema 下的会话继续生效。
func stepRuntimeCleanup(ctx context.Context, m *Manager) (StepResult, error) {
	result := StepResult{Changed: true}
	if err := m.store.DeleteAllSessions(); err != nil {
		return result, fmt.Errorf("清理历史会话失败: %w", err)
	}
	m.store.ResetRunningTasks()
	m.store.ResetProcessingACMEChallenges()
	if err := m.store.Maintenance(); err != nil {
		result.Details = append(result.Details, "基础维护部分失败: "+err.Error())
	}
	result.Details = append(result.Details, "已清空旧登录会话、重排中断任务并清理过期记录")
	return result, nil
}

func stepDatabaseIndexes(ctx context.Context, m *Manager) (StepResult, error) {
	created, err := m.EnsureIndexes(ctx)
	if err != nil {
		return StepResult{}, err
	}
	result := StepResult{Changed: len(created) > 0}
	if len(created) == 0 {
		result.Skipped = true
		result.Details = append(result.Details, "热路径索引已存在")
		return result, nil
	}
	result.Details = append(result.Details, "已补充索引: "+strings.Join(created, ", "))
	return result, nil
}

// logsUnifiedSetting 标记 system_logs → logs 的一次性复制已经完成。
const logsUnifiedSetting = "upgrade.logs_unified"

// stepLogsUnified 把历史 system_logs 记录复制到统一 logs 表。
// 旧表保留用于回滚；复制与标记写入同一个事务，重复执行不会产生重复记录。
func stepLogsUnified(ctx context.Context, m *Manager) (StepResult, error) {
	marker, err := m.store.GetSetting(logsUnifiedSetting)
	if err != nil {
		return StepResult{}, err
	}
	if marker == "done" {
		return StepResult{Skipped: true, Details: []string{"系统日志已合并"}}, nil
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return StepResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// 旧业务日志没有 ref_type；按既有 source+ref_id 语义回填，便于后续按对象关联。
	if _, err := tx.ExecContext(ctx, `UPDATE logs SET category='operation' WHERE category='' OR category IS NULL`); err != nil {
		return StepResult{}, fmt.Errorf("归一日志分类失败: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE logs SET ref_type=source WHERE category='operation' AND ref_id<>0 AND ref_type=''`); err != nil {
		return StepResult{}, fmt.Errorf("回填日志关联字段失败: %w", err)
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO logs(category,source,ref_type,ref_id,level,message,fields,request_id,created_at)
		SELECT 'runtime',
		       CASE WHEN TRIM(component)='' THEN 'app' ELSE TRIM(component) END,
		       '', 0,
		       CASE LOWER(TRIM(level))
		            WHEN 'warning' THEN 'warn'
		            WHEN '' THEN 'info'
		            ELSE LOWER(TRIM(level)) END,
		       message, fields, '', created_at
		FROM system_logs`)
	if err != nil {
		return StepResult{}, fmt.Errorf("复制系统日志失败: %w", err)
	}
	copied, _ := result.RowsAffected()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO settings(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		logsUnifiedSetting, "done"); err != nil {
		return StepResult{}, fmt.Errorf("写入日志合并标记失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StepResult{}, fmt.Errorf("提交日志合并失败: %w", err)
	}

	step := StepResult{Changed: copied > 0}
	if copied == 0 {
		step.Skipped = true
	}
	step.Details = append(step.Details, fmt.Sprintf("迁移系统日志 %d 条", copied))
	return step, nil
}
