package api

import (
	"os"
	"runtime"
	"time"
)

// 构建信息，由构建命令通过 -ldflags 注入（版本号来自仓库根目录的 VERSION 文件），
// 未注入时回退到开发默认值或运行时信息。
var (
	// Version 是应用版本号，对应 VERSION 文件的内容。
	Version = "dev"
	// BuildTime 是二进制构建时间，RFC3339 格式。
	BuildTime = ""
	// BuildArch 是二进制构建目标平台，形如 linux/amd64。
	BuildArch = ""
)

// buildInfo 返回用于展示的版本号、构建时间与构建架构。
func buildInfo() (string, string, string) {
	version := Version
	if version == "" {
		version = "dev"
	}
	arch := BuildArch
	if arch == "" {
		arch = runtime.GOOS + "/" + runtime.GOARCH
	}
	return version, resolveBuildTime(), arch
}

// resolveBuildTime 返回二进制构建时间。
// 优先使用 ldflags 注入的值；go run、go build 等未注入时回退到可执行文件的修改时间，
// 避免「关于」页面显示为“—”。
func resolveBuildTime() string {
	if BuildTime != "" {
		return BuildTime
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return info.ModTime().UTC().Format(time.RFC3339)
}
