package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// requireAPIKeyCARead 允许 ca:read 或更宽的 ca:write 读取/借用上游 CA。
func (s *Server) requireAPIKeyCARead(c *gin.Context) bool {
	key := apiKeyFromContext(c)
	if key == nil || key.HasScope(apiScopeCARead) || key.HasScope(apiScopeCAWrite) {
		return true
	}
	failKey(c, http.StatusForbidden, "api.error.api_key_scope_denied", "scope", apiScopeCARead)
	return false
}
