//go:build embed_helper

// Package bundled 提供发布版主程序内嵌的 DNS helper。
//
// 默认主程序不编译 DNS 服务商，发布构建（embed_helper）会把
// lactssl-dns-helper-extra 嵌入主程序，并在首次启动时安装到插件目录，
// 因此 Release 包无需手动复制 helper 即可使用内置服务商。
package bundled

import _ "embed"

//go:embed lactssl-dns-helper-extra
var helper []byte

// Helper 返回嵌入的 DNS helper 内容；未启用 embed_helper 构建标签时为 nil。
func Helper() []byte {
	return helper
}
