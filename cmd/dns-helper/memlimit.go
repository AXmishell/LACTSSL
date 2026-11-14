package main

import (
	"os"
	"runtime/debug"
	"strconv"
)

// applyMemoryLimit 根据主程序传入的预算设置 Go 堆软上限。
// 硬性的常驻内存限制由主程序通过轮询 RSS 并在超限时结束进程实现，
// 这里不使用 RLIMIT_AS：Go 运行时需要预留大量虚拟地址空间。
func applyMemoryLimit() {
	raw := os.Getenv("LACTSSL_PLUGIN_MEMORY_LIMIT")
	if raw == "" {
		return
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		return
	}
	debug.SetMemoryLimit(limit * 4 / 5)
}
