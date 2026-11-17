//go:build embed

// Package webui 提供内嵌或本地的前端静态资源。
package webui

import (
	"embed"
	"io/fs"
)

// files 嵌入前端构建产物，发布时使用 go build -tags embed 构建。
// 构建前需将 web/dist 复制到 internal/webui/dist。
//
//go:embed all:dist
var files embed.FS

// Assets 返回嵌入的前端资源。
func Assets() fs.FS {
	sub, err := fs.Sub(files, "dist")
	if err != nil {
		panic("加载嵌入的前端资源失败: " + err.Error())
	}
	return sub
}

// Source 返回嵌入的前端资源来源。
func Source() AssetSource {
	return AssetSource{Kind: SourceEmbedded}
}
