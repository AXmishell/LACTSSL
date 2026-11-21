package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/config"
	"lactssl/internal/i18n"
	"lactssl/internal/ratelimit"
)

// setupConfig 返回初始化页面可编辑的 config.yaml 白名单字段。
func (s *Server) setupConfig(c *gin.Context) {
	if s.ConfigFile == "" {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
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
	cfg, err := config.LoadSetupConfig(s.ConfigFile)
	if err != nil {
		s.logger().Warn("读取初始化配置失败", "error", err)
		failKey(c, http.StatusInternalServerError, "api.error.config_unavailable")
		return
	}
	ok(c, gin.H{
		"config": cfg,
	})
}

// updateSetupConfig 在系统尚未初始化时更新白名单配置项。
// 保存后不会立即生效，由前端提示用户重启服务。
func (s *Server) updateSetupConfig(c *gin.Context) {
	if allowed, wait := s.allow(ratelimit.SetupConfig, "setup-config:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	if s.ConfigFile == "" {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
		return
	}
	if !s.requireSetupToken(c) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	count, err := s.Store.CountUsers()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		failKey(c, http.StatusBadRequest, "api.error.already_initialized")
		return
	}

	var patch config.SetupConfigPatch
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		failKey(c, http.StatusBadRequest, "api.error.invalid_params")
		return
	}
	if patch.Empty() {
		ok(c, gin.H{
			"message":          i18n.T(locale(c), "api.ok.config_unchanged", nil),
			"restart_required": false,
		})
		return
	}

	if err := config.UpdateSetupConfig(s.ConfigFile, patch); err != nil {
		s.logger().Warn("保存初始化配置失败", "error", err)
		if errors.Is(err, config.ErrSetupConfigInvalid) {
			failKey(c, http.StatusBadRequest, "api.error.config_invalid")
			return
		}
		failKey(c, http.StatusInternalServerError, "api.error.config_write_failed")
		return
	}
	ok(c, gin.H{
		"message":          i18n.T(locale(c), "api.ok.config_saved", nil),
		"restart_required": true,
	})
}

// restartSetupConfig 在系统尚未初始化时重启服务，使 config.yaml 修改生效。
func (s *Server) restartSetupConfig(c *gin.Context) {
	if allowed, wait := s.allow(ratelimit.SetupRestart, "setup-restart:"+s.clientRateKey(c)); !allowed {
		rateLimited(c, wait)
		return
	}
	if !s.requireSetupToken(c) {
		return
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	count, err := s.Store.CountUsers()
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		failKey(c, http.StatusBadRequest, "api.error.already_initialized")
		return
	}
	if s.Restart == nil {
		failKey(c, http.StatusServiceUnavailable, "api.error.config_unavailable")
		return
	}
	ok(c, gin.H{
		"message":    i18n.T(locale(c), "api.ok.config_restarting", nil),
		"restarting": true,
	})
	s.goSafe("setup-config-restart", func() {
		time.Sleep(1500 * time.Millisecond)
		s.Restart()
	})
}
