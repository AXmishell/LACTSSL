package service

import (
	"path/filepath"
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
