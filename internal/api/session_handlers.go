package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lactssl/internal/model"
)

// issueSession 签发令牌、登记服务端会话并写入会话 Cookie。
func (s *Server) issueSession(c *gin.Context, user *model.User) (time.Time, error) {
	return s.issueSessionStrong(c, user, false)
}

// issueSessionStrong 签发会话，并记录该会话是否通过通行密钥完成强认证。
func (s *Server) issueSessionStrong(c *gin.Context, user *model.User, strong bool) (time.Time, error) {
	token, expire, id, err := s.Auth.IssueSession(user)
	if err != nil {
		return time.Time{}, err
	}
	now := time.Now().UTC()
	session := &model.Session{
		ID:         id,
		UserID:     user.ID,
		IP:         c.ClientIP(),
		UserAgent:  c.Request.UserAgent(),
		StrongAuth: strong,
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  expire.UTC(),
	}
	if err := s.Store.CreateSession(session); err != nil {
		return time.Time{}, err
	}
	s.setSessionCookie(c, token, expire)
	s.setCSRFCookie(c, token, expire)
	return expire, nil
}

// currentSessionID 返回认证中间件写入的当前会话 ID。
func currentSessionID(c *gin.Context) string {
	value, _ := c.Get("session_id")
	id, _ := value.(string)
	return id
}

// sessionStrongAuth 表示当前会话是否通过通行密钥完成了强认证。
// 强认证会话本身已满足二次验证要求，无需再次弹出验证框。
func sessionStrongAuth(c *gin.Context) bool {
	value, exists := c.Get("session_strong_auth")
	if !exists {
		return false
	}
	strong, _ := value.(bool)
	return strong
}

// listSessions 返回当前账户的所有活跃会话。
func (s *Server) listSessions(c *gin.Context) {
	user := getUser(c)
	items, err := s.Store.ListSessions(user.ID, time.Now().UTC())
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	current := currentSessionID(c)
	for i := range items {
		items[i].Current = items[i].ID == current
	}
	ok(c, items)
}

// revokeSession 撤销指定会话，使其令牌立即失效。
func (s *Server) revokeSession(c *gin.Context) {
	user := getUser(c)
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		failKey(c, http.StatusBadRequest, "api.error.invalid_id")
		return
	}
	if id == currentSessionID(c) {
		failKey(c, http.StatusBadRequest, "api.error.cannot_revoke_current")
		return
	}
	found, err := s.Store.RevokeSession(user.ID, id)
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if !found {
		failKey(c, http.StatusNotFound, "api.error.session_not_found")
		return
	}
	s.Store.AddLogKey("auth", user.ID, "info", "log.auth.session_revoked", nil)
	ok(c, gin.H{"ok": true})
}

// revokeOtherSessions 撤销除当前会话外的所有会话。
func (s *Server) revokeOtherSessions(c *gin.Context) {
	user := getUser(c)
	count, err := s.Store.RevokeOtherSessions(user.ID, currentSessionID(c))
	if err != nil {
		failErr(c, http.StatusInternalServerError, err)
		return
	}
	if count > 0 {
		s.Store.AddLogKey("auth", user.ID, "info", "log.auth.sessions_revoked", map[string]any{"count": count})
	}
	ok(c, gin.H{"revoked": count})
}
