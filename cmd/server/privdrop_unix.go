//go:build unix

package main

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
)

// dropPrivileges 在容器入口以 root 启动时，切换到 LACTSSL_RUN_UID /
// LACTSSL_RUN_GID 指定的低权限用户。非 root 或未设置环境变量时不做任何事。
//
// Docker 镜像不再内置 su-exec，因此把降权放在主程序最前面完成：
// 入口脚本只负责修正挂载目录归属，随后 exec 本进程。
func dropPrivileges() error {
	uidRaw := os.Getenv("LACTSSL_RUN_UID")
	gidRaw := os.Getenv("LACTSSL_RUN_GID")
	if uidRaw == "" && gidRaw == "" {
		return nil
	}
	if uidRaw == "" || gidRaw == "" {
		return fmt.Errorf("LACTSSL_RUN_UID 与 LACTSSL_RUN_GID 必须同时设置")
	}
	if os.Geteuid() != 0 {
		return nil
	}

	uid, err := parseID("LACTSSL_RUN_UID", uidRaw)
	if err != nil {
		return err
	}
	gid, err := parseID("LACTSSL_RUN_GID", gidRaw)
	if err != nil {
		return err
	}

	// 顺序不能调换：先补充组，再设置 gid，最后设置 uid。
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("设置附加组失败: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("设置 gid 失败: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("设置 uid 失败: %w", err)
	}
	return nil
}

// runningAsRoot 判断降权后是否仍为 root，用于启动提示。
func runningAsRoot() bool {
	return os.Geteuid() == 0
}

func parseID(name, raw string) (int, error) {
	id, err := strconv.Atoi(raw)
	if err != nil || id < 0 {
		return 0, fmt.Errorf("%s 不是合法的用户/组 ID: %q", name, raw)
	}
	return id, nil
}
