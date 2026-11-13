package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/backup"
	"lactssl/internal/i18n"
)

// minBackupPasswordLength 是备份加密密码的最小长度，与账户密码要求独立。
const minBackupPasswordLength = 8

// backupInfo 返回备份页面所需的数据库大小与最近备份信息。
func (s *Server) backupInfo(c *gin.Context) {
	if s.Backup == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.backup_unavailable")
		return
	}
	info, err := s.Backup.Info()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	lastBackup, _ := s.Store.GetSetting("backup.last_at")
	ok(c, gin.H{
		"database_size":   info.DatabaseSize,
		"config_size":     info.ConfigSize,
		"config_included": info.ConfigIncluded,
		"pending_restore": info.PendingRestore,
		"last_backup_at":  lastBackup,
	})
}

// downloadBackup 生成并下载包含数据库与配置的 ZIP 备份包。
// 必须提供加密密码，避免包含凭据与私钥的备份以明文形式落地。
func (s *Server) downloadBackup(c *gin.Context) {
	if s.Backup == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.backup_unavailable")
		return
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if !s.requireSecondaryVerify(c, secondaryActionBackupDownload) {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if allowed, wait := s.limiter.Allow("backup:download:"+strconv.FormatInt(user.ID, 10), 5, 10*time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	password := strings.TrimSpace(body.Password)
	if password == "" {
		failKey(c, http.StatusBadRequest, "api.error.backup_password_required")
		return
	}
	if len(password) < minBackupPasswordLength {
		failKey(c, http.StatusBadRequest, "api.error.backup_password_too_short")
		return
	}
	if len(password) > 128 {
		failKey(c, http.StatusBadRequest, "api.error.backup_password_too_long")
		return
	}
	filePath, filename, cleanup, err := s.Backup.Export(password)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.backup_create_failed", "error", err.Error())
		return
	}
	defer cleanup()

	_ = s.Store.SetSetting("backup.last_at", time.Now().UTC().Format(time.RFC3339))
	s.Store.AddLogKey("settings", 0, "info", "log.settings.backup_downloaded", nil)
	c.FileAttachment(filePath, filename)
}

// restoreBackup 校验上传的备份包，写入待恢复目录并安排服务重启。
func (s *Server) restoreBackup(c *gin.Context) {
	if s.Backup == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.backup_unavailable")
		return
	}

	// 先校验二次验证 token，再解析上传内容，避免大文件上传期间 token 过期。
	if !s.requireSecondaryVerify(c, secondaryActionBackupRestore) {
		return
	}

	// 限制整个 multipart 请求体，避免超大文件写入磁盘。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, backup.MaxUploadSize+8<<20)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			failKey(c, http.StatusRequestEntityTooLarge, "api.error.backup_too_large")
			return
		}
		failKey(c, http.StatusBadRequest, "api.error.backup_file_required")
		return
	}
	if fileHeader.Size <= 0 {
		failKey(c, http.StatusBadRequest, "api.error.backup_file_required")
		return
	}
	if fileHeader.Size > backup.MaxUploadSize {
		failKey(c, http.StatusRequestEntityTooLarge, "api.error.backup_too_large")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.backup_invalid_file")
		return
	}
	defer file.Close()
	if err := s.Backup.Stage(file, fileHeader.Filename, strings.TrimSpace(c.PostForm("backup_password"))); err != nil {
		failBackupUpload(c, err)
		return
	}

	s.Store.AddLogKey("settings", 0, "warning", "log.settings.backup_restored", nil)
	restarting := s.Restart != nil
	ok(c, gin.H{
		"message":    i18n.T(locale(c), "api.ok.backup_restored", nil),
		"restarting": restarting,
	})
	if restarting {
		// 等响应发送完成后再触发优雅退出；Docker 的 restart 策略或进程管理器会拉起新进程。
		go func() {
			time.Sleep(1500 * time.Millisecond)
			s.Restart()
		}()
	}
}

// handleSetupRestore 在系统尚未初始化时允许从备份包恢复数据。
// 该接口无需登录，因此必须先确认系统中没有任何账户。
func (s *Server) handleSetupRestore(c *gin.Context) {
	if s.Backup == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.backup_unavailable")
		return
	}
	if allowed, wait := s.limiter.Allow("setup-restore:"+clientRateKey(c), 5, time.Minute); !allowed {
		rateLimited(c, wait)
		return
	}
	count, err := s.Store.CountUsers()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		failKey(c, http.StatusBadRequest, "api.error.already_initialized")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, backup.MaxUploadSize+8<<20)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			failKey(c, http.StatusRequestEntityTooLarge, "api.error.backup_too_large")
			return
		}
		failKey(c, http.StatusBadRequest, "api.error.backup_file_required")
		return
	}
	if fileHeader.Size <= 0 {
		failKey(c, http.StatusBadRequest, "api.error.backup_file_required")
		return
	}
	if fileHeader.Size > backup.MaxUploadSize {
		failKey(c, http.StatusRequestEntityTooLarge, "api.error.backup_too_large")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		failKey(c, http.StatusBadRequest, "api.error.backup_invalid_file")
		return
	}
	defer file.Close()
	if err := s.Backup.Stage(file, fileHeader.Filename, strings.TrimSpace(c.PostForm("backup_password"))); err != nil {
		failBackupUpload(c, err)
		return
	}

	restarting := s.Restart != nil
	ok(c, gin.H{
		"message":    i18n.T(locale(c), "api.ok.backup_restore_pending", nil),
		"restarting": restarting,
	})
	if restarting {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			s.Restart()
		}()
	}
}

// failBackupUpload 把备份解析错误转换为统一的接口错误。
func failBackupUpload(c *gin.Context, err error) {
	switch {
	case errors.Is(err, backup.ErrTooLarge):
		failKey(c, http.StatusRequestEntityTooLarge, "api.error.backup_too_large")
	case errors.Is(err, backup.ErrPasswordRequired):
		failKey(c, http.StatusBadRequest, "api.error.backup_password_required")
	case errors.Is(err, backup.ErrInvalidPassword):
		failKey(c, http.StatusBadRequest, "api.error.backup_password_invalid")
	case errors.Is(err, backup.ErrInvalidArchive), errors.Is(err, backup.ErrInvalidDatabase), errors.Is(err, backup.ErrInvalidConfig):
		failKey(c, http.StatusBadRequest, "api.error.backup_invalid_file")
	default:
		// 未认证的安装页也调用此函数，避免把文件系统/数据库错误原文返回给调用方。
		failKey(c, http.StatusInternalServerError, "api.error.backup_restore_failed")
	}
}
