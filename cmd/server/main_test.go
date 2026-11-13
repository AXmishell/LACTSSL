package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lactssl/internal/backup"
	"lactssl/internal/config"
	"lactssl/internal/db"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	return store.New(conn, cipher)
}

func TestMountACMEHandlerIncludesPKI(t *testing.T) {
	acmeHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("acme:" + r.URL.Path))
	})
	mainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("main:" + r.URL.Path))
	})
	handler := mountACMEHandler(mainHandler, acmeHandler)
	for _, path := range []string{"/acme/test/directory", "/pki/1/crl", "/pki/1/ocsp"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if !strings.HasPrefix(w.Body.String(), "acme:") {
			t.Fatalf("%s 应由 ACME handler 处理，实际响应: %s", path, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if !strings.HasPrefix(w.Body.String(), "main:") {
		t.Fatalf("普通页面应由主 handler 处理，实际响应: %s", w.Body.String())
	}
}

func TestApplyPendingRestore(t *testing.T) {
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "config.yaml")
	dbPath := filepath.Join(dataDir, "lactssl.db")

	// 当前数据库使用旧数据，待恢复数据库使用新数据。
	writeMarkerDB(t, dbPath, "old-value")
	sourceDir := t.TempDir()
	sourceDB := filepath.Join(sourceDir, "source.db")
	writeMarkerDB(t, sourceDB, "restored-value")

	pendingDir := backup.PendingDir(dataDir)
	if err := os.MkdirAll(pendingDir, 0o700); err != nil {
		t.Fatal(err)
	}
	copyBytes(t, sourceDB, filepath.Join(pendingDir, backup.DatabaseName))
	pendingConfig := "data_dir: " + dataDir + "\nsecurity:\n  salt: rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr\n  jwt_secret: jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj\n"
	copyText(t, pendingConfig, filepath.Join(pendingDir, backup.ConfigName))
	oldConfig := "data_dir: " + dataDir + "\nsecurity:\n  salt: oooooooooooooooooooooooooooooooo\n  jwt_secret: kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk\n"
	copyText(t, oldConfig, configPath)

	t.Setenv("LACTSSL_CONFIG", configPath)
	t.Setenv("LACTSSL_DATA", dataDir)

	cfg := &config.Config{DataDir: dataDir, DBPath: dbPath, File: configPath}
	restored, applied, err := applyPendingRestore(cfg)
	if err != nil {
		t.Fatalf("应用待恢复备份失败: %v", err)
	}
	if !applied {
		t.Fatal("应报告已应用恢复")
	}
	if string(restored.EncryptionKey) != "rrrrrrrrrrrrrrrrrrrrrrrrrrrrrrrr" {
		t.Fatalf("配置未从备份恢复: %q", string(restored.EncryptionKey))
	}
	if pending, _ := backup.LoadPending(dataDir); pending != nil {
		t.Fatalf("应用后应清理待恢复目录: %+v", pending)
	}

	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("打开恢复后的数据库失败: %v", err)
	}
	defer conn.Close()
	st := store.New(conn, nil)
	value, err := st.GetSetting("restore.marker")
	if err != nil || value != "restored-value" {
		t.Fatalf("数据库未从备份恢复: %q, %v", value, err)
	}
}

func writeMarkerDB(t *testing.T, path, value string) {
	t.Helper()
	conn, err := db.Open(path)
	if err != nil {
		t.Fatalf("创建测试数据库失败: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO settings(key,value) VALUES('restore.marker',?)`, value); err != nil {
		_ = conn.Close()
		t.Fatalf("写入测试数据失败: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("关闭测试数据库失败: %v", err)
	}
}

func copyBytes(t *testing.T, source, target string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v", source, err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", target, err)
	}
}

func copyText(t *testing.T, content, target string) {
	t.Helper()
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatalf("写入 %s 失败: %v", target, err)
	}
}

func TestEnsureEntryGeneratesAndPersists(t *testing.T) {
	st := newTestStore(t)

	entry, err := ensureEntry(st, "")
	if err != nil {
		t.Fatalf("生成安全入口失败: %v", err)
	}
	if len(entry) != 20 {
		t.Fatalf("安全入口格式不正确: %q", entry)
	}
	if _, err := util.ValidateEntry(entry); err != nil {
		t.Fatalf("安全入口未通过校验: %q, err=%v", entry, err)
	}
	saved, _ := st.GetSetting("security.entry")
	if saved != entry {
		t.Fatalf("安全入口未持久化: got %q want %q", saved, entry)
	}

	// 再次启动应复用已保存的入口。
	again, err := ensureEntry(st, "")
	if err != nil {
		t.Fatalf("读取安全入口失败: %v", err)
	}
	if again != entry {
		t.Fatalf("重启后安全入口发生变化: got %q want %q", again, entry)
	}
}

func TestEnsureEntryRejectsReservedName(t *testing.T) {
	st := newTestStore(t)
	if _, err := ensureEntry(st, "assets"); err == nil {
		t.Fatal("保留名称 assets 应当被拒绝")
	}
}

func TestEnsureEntryNormalizesOverride(t *testing.T) {
	st := newTestStore(t)
	entry, err := ensureEntry(st, " /my-panel/ ")
	if err != nil {
		t.Fatalf("设置安全入口失败: %v", err)
	}
	if entry != "my-panel" {
		t.Fatalf("安全入口未规范化: %q", entry)
	}
}

func TestMigrateLegacyEABSettings(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetSetting("acme.ca", "litessl"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting("acme.eab_kid", "legacy-kid"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSecret("acme.eab_hmac", "legacy-hmac"); err != nil {
		t.Fatal(err)
	}

	migrateLegacyEABSettings(st)

	kid, _ := st.GetSetting("acme.ca.litessl.eab_kid")
	if kid != "legacy-kid" {
		t.Fatalf("EAB Key ID 未迁移: %q", kid)
	}
	hmac, _ := st.GetSecret("acme.ca.litessl.eab_hmac")
	if hmac != "legacy-hmac" {
		t.Fatalf("EAB HMAC 未迁移: %q", hmac)
	}
}

func TestEnsureEntryRegeneratesInvalidStoredEntry(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetSetting("security.entry", "bad/name"); err != nil {
		t.Fatalf("写入无效安全入口失败: %v", err)
	}

	entry, err := ensureEntry(st, "")
	if err != nil {
		t.Fatalf("重新生成安全入口失败: %v", err)
	}
	if _, err := util.ValidateEntry(entry); err != nil {
		t.Fatalf("重新生成的入口仍然无效: %q, err=%v", entry, err)
	}
	saved, _ := st.GetSetting("security.entry")
	if saved != entry {
		t.Fatalf("无效入口未被覆盖: got %q want %q", saved, entry)
	}
}
