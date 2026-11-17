//go:build !embed

package webui

import (
	"io/fs"
	"os"
)

// Assets 在未启用 embed 构建标签时直接读取 web/dist 目录，
// 便于 go run / go test 在不构建前端的情况下编译。
// 发布二进制需使用 go build -tags embed（make build 会自动处理）。
func Assets() fs.FS {
	// go run ./cmd/server 通常在仓库根目录执行；go test 则位于包目录。
	// 两种工作目录都兼容，避免测试或本地启动时找不到前端产物。
	if info, err := os.Stat("web/dist"); err == nil && info.IsDir() {
		return os.DirFS("web/dist")
	}
	return os.DirFS("../../web/dist")
}

// Source 返回本地前端构建目录；未找到构建产物时 Kind 为 SourceMissing。
func Source() AssetSource {
	if info, err := os.Stat("web/dist"); err == nil && info.IsDir() {
		return AssetSource{Kind: SourceLocal, Path: "web/dist"}
	}
	if info, err := os.Stat("../../web/dist"); err == nil && info.IsDir() {
		return AssetSource{Kind: SourceLocal, Path: "../../web/dist"}
	}
	return AssetSource{Kind: SourceMissing}
}
