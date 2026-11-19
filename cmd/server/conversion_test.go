package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/dbconvert"
	"lactssl/internal/util"
)

func writeConversionConfig(t *testing.T, path, dataDir, driver, sqlitePath string) {
	t.Helper()
	secret := strings.Repeat("a", 64)
	content := "data_dir: " + dataDir + "\n" +
		"database:\n  driver: " + driver + "\n  path: " + sqlitePath + "\n  dsn: \"\"\n" +
		"security:\n  jwt_secret: \"" + secret + "\"\n  salt: \"" + secret + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackConversionRestoresConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	sourcePath := filepath.Join(dir, "source.db")
	writeConversionConfig(t, configPath, dir, "sqlite", sourcePath)
	backupPath := filepath.Join(dir, "config.before.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// 模拟切换后配置指向损坏的目标。
	writeConversionConfig(t, configPath, dir, "sqlite", filepath.Join(dir, "target.db"))
	cfg, err := config.LoadFrom(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state := &dbconvert.State{Phase: dbconvert.PhaseSwitching, ConfigBackup: backupPath}
	restored, err := rollbackConversion(cfg, state, os.ErrInvalid)
	if err != nil {
		t.Fatal(err)
	}
	if restored.DBPath != sourcePath {
		t.Fatalf("回滚后路径 %s，期望 %s", restored.DBPath, sourcePath)
	}
	if state.Phase != dbconvert.PhaseRolledBack {
		t.Fatalf("状态未标记回滚: %s", state.Phase)
	}
}

func TestFinalizeConversionRollsBackWhenTargetInvalid(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	sourcePath := filepath.Join(dir, "source.db")
	writeConversionConfig(t, configPath, dir, "sqlite", sourcePath)
	source, err := db.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	backupPath := filepath.Join(dir, "config.before.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	targetPath := filepath.Join(dir, "target.db")
	writeConversionConfig(t, configPath, dir, "sqlite", targetPath)
	cfg, err := config.LoadFrom(configPath)
	if err != nil {
		t.Fatal(err)
	}
	// 目标库没有业务表，VerifyStarted 必须失败并触发回滚。
	target, err := db.OpenWithOptions(db.Options{Driver: db.SQLite, Path: targetPath, Migrate: false})
	if err != nil {
		t.Fatal(err)
	}
	cipher, err := util.NewCipher([]byte(strings.Repeat("a", 64)))
	if err != nil {
		t.Fatal(err)
	}
	state := &dbconvert.State{Phase: dbconvert.PhaseSwitching, ConfigBackup: backupPath}
	restored, reopened, err := finalizeConversion(cfg, target, state, cipher)
	if err != nil {
		t.Fatal(err)
	}
	if reopened == nil {
		t.Fatal("回滚后应重新打开源库")
	}
	defer reopened.Close()
	if restored.DBPath != sourcePath {
		t.Fatalf("回滚后路径 %s，期望 %s", restored.DBPath, sourcePath)
	}
	if state.Phase != dbconvert.PhaseRolledBack {
		t.Fatalf("状态未标记回滚: %s", state.Phase)
	}
}
