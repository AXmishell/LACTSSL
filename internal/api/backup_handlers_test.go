package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"lactssl/internal/auth"
	"lactssl/internal/backup"
	"lactssl/internal/db"
	"lactssl/internal/service"
	"lactssl/internal/store"
	"lactssl/internal/util"
)

func newBackupTestServer(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dataDir := t.TempDir()
	dbPath := filepath.Join(dataDir, "lactssl.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	cipher, err := util.NewCipher([]byte("test-key"))
	if err != nil {
		t.Fatalf("初始化加密组件失败: %v", err)
	}
	configPath := filepath.Join(dataDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("security:\n  salt: 0123456789abcdef0123456789abcdef\n  jwt_secret: fedcba9876543210fedcba9876543210\n"), 0o600); err != nil {
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
	s.Backup = backup.New(conn, dataDir, dbPath, configPath, "test")
	s.AllowInsecureSetup = true
	return s.Router(), dataDir
}

func loginBackupTestNoReauth(t *testing.T, router *gin.Engine) *http.Cookie {
	t.Helper()
	setup := sessionRequest(t, router, http.MethodPost, "/api/setup",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if setup.Code != http.StatusOK {
		t.Fatalf("初始化失败: %d %s", setup.Code, setup.Body.String())
	}
	return findCookie(t, setup.Result().Cookies(), sessionCookieName)
}

func loginBackupTest(t *testing.T, router *gin.Engine) *http.Cookie {
	t.Helper()
	session := loginBackupTestNoReauth(t, router)
	return session
}

func TestBackupDownloadWithSession(t *testing.T) {
	router, _ := newBackupTestServer(t)
	session := loginBackupTest(t, router)
	path := "/api/settings/backup/download"

	download := sessionRequest(t, router, http.MethodPost, path,
		`{"password":"backup-secret","account_password":"password1234"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1"})
	if download.Code != http.StatusOK {
		t.Fatalf("登录会话应允许下载备份: %d %s", download.Code, download.Body.String())
	}
}

func TestBackupDownloadRequiresAccountReauth(t *testing.T) {
	router, _ := newBackupTestServer(t)
	session := loginBackupTest(t, router)
	headers := map[string]string{csrfHeaderName: "1"}

	missing := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret"}`, []*http.Cookie{session}, headers)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("缺少账户再认证应拒绝下载备份: %d %s", missing.Code, missing.Body.String())
	}

	wrong := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret","account_password":"wrong-password"}`, []*http.Cookie{session}, headers)
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("错误账户密码应拒绝下载备份: %d %s", wrong.Code, wrong.Body.String())
	}
}

func TestBackupInfoAndDownload(t *testing.T) {
	router, _ := newBackupTestServer(t)
	session := loginBackupTest(t, router)

	info := sessionRequest(t, router, http.MethodGet, "/api/settings/backup", "", []*http.Cookie{session}, nil)
	if info.Code != http.StatusOK {
		t.Fatalf("读取备份信息失败: %d %s", info.Code, info.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			DatabaseSize   int64 `json:"database_size"`
			ConfigIncluded bool  `json:"config_included"`
		} `json:"data"`
	}
	if err := json.Unmarshal(info.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析备份信息失败: %v", err)
	}
	if !body.Success || body.Data.DatabaseSize <= 0 || !body.Data.ConfigIncluded {
		t.Fatalf("备份信息不正确: %s", info.Body.String())
	}
	headers := map[string]string{csrfHeaderName: "1"}
	missingPassword := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{}`, []*http.Cookie{session}, headers)
	if missingPassword.Code != http.StatusBadRequest || !strings.Contains(missingPassword.Body.String(), "api.error.backup_password_required") {
		t.Fatalf("未设置备份密码时应拒绝下载: %d %s", missingPassword.Code, missingPassword.Body.String())
	}

	download := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret","account_password":"password1234"}`, []*http.Cookie{session}, headers)
	if download.Code != http.StatusOK {
		t.Fatalf("下载备份失败: %d %s", download.Code, download.Body.String())
	}
	if contentType := download.Header().Get("Content-Type"); !strings.Contains(contentType, "zip") {
		t.Fatalf("备份应为 ZIP: %s", contentType)
	}
	if !bytes.HasPrefix(download.Body.Bytes(), []byte("PK\x03\x04")) {
		t.Fatal("备份内容不是 ZIP 文件")
	}
}

func TestRestoreBackupAndStagesUpload(t *testing.T) {
	router, dataDir := newBackupTestServer(t)
	session := loginBackupTest(t, router)
	download := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret","account_password":"password1234"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1"})
	if download.Code != http.StatusOK {
		t.Fatalf("下载备份失败: %d", download.Code)
	}

	path := "/api/settings/backup/restore"
	okResponse := multipartBackupRequest(t, router, path, session, download.Body.Bytes(), "backup-secret")
	if okResponse.Code != http.StatusOK {
		t.Fatalf("恢复备份失败: %d %s", okResponse.Code, okResponse.Body.String())
	}
	pending, err := backup.LoadPending(dataDir)
	if err != nil || pending == nil {
		t.Fatalf("恢复请求应写入待恢复文件: %+v, %v", pending, err)
	}
}

func multipartBackupRequest(t *testing.T, router *gin.Engine, path string, session *http.Cookie, data []byte, backupPassword string, accountPassword ...string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "backup.zip")
	if err != nil {
		t.Fatalf("创建上传表单失败: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("写入上传内容失败: %v", err)
	}
	if err := writer.WriteField("backup_password", backupPassword); err != nil {
		t.Fatalf("写入备份密码字段失败: %v", err)
	}
	if session != nil {
		password := "password1234"
		if len(accountPassword) > 0 {
			password = accountPassword[0]
		}
		if err := writer.WriteField("account_password", password); err != nil {
			t.Fatalf("写入账户密码字段失败: %v", err)
		}
	}
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set(csrfHeaderName, "1")
	req.RemoteAddr = "192.0.2.30:1234"
	if session != nil {
		req.AddCookie(session)
		password := "password1234"
		if len(accountPassword) > 0 {
			password = accountPassword[0]
		}
		req.Header.Set(reauthPasswordHeader, password)
	}
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "1"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestRestoreBackupRequiresAccountReauth(t *testing.T) {
	router, _ := newBackupTestServer(t)
	session := loginBackupTest(t, router)
	download := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret","account_password":"password1234"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1"})
	if download.Code != http.StatusOK {
		t.Fatalf("下载备份失败: %d %s", download.Code, download.Body.String())
	}

	// 下载成功会建立 5 分钟会话级再认证有效期；恢复测试必须使用一个
	// 未提权的新登录会话，才能验证错误密码仍会被拒绝。
	login := sessionRequest(t, router, http.MethodPost, "/api/auth/login",
		`{"email":"admin@example.com","password":"password1234"}`, nil, nil)
	if login.Code != http.StatusOK {
		t.Fatalf("重新登录失败: %d %s", login.Code, login.Body.String())
	}
	freshSession := findCookie(t, login.Result().Cookies(), sessionCookieName)

	wrong := multipartBackupRequest(t, router, "/api/settings/backup/restore", freshSession,
		download.Body.Bytes(), "backup-secret", "wrong-password")
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("错误账户密码应拒绝恢复备份: %d %s", wrong.Code, wrong.Body.String())
	}
}

func TestEncryptedBackupDownloadAndRestore(t *testing.T) {
	router, dataDir := newBackupTestServer(t)
	session := loginBackupTest(t, router)
	download := sessionRequest(t, router, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret","account_password":"password1234"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1"})
	if download.Code != http.StatusOK {
		t.Fatalf("下载加密备份失败: %d %s", download.Code, download.Body.String())
	}
	if !bytes.HasPrefix(download.Body.Bytes(), []byte("PK\x03\x04")) {
		t.Fatal("加密备份应为 ZIP 文件")
	}
	path := "/api/settings/backup/restore"
	missing := multipartBackupRequest(t, router, path, session, download.Body.Bytes(), "")
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), "api.error.backup_password_required") {
		t.Fatalf("缺少解密密码应被拒绝: %d %s", missing.Code, missing.Body.String())
	}
	wrong := multipartBackupRequest(t, router, path, session, download.Body.Bytes(), "wrong-backup-secret")
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "api.error.backup_password_invalid") {
		t.Fatalf("错误解密密码应被拒绝: %d %s", wrong.Code, wrong.Body.String())
	}
	success := multipartBackupRequest(t, router, path, session, download.Body.Bytes(), "backup-secret")
	if success.Code != http.StatusOK {
		t.Fatalf("加密备份恢复失败: %d %s", success.Code, success.Body.String())
	}
	pending, err := backup.LoadPending(dataDir)
	if err != nil || pending == nil {
		t.Fatalf("加密备份应写入待恢复文件: %+v, %v", pending, err)
	}
}

func TestSetupRestoreWithEncryptedBackup(t *testing.T) {
	sourceRouter, _ := newBackupTestServer(t)
	sourceSession := loginBackupTest(t, sourceRouter)
	download := sessionRequest(t, sourceRouter, http.MethodPost, "/api/settings/backup/download",
		`{"password":"backup-secret","account_password":"password1234"}`, []*http.Cookie{sourceSession},
		map[string]string{csrfHeaderName: "1"})
	if download.Code != http.StatusOK {
		t.Fatalf("下载加密备份失败: %d %s", download.Code, download.Body.String())
	}

	targetRouter, targetDataDir := newBackupTestServer(t)
	restore := multipartBackupRequest(t, targetRouter, "/api/setup/restore", nil,
		download.Body.Bytes(), "backup-secret")
	if restore.Code != http.StatusOK {
		t.Fatalf("安装页导入备份失败: %d %s", restore.Code, restore.Body.String())
	}
	pending, err := backup.LoadPending(targetDataDir)
	if err != nil || pending == nil {
		t.Fatalf("安装页导入应写入待恢复文件: %+v, %v", pending, err)
	}
}
