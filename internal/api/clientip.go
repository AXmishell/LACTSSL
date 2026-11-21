package api

import (
	"crypto/tls"
	"strings"

	"github.com/gin-gonic/gin"
	"lactssl/internal/netutil"
)

const (
	clientIPKey       = "client_ip"
	clientIPSourceKey = "client_ip_source"
	clientIPResultKey = "client_ip_result"
)

// trustedProxies 返回预编译的可信代理集合。
// 配置非法时安全降级为空集合，并保留错误供诊断接口展示。
func (s *Server) trustedProxies() netutil.TrustedSet {
	s.trustedOnce.Do(func() {
		set, err := netutil.CompileTrusted(s.TrustedProxies)
		if err != nil {
			s.trustedErr = err
			s.logger().Warn("可信代理配置无效，已禁用代理头信任", "error", err)
			return
		}
		s.trustedSet = set
	})
	return s.trustedSet
}

// resolveClientIP 在每个请求开始时解析一次客户端 IP，后续统一从上下文读取。
func (s *Server) clientIPOptions(c *gin.Context) netutil.Options {
	opts := netutil.DefaultOptions()
	opts.EnableForwarded = true
	opts.Cloudflare = strings.EqualFold(strings.TrimSpace(s.ClientIPMode), "cloudflare")
	if c != nil {
		opts.PeerVerified = peerVerifiedByMTLS(c.Request.TLS)
	}
	return opts
}

func peerVerifiedByMTLS(state *tls.ConnectionState) bool {
	return state != nil && len(state.VerifiedChains) > 0
}

// isTrustedRequest 表示当前请求来自可信代理：TCP 对端在列表中，或 mTLS 代理身份已验证。
func (s *Server) isTrustedRequest(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if peerVerifiedByMTLS(c.Request.TLS) {
		return true
	}
	return s.isTrustedProxy(c.Request.RemoteAddr)
}

func (s *Server) resolveClientIP() gin.HandlerFunc {
	return func(c *gin.Context) {
		result := netutil.ResolveWithOptions(c.Request.RemoteAddr, c.Request.Header, s.trustedProxies(), s.clientIPOptions(c))
		c.Set(clientIPResultKey, result)
		if result.IP.IsValid() {
			c.Set(clientIPKey, result.IP.String())
		} else {
			c.Set(clientIPKey, "")
		}
		c.Set(clientIPSourceKey, string(result.Source))
		c.Next()
	}
}

// clientIP 返回当前请求的最终客户端 IP。未经过中间件时按当前配置即时解析。
func (s *Server) clientIP(c *gin.Context) string {
	if c == nil {
		return ""
	}
	if value, ok := c.Get(clientIPKey); ok {
		if ip, ok := value.(string); ok {
			return ip
		}
	}
	result := netutil.ResolveWithOptions(c.Request.RemoteAddr, c.Request.Header, s.trustedProxies(), s.clientIPOptions(c))
	if !result.IP.IsValid() {
		return ""
	}
	return result.IP.String()
}

// clientIPResult 返回包含来源与告警的完整解析结果。
func (s *Server) clientIPResult(c *gin.Context) netutil.Result {
	if c == nil {
		return netutil.Result{}
	}
	if value, ok := c.Get(clientIPResultKey); ok {
		if result, ok := value.(netutil.Result); ok {
			return result
		}
	}
	return netutil.ResolveWithOptions(c.Request.RemoteAddr, c.Request.Header, s.trustedProxies(), s.clientIPOptions(c))
}
