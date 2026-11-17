package upgrade

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newTestManager(t *testing.T) (*Manager, *store.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	conn, err := db.Open(filepath.Join(dir, "lactssl.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	st := store.New(conn, cipher)
	configPath := filepath.Join(dir, "config.yaml")
	secret := strings.Repeat("a", 64)
	content := "# 保留这条配置注释\n" +
		"data_dir: " + dir + "\n" +
		"security:\n" +
		"  jwt_secret: \"" + secret + "\"\n" +
		"  salt: \"" + strings.Repeat("b", 64) + "\"\n" +
		"acme:\n" +
		"  enabled: false\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	manager, err := New(Options{
		DB:           conn,
		Store:        st,
		ConfigPath:   configPath,
		DataDir:      dir,
		DBPath:       filepath.Join(dir, "lactssl.db"),
		AppVersion:   "test-version",
		Auto:         true,
		AutoOptimize: true,
		Vacuum:       true,
	})
	if err != nil {
		t.Fatalf("创建升级管理器失败: %v", err)
	}
	return manager, st, dir, configPath
}

func TestAutoMigrateMigratesLegacyDataAndConfig(t *testing.T) {
	manager, st, dir, configPath := newTestManager(t)
	ctx := context.Background()

	if err := st.SetSetting("acme.ca", "litessl"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("acme.eab_kid", "legacy-kid"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecret("acme.eab_hmac", "legacy-hmac"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("gotify.url", "https://gotify.example"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("security.verify_mode", "password_passkey"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("security.step_up.task_write", "false"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO users(email,password_hash,totp_secret,totp_enabled,token_version,created_at,updated_at)
		VALUES('legacy@example.com','hash','PLAINTOTP',1,1,datetime('now'),datetime('now'))`); err != nil {
		t.Fatal(err)
	}

	if err := manager.AutoMigrate(ctx); err != nil {
		t.Fatalf("自动迁移失败: %v", err)
	}

	step := manager.stepStatuses(CurrentVersion)
	for _, item := range step {
		if item.Status == "failed" {
			t.Fatalf("步骤 %s 失败: %s", item.ID, item.Detail)
		}
	}

	version, err := manager.storedVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != CurrentVersion {
		t.Fatalf("数据格式版本未推进: got %d want %d", version, CurrentVersion)
	}
	if kid, _ := st.GetSetting("acme.ca.litessl.eab_kid"); kid != "legacy-kid" {
		t.Fatalf("EAB Key ID 未迁移: %q", kid)
	}
	if hmac, _ := st.GetSecret("acme.ca.litessl.eab_hmac"); hmac != "legacy-hmac" {
		t.Fatalf("EAB HMAC 未迁移: %q", hmac)
	}
	if value, _ := st.GetSetting("acme.eab_kid"); value != "" {
		t.Fatalf("旧 EAB 设置未清理: %q", value)
	}
	if value, _ := st.GetSetting("gotify.url"); value != "" {
		t.Fatalf("旧通知设置未清理: %q", value)
	}
	if mode, _ := st.GetSetting("security.verify_mode"); mode != "" {
		t.Fatalf("旧验证模式设置未清理: %q", mode)
	}
	if value, _ := st.GetSetting("security.step_up.task_write"); value != "" {
		t.Fatalf("旧二次验证开关未清理: %q", value)
	}
	var secret string
	if err := st.DB.QueryRow(`SELECT totp_secret FROM users WHERE email='legacy@example.com'`).Scan(&secret); err != nil {
		t.Fatal(err)
	}
	if !util.IsEncrypted(secret) {
		t.Fatalf("TOTP 密钥仍未加密: %q", secret)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "# 保留这条配置注释") {
		t.Fatal("配置优化丢失了原有注释")
	}
	if !strings.Contains(text, "upgrade:") || !strings.Contains(text, "auto_optimize:") {
		t.Fatal("配置优化未补齐 upgrade 段")
	}
	if !strings.Contains(text, "notify_plugins:") {
		t.Fatal("配置优化未补齐新版本配置段")
	}

	for _, index := range []string{"idx_certificates_status_not_after", "idx_tasks_enabled_next_run"} {
		var count int
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("热路径索引 %s 未创建", index)
		}
	}

	status, err := manager.Status(ctx)
	if err != nil {
		t.Fatalf("读取升级状态失败: %v", err)
	}
	if status.NeedsUpgrade {
		t.Fatal("迁移完成后仍显示需要升级")
	}
	if status.Database.Integrity != "ok" {
		t.Fatalf("数据库完整性检查未通过: %s", status.Database.Integrity)
	}
	if status.LastRun == nil || status.LastRun.ToVersion != CurrentVersion {
		t.Fatalf("最近一次升级记录不正确: %+v", status.LastRun)
	}
	if _, err := os.Stat(filepath.Join(dir, "upgrade-backups")); err != nil {
		t.Fatalf("迁移前备份目录不存在: %v", err)
	}
}

func TestLegacySecretFileMovedToBackup(t *testing.T) {
	manager, _, dir, configPath := newTestManager(t)
	legacy := strings.Repeat("a", 64)
	legacyPath := filepath.Join(dir, "jwt.secret")
	if err := os.WriteFile(legacyPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Optimize(context.Background(), OptimizeOptions{Vacuum: true, Analyze: true, Checkpoint: true, CheckIntegrity: true}); err != nil {
		t.Fatalf("数据库优化失败: %v", err)
	}
	if _, err := manager.Run(context.Background(), RunOptions{Migrate: true, Config: true, Optimize: true, Source: "test"}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("旧版密钥文件仍存在: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "upgrade-backups", "legacy-*", "jwt.secret"))
	if len(matches) == 0 {
		t.Fatal("旧版密钥文件未进入备份目录")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "jwt_secret") {
		t.Fatal("配置中的 jwt_secret 丢失")
	}
}

func TestOptimizeReclaimsSpace(t *testing.T) {
	manager, st, _, _ := newTestManager(t)
	if _, err := st.DB.Exec(`CREATE TABLE IF NOT EXISTS bloat_test(id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2000; i++ {
		if _, err := st.DB.Exec(`INSERT INTO bloat_test(value) VALUES(?)`, strings.Repeat("x", 512)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB.Exec(`DELETE FROM bloat_test`); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Optimize(context.Background(), OptimizeOptions{Vacuum: true, Analyze: true, Checkpoint: true, CheckIntegrity: true})
	if err != nil {
		t.Fatalf("优化失败: %v", err)
	}
	if result.After.Integrity != "ok" {
		t.Fatalf("优化后完整性检查失败: %s", result.After.Integrity)
	}
	if len(result.Actions) == 0 {
		t.Fatal("优化没有产生任何动作")
	}
	if result.ReclaimedBytes <= 0 {
		t.Fatalf("VACUUM 未回收空间: before=%+v after=%+v", result.Before, result.After)
	}
}

func TestConfigOptimizeIsIdempotent(t *testing.T) {
	_, _, _, configPath := newTestManager(t)
	first, err := config.OptimizeFile(configPath, true)
	if err != nil {
		t.Fatalf("首次配置优化失败: %v", err)
	}
	if !first.Changed {
		t.Fatal("首次配置优化应产生变更")
	}
	second, err := config.OptimizeFile(configPath, true)
	if err != nil {
		t.Fatalf("二次配置优化失败: %v", err)
	}
	if second.Changed {
		t.Fatalf("配置优化不幂等，仍产生变更: %+v", second.Changes)
	}
}
