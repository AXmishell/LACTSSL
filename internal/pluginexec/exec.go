// Package pluginexec 为外部插件 helper 提供一次性子进程执行环境。
//
// DNS 插件与部署插件都需要相同的安全约束：超时、输出上限、常驻内存监控、
// 进程组结束、最小环境变量与敏感信息脱敏。把这些逻辑集中在一个包里，
// 避免两套实现随时间产生安全差异。
package pluginexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// ErrTimeout 表示 helper 超过调用方设置的单次执行时间。
var ErrTimeout = errors.New("plugin execution timed out")

// MemoryLimitError 表示 helper 的常驻内存超过配置上限，已被强制结束。
type MemoryLimitError struct {
	LimitMB int
}

func (e *MemoryLimitError) Error() string {
	return fmt.Sprintf("plugin memory usage exceeded %d MB", e.LimitMB)
}

// Options 控制单次 helper 执行的资源限制。
type Options struct {
	// Timeout 是单次执行超时时间，<=0 时使用 60 秒。
	Timeout time.Duration
	// MemoryLimitMB 是常驻内存上限，<=0 表示不限制。
	MemoryLimitMB int
	// MaxOutputKB 是 stdout/stderr 各自的保留上限，<=0 时使用 64 KB。
	MaxOutputKB int
	// Env 是附加给 helper 的环境变量，例如插件协议版本。
	// 主程序不会继承任何未在允许列表中的变量。
	Env []string
}

const (
	defaultTimeout   = 60 * time.Second
	defaultOutputKB  = 64
	memoryLimitEnv   = "LACTSSL_PLUGIN_MEMORY_LIMIT"
	gomemlimitEnv    = "GOMEMLIMIT"
	rssPollInterval  = 200 * time.Millisecond
	waitDelay        = time.Second
	redactMinLength  = 4
	allowedEnvSuffix = "LC_"
)

func (o Options) normalized() Options {
	if o.Timeout <= 0 {
		o.Timeout = defaultTimeout
	}
	if o.MaxOutputKB <= 0 {
		o.MaxOutputKB = defaultOutputKB
	}
	return o
}

// Run 执行 helper 并返回截断后的 stdout/stderr。
// 调用方负责解释输出内容；Run 只统一处理资源限制与进程生命周期。
func Run(ctx context.Context, path string, args []string, stdin []byte, opts Options) (string, string, error) {
	opts = opts.normalized()
	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, path, args...)
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = waitDelay
	cmd.Env = helperEnv(opts)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	stdout := newCappedBuffer(opts.MaxOutputKB << 10)
	stderr := newCappedBuffer(opts.MaxOutputKB << 10)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return "", stderr.String(), err
	}
	var exceeded atomic.Bool
	stop := watchRSS(cmd.Process.Pid, opts.MemoryLimitMB, &exceeded, func() error {
		killProcessGroup(cmd.Process.Pid)
		return nil
	})
	waitErr := cmd.Wait()
	stop()

	if exceeded.Load() {
		return stdout.String(), stderr.String(), &MemoryLimitError{LimitMB: opts.MemoryLimitMB}
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return stdout.String(), stderr.String(), ErrTimeout
	}
	if err := ctx.Err(); err != nil {
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), waitErr
}

// watchRSS 周期性检查子进程常驻内存，超过上限时结束进程。
// 返回的函数用于停止监控。非 Linux 平台读取 /proc 失败时会静默跳过。
func watchRSS(pid, limitMB int, exceeded *atomic.Bool, kill func() error) func() {
	if limitMB <= 0 || pid <= 0 {
		return func() {}
	}
	limit := uint64(limitMB) << 10 // /proc 中 VmRSS 单位为 kB
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(rssPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
				if err != nil {
					continue
				}
				rss, ok := parseVmRSS(data)
				if ok && rss > limit {
					exceeded.Store(true)
					_ = kill()
					return
				}
			}
		}
	}()
	return func() { close(done) }
}

func parseVmRSS(status []byte) (uint64, bool) {
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, false
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}
		return value, true
	}
	return 0, false
}

// helperEnv 构造最小环境变量集合，避免把主程序的敏感变量泄漏给插件。
func helperEnv(opts Options) []string {
	allow := []string{
		"PATH", "HOME", "TMPDIR", "TZ", "LANG", "LC_ALL",
		"SSL_CERT_FILE", "SSL_CERT_DIR",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
	}
	env := make([]string, 0, len(allow)+len(opts.Env)+2)
	for _, key := range allow {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	for _, item := range os.Environ() {
		name, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(name, allowedEnvSuffix) {
			env = append(env, item)
		}
	}
	env = append(env, opts.Env...)
	if opts.MemoryLimitMB > 0 {
		bytesLimit := int64(opts.MemoryLimitMB) << 20
		env = append(env, memoryLimitEnv+"="+strconv.FormatInt(bytesLimit, 10))
		// Go helper 使用软限制，给运行时留出略低于硬限制的空间。
		env = append(env, gomemlimitEnv+"="+strconv.FormatInt(bytesLimit*4/5, 10))
	}
	return env
}

// Redact 把消息中出现的敏感值替换为 ***，用于错误信息与日志。
// 只处理长度不小于 4 的值，避免把常见短字符串全部替换掉。
func Redact(message string, secrets map[string]string) string {
	for _, value := range secrets {
		if value == "" {
			continue
		}
		if len(value) >= redactMinLength {
			message = strings.ReplaceAll(message, value, "***")
		}
		trimmed := strings.TrimSpace(value)
		if trimmed != value && len(trimmed) >= redactMinLength {
			message = strings.ReplaceAll(message, trimmed, "***")
		}
	}
	return message
}

// Limit 按字节截断字符串，用于错误信息展示。
func Limit(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

type cappedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedBuffer(limit int) *cappedBuffer {
	return &cappedBuffer{limit: limit}
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	return b.buf.Write(p)
}

func (b *cappedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "…(已截断)"
	}
	return b.buf.String()
}
