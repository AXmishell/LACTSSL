//go:build !unix

package pluginexec

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(pid int) {
	_ = pid
}

// runningAsRoot 在非 Unix 平台不存在 root 概念。
func runningAsRoot() bool { return false }
