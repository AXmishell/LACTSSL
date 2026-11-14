package service

import (
	"context"
	"net"
	"net/http"
	"time"

	"lactssl/internal/outbound"
)

// 以下函数保留在 service 包中，作为 internal/outbound 的薄包装，
// 避免通知、监控与接口层直接依赖具体实现。

// ValidateOutboundURL 校验用户配置的出站 HTTP(S) 地址。
func ValidateOutboundURL(raw string) error { return outbound.ValidateURL(raw) }

// ValidateWebhookURL 校验部署 webhook 地址。
func ValidateWebhookURL(raw string) error { return outbound.ValidateWebhookURL(raw) }

// SanitizeErrorText 脱敏错误文本中的 URL 路径与查询参数。
func SanitizeErrorText(text string) string { return outbound.SanitizeErrorText(text) }

// SafeError 返回适合写入日志或接口响应的错误文本。
func SafeError(err error) string { return outbound.SafeError(err) }

func newOutboundClient(timeout time.Duration, skipVerify bool) *http.Client {
	return outbound.NewClient(timeout, skipVerify)
}

func dialSafe(ctx context.Context, network, address string) (net.Conn, error) {
	return outbound.DialSafe(ctx, network, address)
}
