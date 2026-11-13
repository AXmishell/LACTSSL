package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"lactssl/internal/backup"
)

func secondaryVerifyRequest(t *testing.T, router http.Handler, session *http.Cookie, action, password, totp string, base ...string) *httptest.ResponseRecorder {
	t.Helper()
	prefix := "/secret-entry/api"
	if len(base) > 0 && base[0] != "" {
		prefix = strings.TrimSuffix(base[0], "/")
	}
	body := fmt.Sprintf(`{"action":%q,"password":%q,"totp":%q}`, action, password, totp)
	return sessionRequest(t, router, http.MethodPost, prefix+"/auth/secondary-verify",
		body, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
}

func secondaryVerifyToken(t *testing.T, router http.Handler, session *http.Cookie, action, password, totp string, base ...string) string {
	t.Helper()
	resp := secondaryVerifyRequest(t, router, session, action, password, totp, base...)
	if resp.Code != http.StatusOK {
		t.Fatalf("获取二次验证 token 失败(%s): %d %s", action, resp.Code, resp.Body.String())
	}
	var payload struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析二次验证 token 失败: %v", err)
	}
	if payload.Data.Token == "" {
		t.Fatal("二次验证 token 为空")
	}
	return payload.Data.Token
}

func secondaryVerifyHeaders(token string) map[string]string {
	return map[string]string{secondaryVerifyHeader: token}
}

// totpCodeAt 生成指定时间的动态验证码。测试中用它避开验证码重放限制。
func totpCodeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	code, err := totp.GenerateCodeCustom(secret, at, totp.ValidateOpts{
		Period:    30,
		Skew:      1,
		Digits:    otp.DigitsSix,
		Algorithm: otp.AlgorithmSHA1,
	})
	if err != nil {
		t.Fatalf("生成动态验证码失败: %v", err)
	}
	return code
}

// enableTOTPForTest 通过接口完成 TOTP 初始化与启用，并返回密钥。
// handleTOTPEnable 会消费当前时间窗口的验证码，因此后续操作需使用新的时间窗口。
func enableTOTPForTest(t *testing.T, router http.Handler, session *http.Cookie, password string) string {
	t.Helper()
	setup := sessionRequest(t, router, http.MethodPost,
		"/secret-entry/api/auth/totp/setup", `{}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if setup.Code != http.StatusOK {
		t.Fatalf("获取 TOTP 密钥失败: %d %s", setup.Code, setup.Body.String())
	}
	var payload struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	if err := json.Unmarshal(setup.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析 TOTP 密钥失败: %v", err)
	}
	if payload.Data.Secret == "" {
		t.Fatal("TOTP 密钥为空")
	}
	code := totpCodeAt(t, payload.Data.Secret, time.Now())
	enable := sessionRequest(t, router, http.MethodPost,
		"/secret-entry/api/auth/totp/enable",
		`{"code":"`+code+`","password":"`+password+`"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if enable.Code != http.StatusOK {
		t.Fatalf("启用 TOTP 失败: %d %s", enable.Code, enable.Body.String())
	}
	return payload.Data.Secret
}

func TestChangeEntryWithVerifyModes(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	enableTOTPForTest(t, router, session, "password1234")

	base := "/secret-entry/api"

	// 默认是密码验证模式。
	settings := sessionRequest(t, router, http.MethodGet, base+"/settings", "", []*http.Cookie{session}, nil)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"verify_mode":"password"`) {
		t.Fatalf("默认验证方式应为密码: %d %s", settings.Code, settings.Body.String())
	}

	wrong := secondaryVerifyRequest(t, router, session, secondaryActionEntryChange, "wrong-password", "")
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("密码模式下错误密码应被拒绝: %d %s", wrong.Code, wrong.Body.String())
	}

	passwordToken := secondaryVerifyToken(t, router, session, secondaryActionEntryChange, "password1234", "")
	okEntry := sessionRequest(t, router, http.MethodPost, base+"/security/entry",
		`{"entry":"entry-password"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: passwordToken})
	if okEntry.Code != http.StatusOK {
		t.Fatalf("密码模式下正确密码应通过: %d %s", okEntry.Code, okEntry.Body.String())
	}
	base = "/entry-password/api"

	// 使用当前生效的密码切换到 TOTP 验证。
	switched := sessionRequest(t, router, http.MethodPost, base+"/security/verify-mode",
		`{"mode":"totp","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if switched.Code != http.StatusOK {
		t.Fatalf("切换为 TOTP 验证失败: %d %s", switched.Code, switched.Body.String())
	}
	if mode, _ := st.GetSetting(verifyModeSetting); mode != verifyModeTOTP {
		t.Fatalf("验证方式未保存: %q", mode)
	}

	// TOTP 模式下只接受动态验证码。
	missing := secondaryVerifyRequest(t, router, session, secondaryActionEntryChange, "", "", base)
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), `"totp_required":true`) {
		t.Fatalf("TOTP 模式下缺少验证码应被拒绝: %d %s", missing.Code, missing.Body.String())
	}

	wrongCode := secondaryVerifyRequest(t, router, session, secondaryActionEntryChange, "", "000000", base)
	if wrongCode.Code != http.StatusBadRequest || !strings.Contains(wrongCode.Body.String(), "api.error.totp_invalid") {
		t.Fatalf("错误验证码应被拒绝: %d %s", wrongCode.Code, wrongCode.Body.String())
	}

	user, err := st.GetUserByEmail("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	code := totpCodeAt(t, user.TOTPSecret, time.Now().Add(30*time.Second))
	totpToken := secondaryVerifyToken(t, router, session, secondaryActionEntryChange, "", code, base)
	okTOTP := sessionRequest(t, router, http.MethodPost, base+"/security/entry",
		`{"entry":"entry-totp"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: totpToken})
	if okTOTP.Code != http.StatusOK {
		t.Fatalf("TOTP 模式下正确验证码应通过: %d %s", okTOTP.Code, okTOTP.Body.String())
	}
	base = "/entry-totp/api"

	// 从 TOTP 切回密码验证时，必须提供当前 TOTP。
	backCode := totpCodeAt(t, user.TOTPSecret, time.Now().Add(-30*time.Second))
	back := sessionRequest(t, router, http.MethodPost, base+"/security/verify-mode",
		`{"mode":"password","totp":"`+backCode+`"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if back.Code != http.StatusOK {
		t.Fatalf("切回密码验证失败: %d %s", back.Code, back.Body.String())
	}
	if mode, _ := st.GetSetting(verifyModeSetting); mode != verifyModePassword {
		t.Fatalf("验证方式未切回密码: %q", mode)
	}
}

func TestVerifyModeRequiresTOTPEnabled(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	for _, mode := range []string{"totp", "password_totp"} {
		resp := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
			`{"mode":"`+mode+`","password":"password1234"}`,
			[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "api.error.totp_not_enabled") {
			t.Fatalf("未启用 TOTP 时不应允许切换为 %s: %d %s", mode, resp.Code, resp.Body.String())
		}
	}
}

func TestVerifyModeNoneSkipsVerification(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	switched := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"none","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if switched.Code != http.StatusOK {
		t.Fatalf("切换为无验证失败: %d %s", switched.Code, switched.Body.String())
	}
	if mode, _ := st.GetSetting(verifyModeSetting); mode != verifyModeNone {
		t.Fatalf("验证方式未保存为 none: %q", mode)
	}

	token := secondaryVerifyToken(t, router, session, secondaryActionEntryChange, "", "")
	entry := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/entry",
		`{"entry":"entry-none"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: token})
	if entry.Code != http.StatusOK {
		t.Fatalf("无验证模式修改入口不应要求凭证: %d %s", entry.Code, entry.Body.String())
	}

	settings := sessionRequest(t, router, http.MethodGet, "/entry-none/api/settings", "", []*http.Cookie{session}, nil)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"verify_mode":"none"`) {
		t.Fatalf("设置接口应返回 none: %d %s", settings.Code, settings.Body.String())
	}
}

func TestVerifyModePasswordTOTPRequiresBoth(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	secret := enableTOTPForTest(t, router, session, "password1234")

	switched := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"password_totp","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if switched.Code != http.StatusOK {
		t.Fatalf("切换为密码和 TOTP 验证失败: %d %s", switched.Code, switched.Body.String())
	}
	if mode, _ := st.GetSetting(verifyModeSetting); mode != verifyModePasswordTOTP {
		t.Fatalf("验证方式未保存: %q", mode)
	}

	base := "/secret-entry/api"
	onlyPassword := secondaryVerifyRequest(t, router, session, secondaryActionEntryChange, "password1234", "")
	if onlyPassword.Code != http.StatusBadRequest || !strings.Contains(onlyPassword.Body.String(), `"totp_required":true`) {
		t.Fatalf("仅密码不应通过密码和 TOTP 模式: %d %s", onlyPassword.Code, onlyPassword.Body.String())
	}

	code := totpCodeAt(t, secret, time.Now().Add(30*time.Second))
	onlyTOTP := secondaryVerifyRequest(t, router, session, secondaryActionEntryChange, "", code)
	if onlyTOTP.Code != http.StatusBadRequest || !strings.Contains(onlyTOTP.Body.String(), "api.error.current_password_wrong") {
		t.Fatalf("仅验证码不应通过密码和 TOTP 模式: %d %s", onlyTOTP.Code, onlyTOTP.Body.String())
	}

	token := secondaryVerifyToken(t, router, session, secondaryActionEntryChange, "password1234", code)
	both := sessionRequest(t, router, http.MethodPost, base+"/security/entry",
		`{"entry":"entry-both"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: token})
	if both.Code != http.StatusOK {
		t.Fatalf("密码和 TOTP 同时提供时应通过: %d %s", both.Code, both.Body.String())
	}
}

func TestVerifyModePasswordTOTPFallsBackWhenTOTPDisabled(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	secret := enableTOTPForTest(t, router, session, "password1234")

	switched := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"password_totp","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if switched.Code != http.StatusOK {
		t.Fatalf("切换为密码和 TOTP 验证失败: %d %s", switched.Code, switched.Body.String())
	}

	user, err := st.GetUserByEmail("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, secret, false); err != nil {
		t.Fatal(err)
	}

	settings := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/settings", "", []*http.Cookie{session}, nil)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"verify_mode":"password"`) {
		t.Fatalf("TOTP 关闭后密码和 TOTP 模式应回退密码: %d %s", settings.Code, settings.Body.String())
	}
	token := secondaryVerifyToken(t, router, session, secondaryActionEntryChange, "password1234", "")
	entry := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/entry",
		`{"entry":"entry-both-fallback"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: token})
	if entry.Code != http.StatusOK {
		t.Fatalf("回退密码模式后应接受密码: %d %s", entry.Code, entry.Body.String())
	}
}

func TestVerifyModeFallsBackWhenTOTPDisabled(t *testing.T) {
	router, st := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)
	secret := enableTOTPForTest(t, router, session, "password1234")

	switched := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"totp","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if switched.Code != http.StatusOK {
		t.Fatalf("切换为 TOTP 验证失败: %d %s", switched.Code, switched.Body.String())
	}

	user, err := st.GetUserByEmail("admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetTOTP(user.ID, secret, false); err != nil {
		t.Fatal(err)
	}

	// 设置仍为 totp，但 TOTP 已不可用，应按密码验证处理。
	settings := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/settings", "", []*http.Cookie{session}, nil)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"verify_mode":"password"`) {
		t.Fatalf("TOTP 关闭后应回退密码模式: %d %s", settings.Code, settings.Body.String())
	}
	token := secondaryVerifyToken(t, router, session, secondaryActionEntryChange, "password1234", "")
	entry := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/entry",
		`{"entry":"entry-fallback"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: token})
	if entry.Code != http.StatusOK {
		t.Fatalf("回退密码模式后应接受密码: %d %s", entry.Code, entry.Body.String())
	}
}

func TestSecondaryVerifyTokenIsActionBound(t *testing.T) {
	router, _ := newBackupTestServer(t)
	session := loginBackupTest(t, router)

	// 为“恢复备份”签发的 token 不能用于“下载备份”。
	token := secondaryVerifyToken(t, router, session, secondaryActionBackupRestore, "password1234", "")
	resp := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/settings/backup/download",
		`{"password":"backup-secret"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: token})
	if resp.Code != http.StatusForbidden || !strings.Contains(resp.Body.String(), `"secondary_verify_required":true`) {
		t.Fatalf("跨操作复用 token 应被拒绝: %d %s", resp.Code, resp.Body.String())
	}
}

func TestBackupDownloadUsesVerifyMode(t *testing.T) {
	router, _ := newBackupTestServer(t)
	session := loginBackupTest(t, router)
	path := "/secret-entry/api/settings/backup/download"

	// 无验证模式下，下载备份不再要求密码或 TOTP。
	toNone := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"none","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if toNone.Code != http.StatusOK {
		t.Fatalf("切换为无验证失败: %d %s", toNone.Code, toNone.Body.String())
	}
	noneToken := secondaryVerifyToken(t, router, session, secondaryActionBackupDownload, "", "")
	noneDownload := sessionRequest(t, router, http.MethodPost, path,
		`{"password":"backup-secret"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: noneToken})
	if noneDownload.Code != http.StatusOK {
		t.Fatalf("无验证模式应允许直接下载备份: %d %s", noneDownload.Code, noneDownload.Body.String())
	}

	// TOTP 模式下，下载备份只要求动态验证码。
	secret := enableTOTPForTest(t, router, session, "password1234")
	toTOTP := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"totp"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if toTOTP.Code != http.StatusOK {
		t.Fatalf("切换为 TOTP 验证失败: %d %s", toTOTP.Code, toTOTP.Body.String())
	}

	missing := secondaryVerifyRequest(t, router, session, secondaryActionBackupDownload, "", "")
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), `"totp_required":true`) {
		t.Fatalf("TOTP 模式下载缺少验证码应被拒绝: %d %s", missing.Code, missing.Body.String())
	}

	code := totpCodeAt(t, secret, time.Now().Add(30*time.Second))
	totpToken := secondaryVerifyToken(t, router, session, secondaryActionBackupDownload, "", code)
	totpDownload := sessionRequest(t, router, http.MethodPost, path,
		`{"password":"backup-secret"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: totpToken})
	if totpDownload.Code != http.StatusOK {
		t.Fatalf("TOTP 验证后应允许下载备份: %d %s", totpDownload.Code, totpDownload.Body.String())
	}
}

func TestRestoreBackupWithTOTPVerification(t *testing.T) {
	router, dataDir := newBackupTestServer(t)
	session := loginBackupTest(t, router)

	downloadToken := secondaryVerifyToken(t, router, session, secondaryActionBackupDownload, "password1234", "")
	download := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/settings/backup/download",
		`{"password":"backup-secret"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: downloadToken})
	if download.Code != http.StatusOK {
		t.Fatalf("下载备份失败: %d %s", download.Code, download.Body.String())
	}

	secret := enableTOTPForTest(t, router, session, "password1234")
	switched := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"totp","password":"password1234"}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if switched.Code != http.StatusOK {
		t.Fatalf("切换为 TOTP 验证失败: %d %s", switched.Code, switched.Body.String())
	}

	missing := secondaryVerifyRequest(t, router, session, secondaryActionBackupRestore, "", "")
	if missing.Code != http.StatusBadRequest || !strings.Contains(missing.Body.String(), `"totp_required":true`) {
		t.Fatalf("恢复备份缺少验证码应被拒绝: %d %s", missing.Code, missing.Body.String())
	}

	wrong := secondaryVerifyRequest(t, router, session, secondaryActionBackupRestore, "", "000000")
	if wrong.Code != http.StatusBadRequest || !strings.Contains(wrong.Body.String(), "api.error.totp_invalid") {
		t.Fatalf("恢复备份错误验证码应被拒绝: %d %s", wrong.Code, wrong.Body.String())
	}

	code := totpCodeAt(t, secret, time.Now().Add(30*time.Second))
	restoreToken := secondaryVerifyToken(t, router, session, secondaryActionBackupRestore, "", code)
	success := multipartBackupRequest(t, router, "/secret-entry/api/settings/backup/restore",
		session, download.Body.Bytes(), restoreToken, "backup-secret")
	if success.Code != http.StatusOK {
		t.Fatalf("TOTP 验证后恢复备份失败: %d %s", success.Code, success.Body.String())
	}
	pending, err := backup.LoadPending(dataDir)
	if err != nil || pending == nil {
		t.Fatalf("恢复请求应写入待恢复文件: %+v, %v", pending, err)
	}
}

func TestUpdateSettingsRequiresStepUpOnlyForSensitiveFields(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	languageOnly := sessionRequest(t, router, http.MethodPut, "/secret-entry/api/settings",
		`{"language":"en-US"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if languageOnly.Code != http.StatusOK {
		t.Fatalf("仅切换语言不应要求二次验证: %d %s", languageOnly.Code, languageOnly.Body.String())
	}

	sensitive := sessionRequest(t, router, http.MethodPut, "/secret-entry/api/settings",
		`{"smtp_host":"smtp.example.com"}`, []*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if sensitive.Code != http.StatusForbidden || !strings.Contains(sensitive.Body.String(), `"secondary_verify_required":true`) {
		t.Fatalf("修改 SMTP 未完成二次验证时应返回 403: %d %s", sensitive.Code, sensitive.Body.String())
	}

	token := secondaryVerifyToken(t, router, session, secondaryActionSettingsWrite, "password1234", "")
	allowed := sessionRequest(t, router, http.MethodPut, "/secret-entry/api/settings",
		`{"smtp_host":"smtp.example.com"}`, []*http.Cookie{session},
		map[string]string{csrfHeaderName: "1", secondaryVerifyHeader: token})
	if allowed.Code != http.StatusOK {
		t.Fatalf("二次验证后应允许修改 SMTP: %d %s", allowed.Code, allowed.Body.String())
	}
}

func TestStepUpCanBeDisabledPerAction(t *testing.T) {
	router, _ := newSessionTestServerWithStore(t)
	session := loginSessionTest(t, router)

	settings := sessionRequest(t, router, http.MethodGet, "/secret-entry/api/settings", "", []*http.Cookie{session}, nil)
	if settings.Code != http.StatusOK || !strings.Contains(settings.Body.String(), `"step_up"`) {
		t.Fatalf("设置接口应返回 step_up 开关: %d %s", settings.Code, settings.Body.String())
	}

	createAccount := func() *httptest.ResponseRecorder {
		return sessionRequest(t, router, http.MethodPost, "/secret-entry/api/deploy-accounts",
			`{"name":"local","type":"local","config":{}}`, []*http.Cookie{session},
			map[string]string{csrfHeaderName: "1"})
	}

	if resp := createAccount(); resp.Code != http.StatusForbidden {
		t.Fatalf("默认应要求二次验证: %d %s", resp.Code, resp.Body.String())
	}

	disable := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"password","password":"password1234","step_up":{"account_write":false}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if disable.Code != http.StatusOK {
		t.Fatalf("关闭 account_write 二次验证失败: %d %s", disable.Code, disable.Body.String())
	}
	if resp := createAccount(); resp.Code != http.StatusOK {
		t.Fatalf("关闭后不应再要求二次验证: %d %s", resp.Code, resp.Body.String())
	}

	enable := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"password","password":"password1234","step_up":{"account_write":true}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if enable.Code != http.StatusOK {
		t.Fatalf("重新开启 account_write 二次验证失败: %d %s", enable.Code, enable.Body.String())
	}
	if resp := createAccount(); resp.Code != http.StatusForbidden {
		t.Fatalf("重新开启后应要求二次验证: %d %s", resp.Code, resp.Body.String())
	}

	invalid := sessionRequest(t, router, http.MethodPost, "/secret-entry/api/security/verify-mode",
		`{"mode":"password","password":"password1234","step_up":{"unknown_action":false}}`,
		[]*http.Cookie{session}, map[string]string{csrfHeaderName: "1"})
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("未知开关应被拒绝: %d %s", invalid.Code, invalid.Body.String())
	}
}
