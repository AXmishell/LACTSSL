package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/db"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newSetupConfigTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()
	conn, err := db.Open(filepath.Join(dataDir, "lactssl.db"))
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	configPath := filepath.Join(dataDir, "config.yaml")
	content := `server:
  host: "127.0.0.1"
  port: 8080
security:
  jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"
  salt: "ssssssssssssssssssssssssssssssss"
log:
  level: info
  format: text
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}
	st := store.New(conn, cipher)
	s := New(
		st,
		auth.New([]byte("jwt-secret")),
		service.New(st),
		"secret-entry",
		fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html><head></head><body>app</body></html>")}},
		nil,
	)
	s.ConfigFile = configPath
	// 该测试辅助函数用于验证白名单逻辑，不模拟 main 的一次性 token。
	s.AllowInsecureSetup = true
	return s, configPath
}

func TestSetupConfigGetAndUpdate(t *testing.T) {
	s, configPath := newSetupConfigTestServer(t)
	router := s.Router()

	get := sessionRequest(t, router, http.MethodGet, "/api/setup/config", "", nil, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("读取初始化配置失败: %d %s", get.Code, get.Body.String())
	}
	var status struct {
		Success bool `json:"success"`
		Data    struct {
			Config struct {
				Server struct {
					Host string `json:"host"`
					Port int    `json:"port"`
				} `json:"server"`
				PublicURL string `json:"public_url"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal(get.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Success || status.Data.Config.Server.Host != "127.0.0.1" || status.Data.Config.Server.Port != 8080 {
		t.Fatalf("初始化配置返回不正确: %s", get.Body.String())
	}

	update := sessionRequest(t, router, http.MethodPut, "/api/setup/config",
		`{"public_url":"https://ssl.example.com/","session_persistence":true,"acme":{"addr":":14000"},"log":{"level":"debug","format":"json","max_backups":9},"upgrade":{"program":{"channel":"beta"}}}`,
		nil, nil)
	if update.Code != http.StatusOK {
		t.Fatalf("保存初始化配置失败: %d %s", update.Code, update.Body.String())
	}
	if !strings.Contains(update.Body.String(), `"restart_required":true`) {
		t.Fatalf("保存后应提示重启: %s", update.Body.String())
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{
		`jwt_secret: "jjjjjjjjjjjjjjjjjjjjjjjjjjjjjjjj"`,
		`salt: "ssssssssssssssssssssssssssssssss"`,
		"public_url: https://ssl.example.com",
		"session_persistence: true",
		"addr: :14000",
		"level: debug",
		"format: json",
		"max_backups: 9",
		"channel: beta",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("配置未正确写入，缺少 %q:\n%s", want, content)
		}
	}
}

func TestSetupConfigRejectsInvalidAndInitializedRequests(t *testing.T) {
	s, _ := newSetupConfigTestServer(t)
	router := s.Router()

	invalid := sessionRequest(t, router, http.MethodPut, "/api/setup/config",
		`{"public_url":"https://ssl.example.com/path"}`, nil, nil)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "api.error.config_invalid") {
		t.Fatalf("非法配置应被拒绝: %d %s", invalid.Code, invalid.Body.String())
	}

	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}

	update := sessionRequest(t, router, http.MethodPut, "/api/setup/config",
		`{"public_url":"https://ssl.example.com"}`, nil, nil)
	if update.Code != http.StatusBadRequest || !strings.Contains(update.Body.String(), "api.error.already_initialized") {
		t.Fatalf("初始化后应拒绝配置写入: %d %s", update.Code, update.Body.String())
	}

	get := sessionRequest(t, router, http.MethodGet, "/api/setup/config", "", nil, nil)
	if get.Code != http.StatusBadRequest || !strings.Contains(get.Body.String(), "api.error.already_initialized") {
		t.Fatalf("初始化后应拒绝配置读取: %d %s", get.Code, get.Body.String())
	}
}

func TestSetupConfigIgnoresEnvironment(t *testing.T) {
	s, configPath := newSetupConfigTestServer(t)
	router := s.Router()
	t.Setenv("LACTSSL_LOG_LEVEL", "warn")

	update := sessionRequest(t, router, http.MethodPut, "/api/setup/config",
		`{"public_url":"https://ssl.example.com","log":{"level":"debug"}}`, nil, nil)
	if update.Code != http.StatusOK {
		t.Fatalf("保存失败: %d %s", update.Code, update.Body.String())
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "level: debug") {
		t.Fatalf("环境变量不应锁定配置文件字段: %s", raw)
	}
}

func TestSetupConfigRestartUnavailable(t *testing.T) {
	s, _ := newSetupConfigTestServer(t)
	router := s.Router()
	restart := sessionRequest(t, router, http.MethodPost, "/api/setup/config/restart", `{}`, nil, nil)
	if restart.Code != http.StatusServiceUnavailable {
		t.Fatalf("未注入 Restart 时应返回 503: %d %s", restart.Code, restart.Body.String())
	}
}
