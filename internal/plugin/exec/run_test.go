package pluginexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipWithoutShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("依赖 POSIX shell")
	}
}

func TestRunPassesStdinAndReturnsOutput(t *testing.T) {
	skipWithoutShell(t)
	stdout, stderr, err := Run(context.Background(), "/bin/sh", []string{"-c", "read line; printf 'got:%s' \"$line\""},
		[]byte("hello\n"), Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if stdout != "got:hello" || stderr != "" {
		t.Fatalf("输出不正确: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestRunReturnsExitError(t *testing.T) {
	skipWithoutShell(t)
	_, stderr, err := Run(context.Background(), "/bin/sh", []string{"-c", "echo boom >&2; exit 3"}, nil, Options{Timeout: 5 * time.Second})
	if err == nil {
		t.Fatal("非零退出码应返回错误")
	}
	if !strings.Contains(stderr, "boom") {
		t.Fatalf("stderr 未保留: %q", stderr)
	}
}

func TestRunTimeout(t *testing.T) {
	skipWithoutShell(t)
	start := time.Now()
	_, _, err := Run(context.Background(), "/bin/sh", []string{"-c", "sleep 5"}, nil, Options{Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("应返回 ErrTimeout，实际: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("超时后未及时返回: %v", elapsed)
	}
}

func TestRunCapsOutput(t *testing.T) {
	skipWithoutShell(t)
	stdout, _, err := Run(context.Background(), "/bin/sh", []string{"-c", "printf 'x%.0s' $(seq 1 5000)"}, nil,
		Options{Timeout: 5 * time.Second, MaxOutputKB: 1})
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if len(stdout) > 1024+len("…(已截断)") {
		t.Fatalf("stdout 未被截断: %d", len(stdout))
	}
	if !strings.Contains(stdout, "已截断") {
		t.Fatalf("缺少截断标记: %q", stdout)
	}
}

func TestRunFiltersEnvironment(t *testing.T) {
	skipWithoutShell(t)
	t.Setenv("LACTSSL_SECRET_TOKEN", "should-not-leak")
	stdout, _, err := Run(context.Background(), "/bin/sh", []string{"-c", "printf '%s' \"$LACTSSL_SECRET_TOKEN\""}, nil,
		Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("Run 失败: %v", err)
	}
	if stdout != "" {
		t.Fatalf("敏感环境变量泄漏给插件: %q", stdout)
	}
	// 调用方显式传入的 Env 仍然生效。
	stdout, _, err = Run(context.Background(), "/bin/sh", []string{"-c", "printf '%s' \"$LACTSSL_PLUGIN_TEST\""}, nil,
		Options{Timeout: 5 * time.Second, Env: []string{"LACTSSL_PLUGIN_TEST=explicit"}})
	if err != nil || stdout != "explicit" {
		t.Fatalf("显式 Env 未生效: %q %v", stdout, err)
	}
}

func TestRunTimeoutKillsProcessGroup(t *testing.T) {
	skipWithoutShell(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "leaked.txt")
	script := "sh -c 'sleep 2; echo leaked > " + marker + "' & wait"
	_, _, err := Run(context.Background(), "/bin/sh", []string{"-c", script}, nil,
		Options{Timeout: 200 * time.Millisecond})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("应返回 ErrTimeout，实际: %v", err)
	}
	time.Sleep(2500 * time.Millisecond)
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("超时后子进程仍继续运行并写入了文件")
	}
}

func TestLimit(t *testing.T) {
	if got := Limit("abcdef", 3); got != "abc…" {
		t.Fatalf("Limit 截断不正确: %q", got)
	}
	if got := Limit("ab", 3); got != "ab" {
		t.Fatalf("短文本不应截断: %q", got)
	}
}

func TestCappedBuffer(t *testing.T) {
	buf := newCappedBuffer(4)
	if n, err := buf.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("Write 应报告完整长度: n=%d err=%v", n, err)
	}
	if got := buf.String(); got != "abcd…(已截断)" {
		t.Fatalf("截断结果不正确: %q", got)
	}
}

func TestRunRefusesSandboxAsRoot(t *testing.T) {
	if !runningAsRoot() {
		t.Skip("仅 root 环境验证 fail-closed")
	}
	_, _, err := Run(context.Background(), "/bin/true", nil, nil, Options{
		Timeout: 5 * time.Second,
		Sandbox: SandboxOptions{Profile: "basic"},
	})
	if !errors.Is(err, ErrSandboxRoot) {
		t.Fatalf("root 下沙箱执行应被拒绝，实际: %v", err)
	}
	// 显式关闭沙箱（sandbox: off）时保留原有行为，供受控调试使用。
	if _, _, err := Run(context.Background(), "/bin/true", nil, nil, Options{
		Timeout: 5 * time.Second,
		Sandbox: SandboxOptions{Profile: "off"},
	}); err != nil {
		t.Fatalf("off 配置不应被拒绝: %v", err)
	}
}
