//go:build unix

package pluginexec

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让 helper 及其子进程处于独立进程组，便于超时或超内存时整组结束。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup 结束 helper 所在进程组，避免其子进程继续持有管道导致等待卡住。
func killProcessGroup(pid int) {
	if pid <= 0 {
		return
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
