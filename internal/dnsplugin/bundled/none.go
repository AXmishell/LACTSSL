//go:build !embed_helper

// Package bundled 提供发布版主程序内嵌的 DNS helper。
package bundled

// Helper 返回嵌入的 DNS helper 内容；未启用 embed_helper 构建标签时为 nil。
func Helper() []byte {
	return nil
}
