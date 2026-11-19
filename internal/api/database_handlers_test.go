package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMaintenanceGateAllowsDatabaseStatusOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{DataDir: t.TempDir()}
	s.SetMaintenance(true)
	r := gin.New()
	r.Use(s.maintenanceGate())
	r.GET("/api/upgrade/database", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/certificates", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/tasks", func(c *gin.Context) { c.Status(http.StatusOK) })

	status := httptest.NewRecorder()
	r.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/upgrade/database", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("转换状态接口应放行，实际 %d", status.Code)
	}
	blocked := httptest.NewRecorder()
	r.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/api/certificates", nil))
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("普通接口应被维护模式拒绝，实际 %d", blocked.Code)
	}
	writeBlocked := httptest.NewRecorder()
	r.ServeHTTP(writeBlocked, httptest.NewRequest(http.MethodPost, "/api/tasks", nil))
	if writeBlocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("写接口应被维护模式拒绝，实际 %d", writeBlocked.Code)
	}
}
