package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/config"
	"lactssl/internal/i18n"
	"lactssl/internal/ratelimit"
)

const configPendingRestartSetting = "config.pending_restart"

type configPendingRestart struct {
	Revision string    `json:"revision"`
	At       time.Time `json:"at"`
	Fields   []string  `json:"fields,omitempty"`
}

type configProxySuggestion struct {
	Value          string `json:"value"`
	Kind           string `json:"kind"`
	Confidence     string `json:"confidence"`
	Reason         string `json:"reason"`
	AlreadyTrusted bool   `json:"already_trusted"`
}

type configProxyWarning struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// getSettingsConfig 返回当前 config.yaml 的可编辑白名单、版本与快照列表。
func (s *Server) getSettingsConfig(c *gin.Context) {
	if s.ConfigFile == "" {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
		return
	}
	cfg, err := config.LoadSetupConfig(s.ConfigFile)
	if err != nil {
		s.logger().Warn("读取配置文件失败", "error", err)
		failKey(c, http.StatusInternalServerError, "api.error.config_unavailable")
		return
	}
	revision, err := config.ConfigRevision(s.ConfigFile)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.config_unavailable")
		return
	}
	effective, _ := config.EffectiveAddr(s.ConfigFile)
	backups, err := config.ListConfigBackups(s.ConfigFile, s.configDataDir())
	if err != nil {
		s.logger().Warn("读取配置备份失败", "error", err)
		backups = []config.ConfigBackupInfo{}
	}
	pending, hasPending := s.readConfigPendingRestart()
	var pendingValue any
	if hasPending {
		pendingValue = pending
	}
	ok(c, gin.H{
		"config":          cfg,
		"revision":        revision,
		"path":            s.ConfigFile,
		"writable":        configFileWritable(s.ConfigFile),
		"effective_addr":  effective,
		"restart_pending": pendingValue,
		"backups":         backups,
	})
}

// updateSettingsConfig 保存白名单字段；写盘前自动创建配置快照。
func (s *Server) updateSettingsConfig(c *gin.Context) {
	if s.ConfigFile == "" {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
		return
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.SettingsConfigSave, fmt.Sprintf("settings-config-save:%d", user.ID)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Revision string                  `json:"revision"`
		Password string                  `json:"password"`
		TOTP     string                  `json:"totp"`
		Note     string                  `json:"note"`
		Config   config.SetupConfigPatch `json:"config"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	if body.Config.Empty() {
		revision, _ := config.ConfigRevision(s.ConfigFile)
		ok(c, gin.H{
			"message":          i18n.T(locale(c), "api.ok.config_unchanged", nil),
			"restart_required": false,
			"changed_fields":   []string{},
			"revision":         revision,
		})
		return
	}

	s.configMu.Lock()
	result, err := config.UpdateEditableConfig(
		s.ConfigFile,
		s.configDataDir(),
		body.Config,
		body.Revision,
		"before-save",
		body.Note,
		user.Email,
		Version,
	)
	if err == nil && !result.Noop {
		s.markConfigPendingRestart(result)
	}
	s.configMu.Unlock()
	if err != nil {
		s.respondConfigError(c, err)
		return
	}
	if result.Noop {
		ok(c, gin.H{
			"message":          i18n.T(locale(c), "api.ok.config_unchanged", nil),
			"restart_required": false,
			"changed_fields":   []string{},
			"revision":         result.Revision,
		})
		return
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.config_updated", map[string]any{
		"fields":    strings.Join(result.ChangedFields, ","),
		"backup_id": configBackupID(result),
	})
	var backupID string
	if result.Backup != nil {
		backupID = result.Backup.ID
	}
	ok(c, gin.H{
		"message":          i18n.T(locale(c), "api.ok.config_saved", nil),
		"restart_required": true,
		"changed_fields":   result.ChangedFields,
		"backup_id":        backupID,
		"revision":         result.Revision,
	})
}

// settingsConfigProxyHints 根据当前请求返回最近一跳与可信代理候选。
// 只信任 TCP 对端地址；转发头仅作为“未验证”的信息展示。
func (s *Server) settingsConfigProxyHints(c *gin.Context) {
	peer := requestPeerIP(c.Request.RemoteAddr)
	trusted := s.trustedProxiesFromConfig()
	xff := splitHeaderList(c.GetHeader("X-Forwarded-For"), 8, 64)
	xRealIP := truncateHeader(c.GetHeader("X-Real-IP"), 64)
	forwarded := truncateHeader(c.GetHeader("Forwarded"), 512)
	proto := truncateHeader(c.GetHeader("X-Forwarded-Proto"), 32)
	cfConnecting := truncateHeader(c.GetHeader("CF-Connecting-IP"), 64)
	cfRay := truncateHeader(c.GetHeader("CF-Ray"), 64)
	hasForwarded := len(xff) > 0 || xRealIP != "" || forwarded != ""

	suggestions := make([]configProxySuggestion, 0, 1)
	warnings := make([]configProxyWarning, 0, 2)
	rightmost := ""
	if len(xff) > 0 {
		rightmost = xff[len(xff)-1]
	}
	peerIsRightmost := rightmost != "" && samePeerAddress(peer, rightmost)
	if peer.IsValid() {
		already := config.TrustedProxyContains(trusted, peer)
		switch {
		case peer.IsLoopback():
			suggestions = append(suggestions, configProxySuggestion{
				Value:          peer.String(),
				Kind:           "ip",
				Confidence:     "high",
				Reason:         "loopback",
				AlreadyTrusted: already,
			})
		case hasForwarded && !peerIsRightmost:
			suggestions = append(suggestions, configProxySuggestion{
				Value:          peer.String(),
				Kind:           "ip",
				Confidence:     "high",
				Reason:         "forwarded_headers",
				AlreadyTrusted: already,
			})
		default:
			suggestions = append(suggestions, configProxySuggestion{
				Value:          peer.String(),
				Kind:           "ip",
				Confidence:     "low",
				Reason:         "direct_connection",
				AlreadyTrusted: already,
			})
			warnings = append(warnings, configProxyWarning{Code: "direct_connection"})
		}
	}
	if cfConnecting != "" || cfRay != "" {
		warnings = append(warnings, configProxyWarning{Code: "cdn_detected"})
	}
	for _, item := range trusted {
		prefix, err := netip.ParsePrefix(item)
		if err != nil {
			continue
		}
		bits := prefix.Bits()
		if (prefix.Addr().Is4() && bits < 16) || (prefix.Addr().Is6() && bits < 48) {
			warnings = append(warnings, configProxyWarning{Code: "broad_proxy_range", Detail: item})
			break
		}
	}
	peerValue := ""
	if peer.IsValid() {
		peerValue = peer.String()
	}
	ok(c, gin.H{
		"peer_ip":          peerValue,
		"peer_is_loopback": peer.IsValid() && peer.IsLoopback(),
		"peer_is_private":  peer.IsValid() && peer.IsPrivate(),
		"has_forwarded":    hasForwarded,
		"forwarded": gin.H{
			"x_forwarded_for":   xff,
			"x_real_ip":         xRealIP,
			"x_forwarded_proto": proto,
			"forwarded":         forwarded,
			"cf_connecting_ip":  cfConnecting,
			"cf_ray":            cfRay,
		},
		"trusted_proxies":     trusted,
		"effective_client_ip": c.ClientIP(),
		"suggestions":         suggestions,
		"warnings":            warnings,
	})
}

// listSettingsConfigBackups 返回配置文件快照列表。
func (s *Server) listSettingsConfigBackups(c *gin.Context) {
	backups, err := config.ListConfigBackups(s.ConfigFile, s.configDataDir())
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.config_unavailable")
		return
	}
	ok(c, gin.H{"backups": backups})
}

// createSettingsConfigBackup 手动创建一份配置快照。
func (s *Server) createSettingsConfigBackup(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.SettingsConfigBackup, fmt.Sprintf("settings-config-backup:%d", user.ID)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Note     string `json:"note"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	s.configMu.Lock()
	info, err := config.CreateConfigBackup(s.ConfigFile, s.configDataDir(), "manual", body.Note, user.Email, Version)
	s.configMu.Unlock()
	if err != nil {
		s.logger().Warn("创建配置备份失败", "error", err)
		failKey(c, http.StatusInternalServerError, "api.error.config_backup_create_failed")
		return
	}
	s.Store.AddLogKey("settings", 0, "info", "log.settings.config_backup_created", map[string]any{
		"id":     info.ID,
		"reason": info.Reason,
	})
	ok(c, info)
}

// downloadSettingsConfigBackup 下载完整配置文件快照（包含密钥）。
func (s *Server) downloadSettingsConfigBackup(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.SettingsConfigDownload, fmt.Sprintf("settings-config-download:%d", user.ID)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	info, data, err := config.LoadConfigBackup(s.ConfigFile, s.configDataDir(), c.Param("id"))
	if err != nil {
		failKey(c, http.StatusNotFound, "api.error.config_backup_not_found")
		return
	}
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.config_downloaded", map[string]any{
		"id": info.ID,
	})
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"config-%s.yaml\"", info.ID))
	c.Data(http.StatusOK, "application/yaml; charset=utf-8", data)
}

// restoreSettingsConfigBackup 用快照中的白名单字段恢复当前配置。
func (s *Server) restoreSettingsConfigBackup(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.SettingsConfigRestore, fmt.Sprintf("settings-config-restore:%d", user.ID)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	s.configMu.Lock()
	result, err := config.RestoreConfigBackup(s.ConfigFile, s.configDataDir(), c.Param("id"), user.Email, Version)
	if err == nil && !result.Noop {
		s.markConfigPendingRestart(result)
	}
	s.configMu.Unlock()
	if err != nil {
		if errors.Is(err, config.ErrConfigBackupNotFound) {
			failKey(c, http.StatusNotFound, "api.error.config_backup_not_found")
			return
		}
		s.respondConfigError(c, err)
		return
	}
	if result.Noop {
		ok(c, gin.H{
			"message":          i18n.T(locale(c), "api.ok.config_unchanged", nil),
			"restart_required": false,
			"revision":         result.Revision,
		})
		return
	}
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.config_restored", map[string]any{
		"id":     c.Param("id"),
		"fields": strings.Join(result.ChangedFields, ","),
	})
	ok(c, gin.H{
		"message":          i18n.T(locale(c), "api.ok.config_restored", nil),
		"restart_required": true,
		"changed_fields":   result.ChangedFields,
		"revision":         result.Revision,
	})
}

// deleteSettingsConfigBackup 删除一份配置快照。
func (s *Server) deleteSettingsConfigBackup(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.SettingsConfigDelete, fmt.Sprintf("settings-config-delete:%d", user.ID)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	s.configMu.Lock()
	err := config.DeleteConfigBackup(s.configDataDir(), c.Param("id"))
	s.configMu.Unlock()
	if err != nil {
		if errors.Is(err, config.ErrConfigBackupNotFound) {
			failKey(c, http.StatusNotFound, "api.error.config_backup_not_found")
			return
		}
		failKey(c, http.StatusInternalServerError, "api.error.config_backup_delete_failed")
		return
	}
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.config_backup_deleted", map[string]any{"id": c.Param("id")})
	ok(c, gin.H{"message": i18n.T(locale(c), "api.ok.config_backup_deleted", nil)})
}

// restartSettingsConfig 在配置文件校验通过后重启服务。
func (s *Server) restartSettingsConfig(c *gin.Context) {
	if s.ConfigFile == "" {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
		return
	}
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	if allowed, wait := s.allow(ratelimit.SettingsConfigRestart, fmt.Sprintf("settings-config-restart:%d", user.ID)); !allowed {
		rateLimited(c, wait)
		return
	}
	var body struct {
		Revision string `json:"revision"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if !s.requireReauth(c, user, body.Password, body.TOTP) {
		return
	}
	if s.RestartConfig == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
		return
	}
	if err := config.ValidateFile(s.ConfigFile); err != nil {
		s.logger().Warn("重启前配置校验失败", "error", err)
		failKey(c, http.StatusBadRequest, "api.error.config_invalid_reason", "reason", configErrorText(err))
		return
	}
	revision, err := config.ConfigRevision(s.ConfigFile)
	if err != nil {
		failKey(c, http.StatusInternalServerError, "api.error.config_unavailable")
		return
	}
	if body.Revision != "" && revision != body.Revision {
		failKey(c, http.StatusConflict, "api.error.config_conflict")
		return
	}
	s.Store.AddLogKey("settings", 0, "warning", "log.settings.config_restart", nil)
	ok(c, gin.H{
		"message":    i18n.T(locale(c), "api.ok.config_restarting", nil),
		"restarting": true,
	})
	s.goSafe("settings-config-restart", func() {
		time.Sleep(1500 * time.Millisecond)
		s.RestartConfig()
	})
}

func (s *Server) respondConfigError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, config.ErrConfigConflict):
		failKey(c, http.StatusConflict, "api.error.config_conflict")
	case errors.Is(err, config.ErrSetupConfigInvalid):
		failKey(c, http.StatusBadRequest, "api.error.config_invalid_reason", "reason", configErrorText(err))
	case errors.Is(err, config.ErrConfigBackupFailed):
		s.logger().Warn("创建配置备份失败", "error", err)
		failKey(c, http.StatusInternalServerError, "api.error.config_backup_create_failed")
	default:
		s.logger().Warn("写入配置文件失败", "error", err)
		failKey(c, http.StatusInternalServerError, "api.error.config_write_failed")
	}
}

func (s *Server) configDataDir() string {
	if strings.TrimSpace(s.DataDir) != "" {
		return s.DataDir
	}
	return filepath.Dir(s.ConfigFile)
}

func (s *Server) trustedProxiesFromConfig() []string {
	if s.ConfigFile != "" {
		if cfg, err := config.LoadSetupConfig(s.ConfigFile); err == nil {
			return cfg.TrustedProxies
		}
	}
	if s.TrustedProxies == nil {
		return []string{}
	}
	return s.TrustedProxies
}

func (s *Server) readConfigPendingRestart() (configPendingRestart, bool) {
	raw, err := s.Store.GetSetting(configPendingRestartSetting)
	if err != nil || strings.TrimSpace(raw) == "" {
		return configPendingRestart{}, false
	}
	var pending configPendingRestart
	if err := json.Unmarshal([]byte(raw), &pending); err != nil || pending.Revision == "" {
		return configPendingRestart{}, false
	}
	return pending, true
}

func (s *Server) markConfigPendingRestart(result *config.EditableUpdateResult) {
	if result == nil || result.Noop {
		return
	}
	raw, err := json.Marshal(configPendingRestart{
		Revision: result.Revision,
		At:       time.Now().UTC(),
		Fields:   result.ChangedFields,
	})
	if err != nil {
		return
	}
	if err := s.Store.SetSetting(configPendingRestartSetting, string(raw)); err != nil {
		s.logger().Warn("写入待重启标记失败", "error", err)
	}
}

func configBackupID(result *config.EditableUpdateResult) string {
	if result == nil || result.Backup == nil {
		return ""
	}
	return result.Backup.ID
}

func configErrorText(err error) string {
	text := strings.TrimSpace(err.Error())
	text = strings.TrimPrefix(text, config.ErrSetupConfigInvalid.Error()+": ")
	text = strings.TrimPrefix(text, config.ErrConfigBackupFailed.Error()+": ")
	return text
}

func configFileWritable(path string) bool {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	file, err := os.CreateTemp(dir, ".config-write-*")
	if err != nil {
		return false
	}
	name := file.Name()
	_ = file.Close()
	_ = os.Remove(name)
	return true
}

func samePeerAddress(peer netip.Addr, forwarded string) bool {
	if !peer.IsValid() {
		return false
	}
	forwarded = strings.Trim(strings.TrimSpace(forwarded), "[]")
	if host, _, err := net.SplitHostPort(forwarded); err == nil {
		forwarded = strings.Trim(host, "[]")
	}
	addr, err := netip.ParseAddr(forwarded)
	return err == nil && addr.Unmap() == peer
}

func requestPeerIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return addr.Unmap()
}

func splitHeaderList(value string, maxItems, maxLen int) []string {
	if strings.TrimSpace(value) == "" {
		return []string{}
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, truncateHeader(item, maxLen))
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func truncateHeader(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if maxLen > 0 && len(value) > maxLen {
		return value[:maxLen]
	}
	return value
}
