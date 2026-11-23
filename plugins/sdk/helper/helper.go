// Package helper 提供官方插件 helper 的启动与协议辅助。
//
// 三个 helper 共用同一套内存预算、JSON 编解码与错误退出约定，
// 避免实现随时间产生差异。
package helper

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/AXmishell/LACTSSL-Plugins/outbound"
	"github.com/AXmishell/LACTSSL-Plugins/protocol"
)

const maxJSONSize = 16 << 20

// ApplyMemoryLimit 根据主程序传入的预算设置 Go 堆软上限。
// 硬性的常驻内存限制由主程序轮询 RSS 并在超限时结束进程实现，
// 这里不使用 RLIMIT_AS：Go 运行时需要预留大量虚拟地址空间。
func ApplyMemoryLimit() {
	raw := strings.TrimSpace(os.Getenv(protocol.MemoryLimitEnv))
	if raw == "" {
		return
	}
	limit, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || limit <= 0 {
		return
	}
	debug.SetMemoryLimit(limit * 4 / 5)
}

// Recover 捕获插件 panic，避免把完整栈和可能含凭据的 panic 值写入日志。
func Recover() {
	if value := recover(); value != nil {
		Failf("插件内部错误: %v", value)
	}
}

// Fail 把错误写入 stderr 并以非 0 退出。
func Fail(message string) {
	fmt.Fprintln(os.Stderr, outbound.SanitizeErrorText(message))
	os.Exit(1)
}

// Failf 是 Fail 的格式化版本。
func Failf(format string, args ...any) {
	Fail(fmt.Sprintf(format, args...))
}

// WriteJSON 把 v 编码到 stdout，保留非 ASCII 字符且不转义 HTML。
func WriteJSON(v any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(v)
}

// ReadJSON 从 stdin 解码一个 JSON 对象，并限制最大输入大小。
func ReadJSON(v any) error {
	return readJSON(os.Stdin, v)
}

func readJSON(reader io.Reader, v any) error {
	// 用 Decoder 而不是 ReadAll：宿主写完一个 JSON 对象后不一定会关闭
	// stdin，ReadAll 会一直等待 EOF。LimitReader 把单次请求限制在 16 MiB。
	decoder := json.NewDecoder(io.LimitReader(reader, maxJSONSize+1))
	if err := decoder.Decode(v); err != nil {
		return fmt.Errorf("解析请求失败: %w", err)
	}
	return nil
}

// HasFeature 判断主程序是否声明了某个插件特性。
// 保留兼容行为：同时读取部署与 DNS 两套环境变量。
func HasFeature(name string) bool {
	return HasFeatureIn(protocol.DeployFeaturesEnv, name) || HasFeatureIn(protocol.DNSFeaturesEnv, name)
}

// HasFeatureIn 只读取指定的环境变量，避免 deploy/DNS 特性串用。
func HasFeatureIn(env, name string) bool {
	for _, item := range strings.Split(os.Getenv(env), ",") {
		if strings.TrimSpace(item) == name {
			return true
		}
	}
	return false
}
