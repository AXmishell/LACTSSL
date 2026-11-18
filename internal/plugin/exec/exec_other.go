//go:build !unix

package pluginexec

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(pid int) {
	_ = pid
}
