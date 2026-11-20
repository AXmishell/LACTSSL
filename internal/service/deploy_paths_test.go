package service

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestValidateLocalDeployPaths 验证本地部署不能写入数据目录、配置目录或程序目录，
// 避免部署参数覆盖数据库、config.yaml 或二进制。
func TestValidateLocalDeployPaths(t *testing.T) {
	protected := t.TempDir()
	other := t.TempDir()
	m := &Manager{ProtectedPaths: []string{protected}}

	if err := validateLocalDeployPaths(m, filepath.Join(protected, "cert.pem")); err == nil {
		t.Fatal("受保护目录内的证书路径应被拒绝")
	}
	if err := validateLocalDeployPaths(m, filepath.Join(protected, "sub", "key.pem")); err == nil {
		t.Fatal("受保护目录子目录内的私钥路径应被拒绝")
	}
	if err := validateLocalDeployPaths(m, protected); err == nil {
		t.Fatal("受保护目录本身应被拒绝")
	}
	if err := validateLocalDeployPaths(m, filepath.Join(other, "cert.pem")); err != nil {
		t.Fatalf("普通目录应允许部署: %v", err)
	}
	if err := validateLocalDeployPaths(nil, filepath.Join(protected, "cert.pem")); err != nil {
		t.Fatalf("未配置受保护目录时应保持兼容: %v", err)
	}
}

// TestValidateLocalDeployPathsSymlink 验证目录符号链接不能绕过受保护目录。
func TestValidateLocalDeployPathsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("符号链接测试仅适用于 Unix")
	}
	root := t.TempDir()
	protected := filepath.Join(root, "data")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(protected, link); err != nil {
		t.Fatal(err)
	}
	m := &Manager{ProtectedPaths: []string{protected}}
	if err := validateLocalDeployPaths(m, filepath.Join(link, "lactssl.db")); err == nil {
		t.Fatal("经符号链接写入受保护目录应被拒绝")
	}
}

// TestValidateLocalDeployPathsProcRoot 验证 /proc/self/root 魔法链接不能绕过保护。
func TestValidateLocalDeployPathsProcRoot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc 测试仅适用于 Linux")
	}
	if _, err := os.Stat("/proc/self/root"); err != nil {
		t.Skip("/proc/self/root 不可用")
	}
	protected := t.TempDir()
	m := &Manager{ProtectedPaths: []string{protected}}
	target := filepath.Join("/proc/self/root", protected, "lactssl.db")
	if err := validateLocalDeployPaths(m, target); err == nil {
		t.Fatalf("/proc/self/root 绕过应被拒绝: %s", target)
	}
}
