package api

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// handleStatus 返回后端进程的运行状态，供「关于」页面展示。
func (s *Server) handleStatus(c *gin.Context) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	rss, swap := processMemory()
	buildVersion, buildTime, buildArch := buildInfo()
	ok(c, gin.H{
		"version":        buildVersion,
		"build_time":     buildTime,
		"build_arch":     buildArch,
		"started_at":     s.startedAt.Format(time.RFC3339),
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"go_version":     runtime.Version(),
		"goroutines":     runtime.NumGoroutine(),
		"memory_alloc":   mem.Alloc,
		"memory_heap":    mem.HeapInuse,
		"memory_sys":     mem.Sys,
		"memory_rss":     rss,
		"memory_swap":    swap,
	})
}

// processMemory 读取当前进程的物理内存与交换内存占用，单位为字节。
// 非 Linux 平台没有 /proc/self/status，返回 0。
func processMemory() (rss, swap uint64) {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0, 0
	}
	return parseProcessMemory(string(data))
}

// parseProcessMemory 从 /proc/self/status 内容中解析 VmRSS 与 VmSwap。
func parseProcessMemory(status string) (rss, swap uint64) {
	for _, line := range strings.Split(status, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "VmRSS:":
			rss = value * 1024
		case "VmSwap:":
			swap = value * 1024
		}
	}
	return rss, swap
}
