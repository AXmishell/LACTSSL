//go:build windows

package main

import "errors"

// reexecProcess 在 Windows 上无法用新二进制替换当前进程，
// 升级后由服务管理器（例如 Windows 服务）重新拉起。
func reexecProcess(_ string) error {
	return errors.New("Windows 不支持进程内重启，请通过服务管理器重启")
}
