// Package discovery 提供外部插件发现与执行共用的文件、摘要和错误格式化工具。
package discovery

import (
	"strings"

	pluginexec "lactssl/internal/plugin/runtime/exec"
	"lactssl/internal/util"
)

// FileSHA256 计算插件 helper 的 SHA-256。
func FileSHA256(path string) (string, error) {
	return util.FileSHA256(path)
}

// StderrSuffix 把 helper stderr 截断为统一的错误后缀。
func StderrSuffix(stderr string) string {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return ""
	}
	return ": " + pluginexec.Limit(stderr, 512)
}

// Suffix 把错误信息格式化为 ": message"；空消息返回空串。
func Suffix(message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		return ""
	}
	return ": " + message
}

// CloneMap 复制 map，避免把调用方可变配置传给 helper。
func CloneMap[K comparable, V any](in map[K]V) map[K]V {
	out := make(map[K]V, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
