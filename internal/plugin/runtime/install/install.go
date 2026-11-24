// Package plugininstall 提供插件文件的原子安装、替换、回滚与卸载。
package plugininstall

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"lactssl/internal/i18n"
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Filename 返回插件在目录中的文件名。
func Filename(kind, id, goos string) string {
	name := "lactssl-" + kind + "-" + id
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// SafePath 把文件名拼到目录下，拒绝路径穿越。
func SafePath(dir, name string) (string, error) {
	clean := strings.ReplaceAll(name, "\\", "/")
	if clean != filepath.Base(clean) || clean == "." || clean == ".." || !namePattern.MatchString(clean) {
		return "", i18n.E("plugin.install.invalid_name", "name", name)
	}
	return filepath.Join(dir, clean), nil
}

// Install 把 src 原子安装为 dir/name。
// 返回安装路径与备份路径；备份路径为空表示没有旧文件。
func Install(dir, name, src string) (string, string, error) {
	dest, err := SafePath(dir, name)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("创建插件目录失败: %w", err)
	}
	info, err := os.Lstat(dest)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	if exists && !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("插件路径不是普通文件: %s", dest)
	}
	backup := ""
	if exists {
		backup = filepath.Join(dir, fmt.Sprintf(".backup-%d-%s", time.Now().UnixNano(), name))
		if err := os.Rename(dest, backup); err != nil {
			return "", "", fmt.Errorf("备份旧插件失败: %w", err)
		}
	}
	if err := os.Chmod(src, 0o755); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return "", "", fmt.Errorf("设置插件权限失败: %w", err)
	}
	if err := os.Rename(src, dest); err != nil {
		if backup != "" {
			_ = os.Rename(backup, dest)
		}
		return "", "", fmt.Errorf("安装插件失败: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return dest, backup, nil
}

// Commit 删除安装成功后的备份文件。
func Commit(backup string) {
	if backup != "" {
		_ = os.Remove(backup)
	}
}

// Rollback 删除新文件并恢复备份。
func Rollback(dest, backup string) {
	if dest != "" {
		_ = os.Remove(dest)
	}
	if backup != "" {
		_ = os.Rename(backup, dest)
	}
}

// Remove 删除插件文件。
func Remove(dir, name string) error {
	dest, err := SafePath(dir, name)
	if err != nil {
		return err
	}
	if err := os.Remove(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
