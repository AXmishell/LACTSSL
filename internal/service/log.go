package service

import (
	"log/slog"

	"lactssl/internal/logging"
)

// logger 返回业务层的底层日志实例。
// 每次调用都从全局 logger 派生，确保运行期重新初始化日志后仍指向最新输出。
func logger() *slog.Logger {
	return logging.Component("service")
}
