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
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"lactssl/internal/plugin/runtime/sandbox"
	"lactssl/internal/truststore"
)

// ErrTimeout 表示 helper 超过调用方设置的单次执行时间。
var ErrTimeout = errors.New("plugin execution timed out")

// ErrSandboxRoot 表示上传插件要求沙箱执行，但宿主进程仍是 root。
// 此时 Landlock 只能限制文件访问，无法阻止 root 插件通过 mount/ptrace
// 等方式绕过，因此默认拒绝执行；请以非 root 运行或配置 LACTSSL_RUN_UID/GID。
var ErrSandboxRoot = errors.New("refusing to run uploaded plugin with sandbox as root; run the server as a non-root user or set LACTSSL_RUN_UID/LACTSSL_RUN_GID")

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
	// Sandbox 仅用于上传/外部 helper；内置插件在宿主进程内直接调用，不进入这里。
	Sandbox SandboxOptions
}

// SandboxOptions 描述上传插件的沙箱策略。
// strict 需要 Linux 与 SandboxLauncher；其他情况自动降级为 basic。
type SandboxOptions struct {
	Profile       string
	ReadOnlyPaths []string
	WritableDir   string
	CPUSeconds    uint64
	FileSizeMB    uint64
	OpenFiles     uint64
	Processes     uint64
}

// SandboxLauncher 是主程序路径，用于启动隐藏的 __plugin_sandbox 子命令。
// 由 cmd/server 在启动时注入；测试或未注入时降级为 basic。
var SandboxLauncher string

var (
	sandboxProbeOnce    sync.Once
	sandboxUsable       bool
	sandboxFallbackOnce sync.Once
)

// sandboxReady 判断能否使用 strict 沙箱。
// 直接检查 Landlock 内核能力，不再启动 /bin/sh 探测：避免最小镜像缺少
// shell 时被误判为不支持，也避免在首次插件调用时同步 fork 完整主程序。
func sandboxReady() bool {
	sandboxProbeOnce.Do(func() {
		if SandboxLauncher == "" || !sandbox.Supports() {
			return
		}
		if info, err := os.Stat(SandboxLauncher); err != nil || info.IsDir() {
			return
		}
		sandboxUsable = true
	})
	return sandboxUsable
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

	commandPath := path
	commandArgs := args
	workDir := ""
	extraEnv := []string(nil)
	profile := strings.ToLower(strings.TrimSpace(opts.Sandbox.Profile))
	// 沙箱无法限制 root 进程（Landlock 不拦截 mount/ptrace 等），
	// 因此上传插件默认拒绝以 root 执行；编译内置插件不经过这里。
	if profile != "" && profile != "off" && runningAsRoot() {
		return "", "", ErrSandboxRoot
	}
	if profile != "" && profile != "off" {
		workDir = strings.TrimSpace(opts.Sandbox.WritableDir)
		if workDir == "" {
			if dir, err := os.MkdirTemp("", "lactssl-plugin-*"); err == nil {
				workDir = dir
				defer os.RemoveAll(dir)
			}
		}
		if workDir != "" {
			_ = os.Chmod(workDir, 0o700)
			extraEnv = append(extraEnv, "HOME="+workDir, "TMPDIR="+workDir)
		}
	}
	strict := profile == "strict" && workDir != "" && sandboxReady()
	if profile == "strict" && !strict {
		sandboxFallbackOnce.Do(func() {
			log.Printf("上传插件 strict 沙箱不可用（Landlock 不受支持或 SandboxLauncher 无效），已降级为 basic 资源限制")
		})
	}
	if strict {
		if opts.Sandbox.CPUSeconds == 0 {
			opts.Sandbox.CPUSeconds = uint64(opts.Timeout.Seconds()) + 5
		}
		if opts.Sandbox.FileSizeMB == 0 {
			opts.Sandbox.FileSizeMB = 128
		}
		if opts.Sandbox.OpenFiles == 0 {
			opts.Sandbox.OpenFiles = 256
		}
		if opts.Sandbox.Processes == 0 {
			opts.Sandbox.Processes = 512
		}
		commandPath = SandboxLauncher
		commandArgs = buildSandboxArgs(path, args, opts, workDir)
	}
	cmd := exec.CommandContext(runCtx, commandPath, commandArgs...) // #nosec G204 -- path 为已安装插件路径，参数由插件调用协议构造
	if workDir != "" {
		cmd.Dir = workDir
	}
	setProcessGroup(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			killProcessGroup(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = waitDelay
	cmd.Env = mergeEnv(helperEnv(opts), extraEnv)
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

// SetProcessGroup 让子进程运行在独立进程组中，便于超时或取消时整组结束。
// 供插件之外的命令执行路径（例如证书部署重载命令）复用同一套进程组语义。
func SetProcessGroup(cmd *exec.Cmd) {
	setProcessGroup(cmd)
}

// KillProcessGroup 结束指定进程所在进程组。pid<=0 时安全忽略。
func KillProcessGroup(pid int) {
	killProcessGroup(pid)
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
func buildSandboxArgs(path string, args []string, opts Options, workDir string) []string {
	absolute := path
	if resolved, err := filepath.Abs(path); err == nil {
		absolute = resolved
	}
	out := []string{"__plugin_sandbox", "--rw", workDir}
	if path := truststore.BundlePath(); path != "" {
		out = append(out, "--ro", path)
	}
	for _, item := range opts.Sandbox.ReadOnlyPaths {
		if strings.TrimSpace(item) != "" {
			out = append(out, "--ro", item)
		}
	}
	if opts.Sandbox.CPUSeconds > 0 {
		out = append(out, "--cpu", strconv.FormatUint(opts.Sandbox.CPUSeconds, 10))
	}
	if opts.Sandbox.FileSizeMB > 0 {
		out = append(out, "--fsize", strconv.FormatUint(opts.Sandbox.FileSizeMB, 10))
	}
	if opts.Sandbox.OpenFiles > 0 {
		out = append(out, "--nofile", strconv.FormatUint(opts.Sandbox.OpenFiles, 10))
	}
	if opts.Sandbox.Processes > 0 {
		out = append(out, "--nproc", strconv.FormatUint(opts.Sandbox.Processes, 10))
	}
	out = append(out, "--", absolute)
	out = append(out, args...)
	return out
}

func mergeEnv(base, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	overrides := make(map[string]bool, len(extra))
	for _, item := range extra {
		name, _, _ := strings.Cut(item, "=")
		overrides[name] = true
	}
	out := make([]string, 0, len(base)+len(extra))
	for _, item := range base {
		name, _, _ := strings.Cut(item, "=")
		if overrides[name] {
			continue
		}
		out = append(out, item)
	}
	return append(out, extra...)
}

func helperEnv(opts Options) []string {
	allow := []string{
		"PATH", "HOME", "TMPDIR", "TZ", "LANG", "LC_ALL",
		"SSL_CERT_FILE", "SSL_CERT_DIR", "CURL_CA_BUNDLE", "REQUESTS_CA_BUNDLE",
		"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"http_proxy", "https_proxy", "no_proxy",
	}
	env := make([]string, 0, len(allow)+len(opts.Env)+2)
	for _, key := range allow {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	// 即使进程环境被外部清理，也把当前信任链传给 helper，
	// 避免导入的内部 CA 在插件侧失效。
	if _, ok := os.LookupEnv("SSL_CERT_FILE"); !ok {
		if path := truststore.BundlePath(); path != "" {
			env = append(env, "SSL_CERT_FILE="+path)
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
	// 必须返回原始长度：os/exec 通过 io.Copy 写入 stdout/stderr，
	// 返回短写会被 io.Copy 视为 io.ErrShortWrite，使正常的超长输出把
	// Run 变成失败。截断只影响保留内容，不影响“已消费”的字节数。
	total := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return total, nil
	}
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	if _, err := b.buf.Write(p); err != nil {
		return 0, err
	}
	return total, nil
}

func (b *cappedBuffer) String() string {
	if b.truncated {
		return b.buf.String() + "…(已截断)"
	}
	return b.buf.String()
}
