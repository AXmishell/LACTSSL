package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
)

// securityEvent 构造一条安全事件，统一带上来源 IP、设备信息和可选管理台地址。
func (s *Server) securityEvent(c *gin.Context, userID int64, kind string, detail map[string]string) model.SecurityEvent {
	if detail == nil {
		detail = map[string]string{}
	}
	if id := c.GetString(requestIDKey); id != "" {
		detail["request_id"] = id
	}
	return model.SecurityEvent{
		UserID:    userID,
		Kind:      kind,
		IP:        s.clientIP(c),
		UserAgent: sanitizeSecurityUserAgent(c.Request.UserAgent()),
		Detail:    detail,
		Link:      strings.TrimSpace(s.PublicURL),
		CreatedAt: time.Now().UTC(),
		// 核心凭据变更不能被每小时上限吞掉；限流只针对登录/失败提醒等高频噪音。
		BypassRateLimit: model.IsSecurityNotifyEventKind(kind),
	}
}

// notifySecurity 写入安全事件。通知失败只记录日志，不影响已经完成的业务操作。
func (s *Server) notifySecurity(event model.SecurityEvent) {
	if s.Manager == nil {
		return
	}
	if err := s.Manager.NotifySecurity(event); err != nil {
		s.logger().Warn("安全事件写入失败", "error", err.Error(), "kind", event.Kind)
	}
}

func (s *Server) notifySecurityEvent(c *gin.Context, userID int64, kind string, detail map[string]string) {
	s.notifySecurity(s.securityEvent(c, userID, kind, detail))
}

// recordSecurityEvent 只记录事件，不投递。用于登录历史等需要保留设备指纹的场景。
func (s *Server) recordSecurityEvent(event model.SecurityEvent) {
	if s.Manager == nil {
		return
	}
	if err := s.Manager.RecordSecurityEvent(event); err != nil {
		s.logger().Warn("安全事件记录失败", "error", err.Error(), "kind", event.Kind)
	}
}

// deviceFingerprint 用服务端密钥对设备 Cookie 做 HMAC，避免数据库里出现
// 可直接复用的 Cookie 值，同时保证同一浏览器多次登录得到稳定指纹。
func (s *Server) deviceFingerprint(c *gin.Context) string {
	value := s.ensureDeviceCookie(c)
	if strings.TrimSpace(value) == "" || s.Auth == nil {
		return ""
	}
	return s.Auth.SignValue("device-fingerprint", value)
}

// notifyLogin 记录一次成功登录，并按策略决定是否提醒。
// forceRecord 用于初始化场景：只建立设备历史，不发送邮件。
func (s *Server) notifyLogin(c *gin.Context, user *model.User, kind, method string, forceRecord bool) {
	if s.Manager == nil || user == nil {
		return
	}
	fingerprint := s.deviceFingerprint(c)
	policy := s.Manager.LoadSecurityNotifyPolicy(0)
	known := false
	if fingerprint != "" {
		since := time.Now().UTC().AddDate(0, 0, -policy.NewDeviceDays)
		if ok, err := s.Store.HasRecentSecurityLogin(user.ID, fingerprint, since); err == nil {
			known = ok
		}
	}
	newDevice := !known
	variant := "known"
	if newDevice {
		variant = "new"
	}
	event := s.securityEvent(c, user.ID, kind, map[string]string{
		"method":     method,
		"new_device": strconv.FormatBool(newDevice),
		"variant":    variant,
	})
	event.Fingerprint = fingerprint
	event.AccountEmail = user.Email
	// 新设备登录是重要信号，不应被失败登录刷出的每小时上限遮盖；
	// “每次登录”模式下已知设备的重复提醒仍参与限流。
	event.BypassRateLimit = newDevice
	if !forceRecord && policy.Enabled && policy.LoginNotifyEnabled(newDevice) {
		event.Force = true
		s.notifySecurity(event)
		return
	}
	s.recordSecurityEvent(event)
}

// notifyLoginFailure 记录一次登录失败，达到阈值且不在静默期时才创建告警事件。
func (s *Server) notifyLoginFailure(c *gin.Context, userID int64, reason string) {
	if s.Manager == nil {
		return
	}
	result := s.Manager.RecordLoginFailure(userID, s.clientIP(c), time.Now().UTC())
	if !result.Notify {
		return
	}
	eventUserID := userID
	accountEmail := ""
	if eventUserID > 0 {
		if user, err := s.Store.GetUserByID(eventUserID); err == nil && user != nil {
			accountEmail = user.Email
		}
	}
	// 匿名失败按 IP 聚合，但告警仍应送达唯一管理员账户。
	if eventUserID == 0 {
		if user, err := s.Store.GetUser(); err == nil && user != nil {
			eventUserID = user.ID
			accountEmail = user.Email
		}
	}
	event := s.securityEvent(c, eventUserID, model.SecurityEventLoginFailed, map[string]string{
		"count":  strconv.Itoa(result.Count),
		"window": strconv.Itoa(result.WindowSeconds),
		"ips":    strings.Join(result.IPs, ", "),
		"reason": reason,
	})
	event.AccountEmail = accountEmail
	event.Force = true
	s.notifySecurity(event)
}

// resetLoginFailures 在成功登录后清空账户和当前 IP 的失败计数。
func (s *Server) resetLoginFailures(c *gin.Context, userID int64) {
	if s.Manager == nil {
		return
	}
	s.Manager.ResetLoginFailures(userID, s.clientIP(c))
}

// listSecurityEvents 返回最近的安全事件及逐目标投递状态。
// 只允许浏览器会话访问，避免 API 密钥读取登录 IP/UA 与收件人信息。
func (s *Server) listSecurityEvents(c *gin.Context) {
	user := getUser(c)
	if user == nil {
		failKey(c, http.StatusUnauthorized, "api.error.unauthorized")
		return
	}
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			limit = value
		}
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	events, err := s.Store.ListSecurityEvents(user.ID, limit)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	out := make([]gin.H, 0, len(events))
	for _, event := range events {
		out = append(out, gin.H{
			"id":           event.ID,
			"kind":         event.Kind,
			"notify":       event.Notify,
			"recipients":   event.Recipients,
			"channels":     event.Channels,
			"deliveries":   event.Deliveries,
			"attempts":     event.Attempts,
			"last_error":   event.LastError,
			"ip":           event.IP,
			"user_agent":   event.UserAgent,
			"detail":       event.Detail,
			"created_at":   event.CreatedAt,
			"delivered_at": event.DeliveredAt,
		})
	}
	ok(c, gin.H{"events": out})
}

// sanitizeSecurityUserAgent 去掉控制字符并限制长度，避免把异常 UA 写进通知或日志。
func sanitizeSecurityUserAgent(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > 256 {
		value = string(runes[:256])
	}
	return value
}
