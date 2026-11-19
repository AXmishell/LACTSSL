package dbconvert

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lactssl/internal/db"
)

func TestManagerWritesConfigAndFreezesSource(t *testing.T) {
	pgDSN := os.Getenv("LACTSSL_TEST_PG_DSN")
	if pgDSN == "" {
		t.Skip("LACTSSL_TEST_PG_DSN 未设置")
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	secret := strings.Repeat("a", 64)
	content := "data_dir: " + dir + "\n" +
		"database:\n  driver: sqlite\n  path: " + filepath.Join(dir, "source.db") + "\n" +
		"security:\n  jwt_secret: \"" + secret + "\"\n  salt: \"" + secret + "\"\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	source, st := seedSQLite(t, filepath.Join(dir, "source.db"))
	defer source.Close()
	cipher := testCipher(t)
	manager := NewManager(source, db.SQLite, filepath.Join(dir, "source.db"), "", dir, configPath, cipher, "test")
	switched := make(chan struct{}, 1)
	manager.OnSwitching = func() { switched <- struct{}{} }
	var maintenance bool
	manager.OnMaintenance = func(active bool) { maintenance = active }
	if _, err := manager.Start(StartRequest{TargetDriver: db.Postgres, TargetDSN: pgDSN, Force: true}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status := manager.CurrentStatus()
		if status.State != nil && status.State.Phase == PhaseFailed {
			t.Fatalf("转换失败: %s", status.State.Error)
		}
		if status.State != nil && status.State.Phase == PhaseSwitching {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	select {
	case <-switched:
	case <-time.After(3 * time.Second):
		t.Fatal("未触发切换回调")
	}
	if !maintenance {
		t.Fatal("转换期间应进入维护模式")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "driver: postgres") {
		t.Fatalf("配置未切换到 PostgreSQL:\n%s", data)
	}
	if !source.Gate().Frozen() {
		t.Fatal("成功后源库应保持冻结，等待重启")
	}
	source.Unfreeze()
	_ = st
	_ = configPath
}
