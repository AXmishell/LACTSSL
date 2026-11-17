//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// reexecProcess 用磁盘上的新二进制替换当前进程。executable 由程序升级工具
// 在替换前记录；不能在替换后重新调用 os.Executable，否则会读到
// "/path/lactssl (deleted)" 这类已失效路径。调用前 HTTP 服务已关闭，
// syscall.Exec 成功时不会返回，失败时由调用方记录日志。
func reexecProcess(executable string) error {
	if executable == "" {
		resolved, err := os.Executable()
		if err != nil {
			return err
		}
		executable = resolved
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return syscall.Exec(executable, os.Args, os.Environ())
}
