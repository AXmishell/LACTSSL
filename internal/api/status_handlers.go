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
	stats := readProcessStats()
	buildVersion, buildTime, buildArch := buildInfo()
	ok(c, gin.H{
		"version":        buildVersion,
		"build_time":     buildTime,
		"build_arch":     buildArch,
		"started_at":     s.startedAt.Format(time.RFC3339),
		"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		"go_version":     runtime.Version(),
		"goroutines":     runtime.NumGoroutine(),
		"threads":        stats.threads,
		"memory_alloc":   mem.Alloc,
		"memory_heap":    mem.HeapInuse,
		"memory_sys":     mem.Sys,
		"memory_rss":     stats.rss,
		"memory_anon":    stats.anon,
		"memory_vsz":     stats.vsz,
		"memory_swap":    stats.swap,
	})
}

// processStats 是进程的内存与线程快照，内存单位均为字节。
type processStats struct {
	rss     uint64
	anon    uint64
	vsz     uint64
	swap    uint64
	threads int
}

// readProcessStats 读取当前进程的物理内存、匿名内存、虚拟内存、交换内存与线程数。
// 非 Linux 平台没有 /proc/self/status，返回零值。
func readProcessStats() processStats {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return processStats{}
	}
	return parseProcessStats(string(data))
}

// parseProcessStats 从 /proc/self/status 内容中解析内存与线程字段。
// 其中 VmRSS、RssAnon、VmSize、VmSwap 以 kB 为单位，转换后返回字节数。
func parseProcessStats(status string) processStats {
	var stats processStats
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
			stats.rss = value * 1024
		case "RssAnon:":
			stats.anon = value * 1024
		case "VmSize:":
			stats.vsz = value * 1024
		case "VmSwap:":
			stats.swap = value * 1024
		case "Threads:":
			stats.threads = int(value)
		}
	}
	return stats
}
