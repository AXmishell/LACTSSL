package api

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// errPublicURLUnavailable 表示未配置固定的对外访问地址，无法安全生成绝对链接。
var errPublicURLUnavailable = errors.New("public url is not configured")

// publicBaseURL 返回用于邮件等绝对链接的基地址。
// 优先使用 security.public_url；未配置时只允许来自本机回环地址的请求按 Host 推导，
// 避免攻击者通过伪造 Host 或 X-Forwarded-Host 把密码重置链接指向自己的域名。
func (s *Server) publicBaseURL(c *gin.Context) (string, error) {
	if s.PublicURL != "" {
		return s.PublicURL, nil
	}
	if c == nil || !isLoopbackRemoteAddr(c.Request.RemoteAddr) {
		return "", errPublicURLUnavailable
	}
	host := strings.TrimSpace(c.Request.Host)
	if !validPublicHost(host) || !isLoopbackHost(host) {
		return "", errPublicURLUnavailable
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host, nil
}

// isLoopbackRemoteAddr 判断请求是否来自本机回环地址。
func isLoopbackRemoteAddr(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// isLoopbackHost 判断 Host 是否为 localhost 或回环 IP，避免本机请求被伪造的 Host 利用。
func isLoopbackHost(host string) bool {
	parsed, err := url.Parse("//" + host)
	if err != nil {
		return false
	}
	name := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	if name == "localhost" {
		return true
	}
	ip := net.ParseIP(name)
	return ip != nil && ip.IsLoopback()
}

// validPublicHost 校验 Host 头只包含合法的主机名或 IP，可选带端口。
func validPublicHost(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	if strings.ContainsAny(host, "/\\@ \t\r\n") {
		return false
	}
	parsed, err := url.Parse("//" + host)
	if err != nil || parsed.Host != host || parsed.Hostname() == "" || parsed.User != nil {
		return false
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return false
		}
	}
	return true
}
