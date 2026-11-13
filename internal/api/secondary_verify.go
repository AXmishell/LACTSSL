package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/i18n"
)

// 二次验证 token 用于把“身份验证”和具体业务操作解耦：
// 前端先调用 /auth/secondary-verify 获取短期 token，再在业务请求头中携带。
const (
	secondaryVerifyPurpose = "secondary-verify"
	secondaryVerifyHeader  = "X-LACTSSL-VERIFY"
	secondaryVerifyTTL     = 2 * time.Minute

	secondaryActionBackupDownload     = "backup_download"
	secondaryActionBackupRestore      = "backup_restore"
	secondaryActionEntryChange        = "entry_change"
	secondaryActionPrivateCAKeyExport = "private_ca_key_export"
	secondaryActionCertificateKeyView = "certificate_key_export"
	secondaryActionSettingsWrite      = "settings_write"
	secondaryActionAccountWrite       = "account_write"
	secondaryActionTaskWrite          = "task_write"
	secondaryActionDeployExecute      = "deploy_execute"
	secondaryActionACMEWrite          = "acme_write"
	secondaryActionPrivateCAWrite     = "private_ca_write"
	secondaryActionCertificateWrite   = "certificate_write"
	secondaryActionMonitorWrite       = "monitor_write"
	secondaryActionNotificationTest   = "notification_test"
)

// toggleableStepUpActions 是允许在系统设置中单独开启/关闭二次验证的操作。
// 备份、恢复、安全入口、私有 CA 私钥导出等关键操作始终强制验证，不在此列表中。
var toggleableStepUpActions = []string{
	secondaryActionDeployExecute,
	secondaryActionTaskWrite,
	secondaryActionAccountWrite,
	secondaryActionSettingsWrite,
	secondaryActionNotificationTest,
	secondaryActionACMEWrite,
	secondaryActionPrivateCAWrite,
	secondaryActionCertificateKeyView,
	secondaryActionCertificateWrite,
	secondaryActionMonitorWrite,
}

func isToggleableStepUp(action string) bool {
	for _, item := range toggleableStepUpActions {
		if item == action {
			return true
		}
	}
	return false
}

func stepUpSettingKey(action string) string {
	return "security.step_up." + action
}

// stepUpEnabled 返回某个操作是否需要二次验证。未配置时默认开启。
func (s *Server) stepUpEnabled(action string) bool {
	if !isToggleableStepUp(action) || s.Store == nil {
		return true
	}
	value, err := s.Store.GetSetting(stepUpSettingKey(action))
	if err != nil || strings.TrimSpace(value) == "" {
		return true
	}
	return strings.ToLower(strings.TrimSpace(value)) != "false"
}

func validSecondaryAction(action string) bool {
	switch action {
	case secondaryActionBackupDownload,
		secondaryActionBackupRestore,
		secondaryActionEntryChange,
		secondaryActionPrivateCAKeyExport,
		secondaryActionCertificateKeyView,
		secondaryActionSettingsWrite,
		secondaryActionAccountWrite,
		secondaryActionTaskWrite,
		secondaryActionDeployExecute,
		secondaryActionACMEWrite,
		secondaryActionPrivateCAWrite,
		secondaryActionCertificateWrite,
		secondaryActionMonitorWrite,
		secondaryActionNotificationTest:
		return true
	default:
		return false
	}
}

// secondaryVerifyValue 把 token 绑定到用户、当前会话和具体操作，
// 避免一个操作的 token 被复用于另一个高风险操作。
func secondaryVerifyValue(userID int64, sessionID, action string) string {
	return fmt.Sprintf("%d|%s|%s", userID, sessionID, action)
}

// handleSecondaryVerify 校验当前“二次验证”方式并签发短期操作 token。
func (s *Server) handleSecondaryVerify(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.not_logged_in")
		return
	}
	var body struct {
		Action   string `json:"action"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	action := strings.TrimSpace(body.Action)
	if !validSecondaryAction(action) {
		failKey(c, http.StatusBadRequest, "api.error.secondary_action_invalid")
		return
	}
	if !s.verifySensitive(c, user, body.Password, body.TOTP) {
		return
	}
	expires := time.Now().Add(secondaryVerifyTTL)
	token := s.Auth.SignExpiringValue(secondaryVerifyPurpose,
		secondaryVerifyValue(user.ID, currentSessionID(c), action), expires)
	ok(c, gin.H{
		"token":      token,
		"expires_in": int(secondaryVerifyTTL.Seconds()),
	})
}

// requireSecondaryVerify 校验业务请求携带的二次验证 token。
func (s *Server) requireSecondaryVerify(c *gin.Context, action string) bool {
	// 系统设置允许逐项关闭二次验证；关键操作（备份/入口/私有 CA 私钥）
	// 不在 toggleableStepUpActions 中，始终要求验证。
	if !s.stepUpEnabled(action) {
		return true
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.not_logged_in")
		return false
	}
	token := strings.TrimSpace(c.GetHeader(secondaryVerifyHeader))
	value := secondaryVerifyValue(user.ID, currentSessionID(c), action)
	if token == "" || !s.Auth.VerifyExpiringValue(secondaryVerifyPurpose, value, token) {
		c.Abort()
		c.JSON(http.StatusForbidden, gin.H{
			"success":                   false,
			"code":                      "api.error.secondary_verify_required",
			"message":                   i18n.T(locale(c), "api.error.secondary_verify_required", nil),
			"secondary_verify_required": true,
		})
		return false
	}
	return true
}
