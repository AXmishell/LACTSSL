package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPublicBaseURLUsesConfiguredValue(t *testing.T) {
	s := &Server{PublicURL: "https://ssl.example.com"}
	req := httptest.NewRequest("GET", "http://internal/reset", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	base, err := s.publicBaseURL(c)
	if err != nil || base != "https://ssl.example.com" {
		t.Fatalf("应使用配置的 public_url: %q %v", base, err)
	}
}

func TestPublicBaseURLRejectsRemoteRequestWithoutConfig(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "http://attacker.example/reset", nil)
	req.RemoteAddr = "198.51.100.7:1234"
	req.Host = "attacker.example"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	if _, err := s.publicBaseURL(c); err == nil {
		t.Fatal("未配置 public_url 时远程请求不应按 Host 推导链接")
	}
}

func TestPublicBaseURLAllowsLoopback(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "http://127.0.0.1:8080/reset", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:8080"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	base, err := s.publicBaseURL(c)
	if err != nil || base != "http://127.0.0.1:8080" {
		t.Fatalf("本机请求应按 Host 推导: %q %v", base, err)
	}
}

func TestPublicBaseURLRejectsMalformedHost(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "http://127.0.0.1/reset", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "evil.example/path"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	if _, err := s.publicBaseURL(c); err == nil {
		t.Fatal("非法 Host 应被拒绝")
	}
}

func TestPublicBaseURLRejectsSpoofedHostFromLoopback(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest("GET", "http://attacker.example/reset", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "attacker.example"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	if _, err := s.publicBaseURL(c); err == nil {
		t.Fatal("本机请求使用非回环 Host 时也不应生成链接")
	}
}
