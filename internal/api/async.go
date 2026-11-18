package api

import (
	"fmt"
	"runtime/debug"
)

// goSafe 启动一个后台 goroutine，并捕获其中的 panic。
// HTTP 请求 goroutine 有 Gin recovery 兜底，但从接口触发的 goroutine
// 没有，未捕获的 panic 会直接终止整个进程。
func (s *Server) goSafe(job string, fn func()) {
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger().Error("后台任务 panic",
					"job", job,
					"panic", fmt.Sprint(recovered),
					"stack", string(debug.Stack()),
				)
			}
		}()
		fn()
	}()
}
