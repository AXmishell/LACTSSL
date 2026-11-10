package backup

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/db"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func TestExportAndStage(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "lactssl.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cipher, err := util.NewCipher([]byte("backup-test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	st := store.New(conn, cipher)
	if err := st.SetSetting("backup.marker", "keep-me"); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}

	configPath := filepath.Join(dataDir, "config.yaml")
	configContent := "security:\n  salt: test-salt\n  jwt_secret: test-jwt\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	manager := New(conn, dataDir, dbPath, configPath, "test-version")
	zipPath, filename, cleanup, err := manager.Export("")
	if err != nil {
		t.Fatalf("生成备份失败: %v", err)
	}
	defer cleanup()
	if !strings.HasSuffix(filename, ".zip") {
		t.Fatalf("备份文件名不正确: %s", filename)
	}

	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("读取备份包失败: %v", err)
	}
	defer archive.Close()
	names := map[string]bool{}
	for _, entry := range archive.File {
		names[entry.Name] = true
	}
	for _, name := range []string{DatabaseName, ConfigName, ManifestName} {
		if !names[name] {
			t.Fatalf("备份包缺少 %s", name)
		}
	}

	file, err := os.Open(zipPath)
	if err != nil {
		t.Fatalf("打开备份包失败: %v", err)
	}
	defer file.Close()
	if err := manager.Stage(file, filename, ""); err != nil {
		t.Fatalf("暂存备份失败: %v", err)
	}

	pending, err := LoadPending(dataDir)
	if err != nil {
		t.Fatalf("读取待恢复备份失败: %v", err)
	}
	if pending == nil || pending.DatabasePath == "" || pending.ConfigPath == "" {
		t.Fatalf("待恢复备份不完整: %+v", pending)
	}

	restoredDB := filepath.Join(t.TempDir(), "restored.db")
	if err := ApplyDatabase(restoredDB, pending.DatabasePath); err != nil {
		t.Fatalf("应用数据库失败: %v", err)
	}
	restoredConfig := filepath.Join(t.TempDir(), "config.yaml")
	if err := ApplyConfig(restoredConfig, pending.ConfigPath); err != nil {
		t.Fatalf("应用配置失败: %v", err)
	}

	restoredConn, err := db.Open(restoredDB)
	if err != nil {
		t.Fatalf("打开恢复后的数据库失败: %v", err)
	}
	defer restoredConn.Close()
	restoredStore := store.New(restoredConn, cipher)
	value, err := restoredStore.GetSetting("backup.marker")
	if err != nil || value != "keep-me" {
		t.Fatalf("恢复后的数据不正确: %q, %v", value, err)
	}
	rawConfig, err := os.ReadFile(restoredConfig)
	if err != nil {
		t.Fatalf("读取恢复后的配置失败: %v", err)
	}
	if string(rawConfig) != configContent {
		t.Fatalf("恢复后的配置不正确: %q", string(rawConfig))
	}

	if err := pending.Cleanup(); err != nil {
		t.Fatalf("清理待恢复备份失败: %v", err)
	}
	if pending, _ := LoadPending(dataDir); pending != nil {
		t.Fatalf("清理后仍存在待恢复备份: %+v", pending)
	}
}

func TestStageRejectsInvalidDatabase(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "lactssl.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer conn.Close()

	manager := New(conn, dataDir, dbPath, filepath.Join(dataDir, "config.yaml"), "test")
	err = manager.Stage(strings.NewReader("this is not a sqlite database"), "bad.db", "")
	if !errors.Is(err, ErrInvalidDatabase) {
		t.Fatalf("应拒绝无效数据库，实际错误: %v", err)
	}
	if pending, _ := LoadPending(dataDir); pending != nil {
		t.Fatalf("无效上传不应留下待恢复文件: %+v", pending)
	}
}

func TestEncryptedExportAndStage(t *testing.T) {
	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "lactssl.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	defer conn.Close()
	cipher, err := util.NewCipher([]byte("backup-test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	if err := store.New(conn, cipher).SetSetting("backup.marker", "encrypted"); err != nil {
		t.Fatalf("写入设置失败: %v", err)
	}
	configPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("security:\n  salt: test-salt\n  jwt_secret: test-jwt\n"), 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}

	manager := New(conn, dataDir, dbPath, configPath, "test")
	zipPath, filename, cleanup, err := manager.Export("correct-password")
	if err != nil {
		t.Fatalf("生成加密备份失败: %v", err)
	}
	defer cleanup()

	meta, payload, err := readArchiveManifest(zipPath)
	if err != nil {
		t.Fatalf("读取加密备份元数据失败: %v", err)
	}
	if meta == nil || !meta.Encrypted || meta.Salt == "" || payload == "" {
		t.Fatalf("加密元数据不完整: %+v, payload=%q", meta, payload)
	}
	archive, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("读取加密备份失败: %v", err)
	}
	for _, entry := range archive.File {
		if entry.Name == DatabaseName {
			t.Fatal("加密备份外层不应包含明文数据库")
		}
	}
	_ = archive.Close()

	stage := func(password string) error {
		file, err := os.Open(zipPath)
		if err != nil {
			t.Fatalf("打开备份失败: %v", err)
		}
		defer file.Close()
		return manager.Stage(file, filename, password)
	}
	if err := stage(""); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("缺少密码时应要求解密密码: %v", err)
	}
	if err := stage("wrong-password"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("错误密码应解密失败: %v", err)
	}
	if err := stage("correct-password"); err != nil {
		t.Fatalf("正确密码应恢复成功: %v", err)
	}
	pending, err := LoadPending(dataDir)
	if err != nil || pending == nil || pending.DatabasePath == "" {
		t.Fatalf("加密备份未生成待恢复文件: %+v, %v", pending, err)
	}
}
