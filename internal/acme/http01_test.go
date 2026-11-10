package acme

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testHTTP01Server(t *testing.T, body string) (*httptest.Server, int) {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/acme-challenge/token-123" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return ts, port
}

func TestHTTP01ValidateSuccess(t *testing.T) {
	_, port := testHTTP01Server(t, "token-123.thumbprint")
	v := NewHTTP01Validator()
	v.Port = port
	v.AllowedPorts = []string{strconv.Itoa(port)}
	v.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}
	if err := v.Validate(context.Background(), "localhost", "token-123", "token-123.thumbprint"); err != nil {
		t.Fatalf("HTTP-01 验证失败: %v", err)
	}
}

func TestHTTP01ValidateWrongBody(t *testing.T) {
	_, port := testHTTP01Server(t, "wrong")
	v := NewHTTP01Validator()
	v.Port = port
	v.AllowedPorts = []string{strconv.Itoa(port)}
	v.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}
	err := v.Validate(context.Background(), "localhost", "token-123", "token-123.thumbprint")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("错误响应体应验证失败: %v", err)
	}
}

func TestHTTP01BlocksPrivateByDefault(t *testing.T) {
	_, port := testHTTP01Server(t, "token-123.thumbprint")
	v := NewHTTP01Validator()
	v.Port = port
	v.AllowedPorts = []string{strconv.Itoa(port)}
	err := v.Validate(context.Background(), "localhost", "token-123", "token-123.thumbprint")
	if err == nil {
		t.Fatal("默认应拒绝回环地址")
	}
}

func TestHTTP01AllowsPrivateWithCIDR(t *testing.T) {
	_, port := testHTTP01Server(t, "token-123.thumbprint")
	v := NewHTTP01Validator()
	v.Port = port
	v.AllowedPorts = []string{strconv.Itoa(port)}
	v.AllowedCIDRs = []string{"127.0.0.0/8"}
	if err := v.Validate(context.Background(), "127.0.0.1", "token-123", "token-123.thumbprint"); err != nil {
		t.Fatalf("CIDR 白名单应允许 IP 标识: %v", err)
	}
	if err := v.Validate(context.Background(), "localhost", "token-123", "token-123.thumbprint"); err != nil {
		t.Fatalf("CIDR 白名单应允许回环地址: %v", err)
	}
}

func TestHTTP01RejectsInvalidToken(t *testing.T) {
	v := NewHTTP01Validator()
	if err := v.Validate(context.Background(), "example.com", "bad/token", "x"); err == nil {
		t.Fatal("非法 token 应被拒绝")
	}
}

func TestHTTP01Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("token-123.thumbprint"))
	}))
	defer ts.Close()
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	v := NewHTTP01Validator()
	v.Port = port
	v.AllowedPorts = []string{portText}
	v.AllowedCIDRs = []string{"127.0.0.0/8", "::1/128"}
	v.Timeout = 50 * time.Millisecond
	if err := v.Validate(context.Background(), "localhost", "token-123", "token-123.thumbprint"); err == nil {
		t.Fatal("超时应返回错误")
	}
}
