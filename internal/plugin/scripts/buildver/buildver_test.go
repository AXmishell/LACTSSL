package buildver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNext(t *testing.T) {
	tests := []struct {
		name     string
		prev     string
		prevHash string
		base     string
		newHash  string
		want     string
	}{
		{name: "新插件", prev: "", prevHash: "", base: "1.3", newHash: "h1", want: "1.3.0"},
		{name: "迁移保留原版本", prev: "1.3.4", prevHash: "", base: "1.3", newHash: "h1", want: "1.3.4"},
		{name: "内容未变化", prev: "1.3.4", prevHash: "h1", base: "1.3", newHash: "h1", want: "1.3.4"},
		{name: "内容变化递增补丁", prev: "1.3.4", prevHash: "h1", base: "1.3", newHash: "h2", want: "1.3.5"},
		{name: "补丁进位", prev: "1.3.9", prevHash: "h1", base: "1.3", newHash: "h2", want: "1.3.10"},
		{name: "次版本基线变化重置", prev: "1.3.9", prevHash: "h1", base: "1.4", newHash: "h2", want: "1.4.0"},
		{name: "带 v 前缀", prev: "v1.3.9", prevHash: "h1", base: "1.3", newHash: "h2", want: "1.3.10"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Next(tt.prev, tt.prevHash, tt.base, tt.newHash); got != tt.want {
				t.Fatalf("Next(%q, %q, %q, %q) = %q, want %q", tt.prev, tt.prevHash, tt.base, tt.newHash, got, tt.want)
			}
		})
	}
}

func TestHashPathsIgnoresTestsAndTestdata(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/provider.go", "package pkg\n")
	writeFile(t, root, "pkg/provider_test.go", "package pkg\n")
	writeFile(t, root, "pkg/testdata/fixture.json", "{}\n")

	first, err := HashPaths(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, root, "pkg/provider_test.go", "package pkg // changed\n")
	writeFile(t, root, "pkg/testdata/fixture.json", "{\"changed\":true}\n")

	second, err := HashPaths(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("测试文件变化不应改变指纹: %s != %s", first, second)
	}
}

func TestHashPathsChangesWithSource(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/provider.go", "package pkg\n")

	first, err := HashPaths(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "pkg/provider.go", "package pkg // changed\n")

	second, err := HashPaths(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("源码变化后指纹应改变")
	}
}

func TestHashPathsOrderAndOverlap(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/provider.go", "package pkg\n")
	writeFile(t, root, "pkg/other.go", "package pkg\n")

	a, err := HashPaths(root, []string{"pkg/provider.go", "pkg"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPaths(root, []string{"pkg", "pkg/provider.go"})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("指纹应与路径顺序无关: %s != %s", a, b)
	}
}

func TestHashPathsMissingPath(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pkg/provider.go", "package pkg\n")

	a, err := HashPaths(root, []string{"pkg"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPaths(root, []string{"pkg", "does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("不存在的可选路径不应改变指纹: %s != %s", a, b)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
