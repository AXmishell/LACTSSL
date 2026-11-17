package webui

import (
	"io/fs"
	"testing"
)

// TestSourceMatchesAssets 验证资源来源声明与实际可读资源一致。
func TestSourceMatchesAssets(t *testing.T) {
	source := Source()
	switch source.Kind {
	case SourceEmbedded, SourceLocal:
		if _, err := fs.Stat(Assets(), "index.html"); err != nil {
			t.Fatalf("来源 %s 应包含 index.html: %v", source.Kind, err)
		}
	case SourceMissing:
		t.Skip("本地前端尚未构建，跳过资源检查")
	default:
		t.Fatalf("未知的前端资源来源: %q", source.Kind)
	}
}
